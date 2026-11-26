//go:build integration

// rabbitmq 真 broker 集成（change: rabbitmq-task-integration）。
//
// 覆盖三块契约：
//   - TaskTopology 声明与同参重声明幂等（guardian 重连重放依赖此契约）；
//   - 发布 → 消费往返：body 一致、成功路径 Ack 后不重投；
//   - 断开期契约与应用级恢复：Close 后 Publish 立即返回 ErrNotConnected
//     （绝不静默缓存重发）；断开期第三方发布的 durable 消息不丢，新客户端
//     重声明同一拓扑（幂等）后续消费成功。
//
// 环境要求：AMQP_DSN（缺失 Skip），URL 形式 amqp://user:pass@host:port/vhost。
//
// 覆盖边界说明：连接级 guardian 断线自动重连需要注入 fake dialer（主库
// rabbitmq/conn_test.go 已覆盖）；本组件未暴露触发真连接级断开的公开手段
// （内部 conn 私有、无测试钩子），故本文件以 Close 模拟进程退出，覆盖对外
// 可见的断开期契约与恢复语义。拓扑删除是运维决策、组件不提供 Delete——
// 测试用 amqp091-go 直连的第三方连接做清理与断开期发布。
package tests

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
)

// amqpTestConfig 读取 AMQP_DSN 并解析为 rabbitmq.Config（缺失 Skip）。
// vhost 取 URL 路径（去前导 "/"）；"/" 或空路径归一为默认 vhost "/"。
func amqpTestConfig(t *testing.T) rabbitmq.Config {
	t.Helper()
	dsn := os.Getenv("AMQP_DSN")
	if dsn == "" {
		t.Skip("未设置 AMQP_DSN（如 amqp://guest:guest@127.0.0.1:5672/），跳过 rabbitmq 集成")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "amqp" || u.Host == "" {
		t.Fatalf("AMQP_DSN 非法（需 amqp://user:pass@host:port/vhost 形式）: %v", err)
	}
	cfg := rabbitmq.DefaultConfig()
	cfg.Host = u.Hostname()
	if p := u.Port(); p != "" {
		n, perr := strconv.Atoi(p)
		if perr != nil {
			t.Fatalf("AMQP_DSN 端口非法: %v", perr)
		}
		cfg.Port = n
	}
	if u.User != nil {
		cfg.User = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			cfg.Password = pw
		}
	}
	if vhost := strings.TrimPrefix(u.Path, "/"); vhost != "" {
		cfg.Vhost = vhost
	}
	return cfg
}

// openAmqpAdmin 开一条与被测 Client 无关的 amqp091-go 直连（第三方视角），
// 用于拓扑清理、断开期发布与队列观测。
func openAmqpAdmin(t *testing.T) *amqp.Connection {
	t.Helper()
	conn, err := amqp.Dial(os.Getenv("AMQP_DSN"))
	if err != nil {
		t.Fatalf("第三方直连 AMQP_DSN 失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// amqpTestNames 生成独占的 exchange/queue/routingKey 名（随机后缀防并行冲突）。
func amqpTestNames() (exchange, queue, key string) {
	s := uuid.NewString()[:8]
	return "gf-lib-itest-ex-" + s, "gf-lib-itest-q-" + s, "itest.key." + s
}

// deleteAmqpTopology 删除测试拓扑（强制删除：测试命名独占，不影响他人）。
func deleteAmqpTopology(t *testing.T, conn *amqp.Connection, exchange, queue string) {
	t.Helper()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("清理 channel 开启失败: %v", err)
	}
	defer ch.Close()
	if _, err := ch.QueueDelete(queue, false, false, false); err != nil {
		t.Logf("清理 queue %s 失败（忽略）: %v", queue, err)
	}
	if err := ch.ExchangeDelete(exchange, false, false); err != nil {
		t.Logf("清理 exchange %s 失败（忽略）: %v", exchange, err)
	}
}

// publishViaAdmin 经第三方连接发布 persistent 消息（断开期投递用）。
func publishViaAdmin(t *testing.T, conn *amqp.Connection, exchange, key string, body []byte) {
	t.Helper()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("第三方 channel 开启失败: %v", err)
	}
	defer ch.Close()
	if err := ch.PublishWithContext(context.Background(), exchange, key, false, false,
		amqp.Publishing{Body: body, DeliveryMode: amqp.Persistent, Timestamp: time.Now()}); err != nil {
		t.Fatalf("第三方发布失败: %v", err)
	}
}

// amqpQueueReadyMessages 查队列 ready 消息数（passive 声明，参数与 TaskTopology 对齐）。
func amqpQueueReadyMessages(t *testing.T, conn *amqp.Connection, queue string) int {
	t.Helper()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("观测 channel 开启失败: %v", err)
	}
	defer ch.Close()
	q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
	if err != nil {
		t.Fatalf("passive 声明 queue %s 失败: %v", queue, err)
	}
	return q.Messages
}

// waitDelivery 在 5s 内等 handler 收到一条消息并校验 body 一致。
func waitDelivery(t *testing.T, got <-chan []byte, want []byte, stage string) {
	t.Helper()
	select {
	case body := <-got:
		if !bytes.Equal(body, want) {
			t.Fatalf("%s: body 不一致，want %q got %q", stage, want, body)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: 5s 内未收到消息", stage)
	}
}

// TestRabbitMQ_PublishConsumeRoundTrip 发布→消费往返：
// TaskTopology 声明（含同参重声明幂等）→ Publish → Consumer 收到一致 body →
// 成功路径 Ack 后无重投、队列 ready 清空。
func TestRabbitMQ_PublishConsumeRoundTrip(t *testing.T) {
	cfg := amqpTestConfig(t)
	admin := openAmqpAdmin(t)
	exchange, queue, key := amqpTestNames()
	t.Cleanup(func() { deleteAmqpTopology(t, admin, exchange, queue) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := rabbitmq.NewClient(cfg)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer client.Close()

	topo := rabbitmq.TaskTopology(exchange, "direct", queue, key)
	if err := client.Declare(ctx, topo); err != nil {
		t.Fatalf("拓扑声明失败: %v", err)
	}
	// 声明幂等：同名同参重复无害（guardian 重连重放依赖此契约）
	if err := client.Declare(ctx, topo); err != nil {
		t.Fatalf("同参重声明应幂等成功: %v", err)
	}

	var handled atomic.Int32
	got := make(chan []byte, 1)
	consumer := client.NewConsumer(queue, func(_ context.Context, d rabbitmq.Delivery) error {
		handled.Add(1)
		select {
		case got <- d.Body():
		default: // 只观测首条
		}
		return nil
	}, rabbitmq.WithPrefetch(1), rabbitmq.WithConsumerName("itest-roundtrip"))
	go func() { _ = consumer.Run(ctx) }()

	producer := client.NewProducer()
	want := []byte(`{"hello":"gf-lib"}`)
	if err := producer.Publish(ctx, exchange, key, want); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	waitDelivery(t, got, want, "往返消费")

	// Ack 契约取证：成功路径不 Nack/requeue——短暂窗口后 handler 仍恰被调 1 次
	time.Sleep(500 * time.Millisecond)
	if n := handled.Load(); n != 1 {
		t.Fatalf("成功路径应恰处理 1 次（Ack 生效不重投），实际 %d 次", n)
	}
	if n := amqpQueueReadyMessages(t, admin, queue); n != 0 {
		t.Fatalf("消费完成后队列 ready 应为 0，实际 %d", n)
	}
}

// TestRabbitMQ_DisconnectContractAndResume 断开期契约与应用级恢复：
//   - 断开（Close 模拟进程退出）后 Publish 立即返回 ErrNotConnected；
//   - 断开期第三方发布的 durable 消息不丢；
//   - 新客户端重声明同一拓扑（幂等）后续订阅，收到断开期的消息。
func TestRabbitMQ_DisconnectContractAndResume(t *testing.T) {
	cfg := amqpTestConfig(t)
	admin := openAmqpAdmin(t)
	exchange, queue, key := amqpTestNames()
	t.Cleanup(func() { deleteAmqpTopology(t, admin, exchange, queue) })

	topo := rabbitmq.TaskTopology(exchange, "direct", queue, key)

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	clientA := rabbitmq.NewClient(cfg)
	if err := clientA.Connect(ctxA); err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	if err := clientA.Declare(ctxA, topo); err != nil {
		t.Fatalf("拓扑声明失败: %v", err)
	}

	gotA := make(chan []byte, 1)
	consumerA := clientA.NewConsumer(queue, func(_ context.Context, d rabbitmq.Delivery) error {
		select {
		case gotA <- d.Body():
		default:
		}
		return nil
	}, rabbitmq.WithPrefetch(1), rabbitmq.WithConsumerName("itest-resume-a"))
	go func() { _ = consumerA.Run(ctxA) }()

	producerA := clientA.NewProducer()
	m1 := []byte(`{"msg":"before-disconnect"}`)
	if err := producerA.Publish(ctxA, exchange, key, m1); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	waitDelivery(t, gotA, m1, "断开前消费")

	clientA.Close() // 模拟进程退出/连接断开（连接级 guardian 语义见主库单测）

	// 断开期契约：Publish 立即报错，绝不静默缓存重发
	if err := producerA.Publish(ctxA, exchange, key, []byte(`{"msg":"should-fail"}`)); !errors.Is(err, rabbitmq.ErrNotConnected) {
		t.Fatalf("断开期 Publish 应返回 ErrNotConnected，实际 %v", err)
	}

	// 断开期第三方发布 durable 消息（persistent + durable queue，broker 侧排队不丢）
	m2 := []byte(`{"msg":"after-resume"}`)
	publishViaAdmin(t, admin, exchange, key, m2)

	// 恢复：新客户端（模拟进程重启）重声明同一拓扑并续订阅
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	clientB := rabbitmq.NewClient(cfg)
	if err := clientB.Connect(ctxB); err != nil {
		t.Fatalf("恢复连接失败: %v", err)
	}
	defer clientB.Close()
	if err := clientB.Declare(ctxB, topo); err != nil {
		t.Fatalf("恢复后重声明应幂等成功: %v", err)
	}
	gotB := make(chan []byte, 1)
	consumerB := clientB.NewConsumer(queue, func(_ context.Context, d rabbitmq.Delivery) error {
		select {
		case gotB <- d.Body():
		default:
		}
		return nil
	}, rabbitmq.WithPrefetch(1), rabbitmq.WithConsumerName("itest-resume-b"))
	go func() { _ = consumerB.Run(ctxB) }()

	waitDelivery(t, gotB, m2, "恢复后续消费")
}

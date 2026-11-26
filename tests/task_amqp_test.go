//go:build integration

// task/amqp 真 PG + 真 RabbitMQ 全链路集成（change: rabbitmq-task-integration）。
//
// 覆盖两条链路：
//   - 成功路径：TypeRegistry 注册 type → Dispatcher（QueueTypes）ViaQueue 提交
//     → amqp.Producer 发 task_id 消息 → amqp.Consumer 即时消费 →
//     task.Worker.ExecuteByID 执行 → succeeded；
//   - 失败重试路径：首次消费 Handler 失败（消息一律 Ack 不 requeue，骨架契约）
//     → task 落 failed → RetryScanner.Scan 退避转回 pending（available_at 推进）
//     并经注册 type 的 Producer 重投消息 → 二次消费成功 → succeeded。
//     本测试无 pull worker 在跑，最终 succeeded 即「重投真实经过 AMQP 并被
//     即时消费」的取证。退避默认 5s 起步，但即时消费模式 ExecuteByID 不看
//
// RetryScanner runs repeatedly; ByID must respect available_at.
//
// 环境要求：AMQP_DSN 与 PG_DSN 同时设置（任一缺失 Skip）。
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
	taskamqp "github.com/shyun-cc/shw-gf-lib/task/amqp"
)

// taskAmqpEnv 全链路用例的共享装配（一次性表 + 独占拓扑 + confirm 生产者）
type taskAmqpEnv struct {
	ctx        context.Context
	cancel     context.CancelFunc
	repo       *task.PgRepository
	client     *rabbitmq.Client
	producer   *taskamqp.AmqpProducer
	registry   *task.TypeRegistry
	exchange   string
	queue      string
	routingKey string
}

// setupTaskAmqp 装配全链路环境：PG 建表（task.DDL 幂等）+ RabbitMQ 独占拓扑 +
// confirm 模式 amqp.Producer（Submit 的队列投递确保真到 broker）。
// 清理顺序（Cleanup LIFO）：停消费与客户端 → 删拓扑 → DROP 表。
// parseDSNForAmqpTest URL 形式 PG_DSN → ConfigNode（复用 migrate 系的 parsePGDSN，
// 避免各自维护解析；ConfigNode.Link 直塞只认 GF Link 格式，URL 形式会报 invalid link）。
func parseDSNForAmqpTest(t *testing.T, dsn string) gdb.ConfigNode {
	t.Helper()
	node, err := parsePGDSN(dsn)
	if err != nil {
		t.Fatalf("解析 PG_DSN 失败: %v", err)
	}
	return node
}

func setupTaskAmqp(t *testing.T) *taskAmqpEnv {
	t.Helper()
	amqpDSN := os.Getenv("AMQP_DSN")
	pgDSN := os.Getenv("PG_DSN")
	if amqpDSN == "" || pgDSN == "" {
		t.Skip("task/amqp 全链路需同时设置 AMQP_DSN（amqp://user:pass@host:port/vhost）与 PG_DSN（postgres://user:pass@127.0.0.1:5432/db），跳过")
	}
	if err := gdb.SetConfig(gdb.Config{
		gdb.DefaultGroupName: gdb.ConfigGroup{
			parseDSNForAmqpTest(t, pgDSN),
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := g.DB().Exec(ctx, task.DDL); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	t.Cleanup(func() {
		_, _ = g.DB().Exec(ctx, `DROP TABLE IF EXISTS task_attempt, task`)
	})

	cfg := amqpTestConfig(t) // AMQP_DSN 已确认非空，Skip 分支不触发
	admin := openAmqpAdmin(t)
	s := uuid.NewString()[:8]
	env := &taskAmqpEnv{
		exchange:   "gf-lib-itest-task-ex-" + s,
		queue:      "gf-lib-itest-task-q-" + s,
		routingKey: "itest.task." + s,
	}
	t.Cleanup(func() { deleteAmqpTopology(t, admin, env.exchange, env.queue) })

	env.ctx, env.cancel = context.WithCancel(ctx)
	env.client = rabbitmq.NewClient(cfg)
	if err := env.client.Connect(env.ctx); err != nil {
		env.cancel()
		t.Fatalf("RabbitMQ 连接失败: %v", err)
	}
	t.Cleanup(func() {
		env.cancel()       // 停消费循环（Consumer.Run 返回）
		env.client.Close() // 优雅停机
	})
	if err := env.client.Declare(env.ctx,
		rabbitmq.TaskTopology(env.exchange, "direct", env.queue, env.routingKey)); err != nil {
		t.Fatalf("拓扑声明失败: %v", err)
	}
	env.producer = taskamqp.NewProducer(
		env.client.NewProducer(rabbitmq.WithConfirm()), env.exchange, env.routingKey)
	env.repo = task.NewPgRepository()
	env.registry = task.NewTypeRegistry()
	return env
}

// waitForTaskStatus 轮询直至目标状态（deadline 内未达则 Fatal 并附当前态取证）。
func waitForTaskStatus(t *testing.T, ctx context.Context, repo *task.PgRepository,
	id, want string, timeout time.Duration) *task.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got, err := repo.Find(ctx, id)
		if err != nil {
			t.Fatalf("查询任务失败: %v", err)
		}
		if got != nil && got.Status == want {
			return got
		}
		if time.Now().After(deadline) {
			if got == nil {
				t.Fatalf("等待状态 %s 超时（%v）：任务不存在", want, timeout)
			}
			t.Fatalf("等待状态 %s 超时（%v），当前 %s（attempts=%d last_error=%q）",
				want, timeout, got.Status, got.Attempts, got.LastError)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestTaskAmqp_FullLinkPipeline 全链路成功路径：
// Submit（已注册 type，ViaQueue）→ AMQP 投递 → 即时消费执行 → succeeded。
func TestTaskAmqp_FullLinkPipeline(t *testing.T) {
	env := setupTaskAmqp(t)
	const typ = "itest_full"
	env.registry.Register(typ)

	var handled atomic.Int32
	handler := func(_ context.Context, tk *task.Task) ([]byte, error) {
		handled.Add(1)
		var p struct {
			V string `json:"v"`
		}
		if err := json.Unmarshal(tk.Payload, &p); err != nil {
			return nil, fmt.Errorf("载荷解析失败: %w", err)
		}
		return []byte(fmt.Sprintf(`{"echo":%q}`, p.V)), nil
	}
	worker := &task.Worker{Name: "itest-task-full", Repo: env.repo, Handler: handler}
	consumer := taskamqp.NewConsumer(env.client, env.queue, worker,
		rabbitmq.WithPrefetch(1), rabbitmq.WithConsumerName("itest-task-full"))
	go func() { _ = consumer.Run(env.ctx) }()

	dispatcher := task.NewDispatcher(env.repo, env.producer)
	dispatcher.QueueTypes = env.registry

	submitted, err := dispatcher.Submit(env.ctx, task.CreationOptions{
		TaskNo:   "itest-full-" + uuid.NewString()[:8],
		Type:     typ,
		Payload:  []byte(`{"v":"hello"}`),
		ViaQueue: true,
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	final := waitForTaskStatus(t, env.ctx, env.repo, submitted.ID, task.StatusSucceeded, 10*time.Second)
	if final.Attempts != 1 {
		t.Fatalf("应恰执行 1 次，实际 %d 次", final.Attempts)
	}
	// PG jsonb 列读回为规范化文本（键值间含空格），须语义比较而非字节比较
	if !jsonEqual(t, final.Result, `{"echo":"hello"}`) {
		t.Fatalf("result 不符: %s", final.Result)
	}
	if n := handled.Load(); n != 1 {
		t.Fatalf("handler 应恰调用 1 次，实际 %d 次", n)
	}
}

// TestTaskAmqp_FailRetryRepublish 失败 → RetryScanner 重投 → 成功：
//   - 首次消费失败：消息 Ack 丢弃不 requeue（重试独占走 RetryScanner），
//     task 落 failed、last_error 留痕；
//   - Scan：failed → 退避转回 pending（available_at 推进到未来）并经
//     amqp.Producer 重投；本测试无 pull worker，succeeded 即重投真实被
//     即时消费的取证；
//   - 二次消费成功：attempts=2、handler 共调用 2 次、result 正确。
func TestTaskAmqp_FailRetryRepublish(t *testing.T) {
	env := setupTaskAmqp(t)
	const typ = "itest_retry"
	env.registry.Register(typ)

	var handled atomic.Int32
	handler := func(_ context.Context, _ *task.Task) ([]byte, error) {
		if handled.Add(1) == 1 {
			return nil, fmt.Errorf("模拟执行失败")
		}
		return []byte(`{"ok":true}`), nil
	}
	worker := &task.Worker{Name: "itest-task-retry", Repo: env.repo, Handler: handler}
	consumer := taskamqp.NewConsumer(env.client, env.queue, worker,
		rabbitmq.WithPrefetch(1), rabbitmq.WithConsumerName("itest-task-retry"))
	go func() { _ = consumer.Run(env.ctx) }()

	dispatcher := task.NewDispatcher(env.repo, env.producer)
	dispatcher.QueueTypes = env.registry
	submitted, err := dispatcher.Submit(env.ctx, task.CreationOptions{
		TaskNo:      "itest-retry-" + uuid.NewString()[:8],
		Type:        typ,
		Payload:     []byte(`{"v":"retry"}`),
		ViaQueue:    true,
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	// ① 首次消费失败：task 落 failed
	failed := waitForTaskStatus(t, env.ctx, env.repo, submitted.ID, task.StatusFailed, 10*time.Second)
	if failed.Attempts != 1 {
		t.Fatalf("失败时应已执行 1 次，实际 %d 次", failed.Attempts)
	}
	if !strings.Contains(failed.LastError, "模拟执行失败") {
		t.Fatalf("last_error 应留痕: %q", failed.LastError)
	}
	// 骨架契约取证：失败消息一律 Ack 不 requeue——短暂窗口后 handler 仍只被调 1 次
	time.Sleep(500 * time.Millisecond)
	if n := handled.Load(); n != 1 {
		t.Fatalf("失败消息应 Ack 丢弃不 requeue（重试独占走 RetryScanner），handler 已被调 %d 次", n)
	}

	// ② RetryScanner：退避转 pending + 注册 type 经 Producer 重投
	scanner := task.NewRetryScanner(env.repo)
	scanner.QueueTypes = env.registry
	scanner.Producer = env.producer
	before := time.Now()
	_, retried, _, err := scanner.Scan(env.ctx)
	if err != nil {
		t.Fatalf("Scan 失败: %v", err)
	}
	if retried < 1 {
		t.Fatalf("应至少重试 1 个任务，实际 %d", retried)
	}
	rescheduled, err := env.repo.Find(env.ctx, submitted.ID)
	if err != nil || rescheduled == nil {
		t.Fatalf("查询重排任务失败: %v", err)
	}
	// 退避取证：available_at 已被推到 Scan 时刻之后（首档 5s）
	if !rescheduled.AvailableAt.After(before) {
		t.Fatalf("重试退避应推进 available_at 到未来，实际 %v", rescheduled.AvailableAt)
	}

	// RetryScanner runs repeatedly; ByID must respect available_at.
	if handled.Load() != 1 {
		t.Fatal("retry executed before available_at")
	}
	var final *task.Task
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, err := scanner.Scan(env.ctx); err != nil {
			t.Fatal(err)
		}
		final, err = env.repo.Find(env.ctx, submitted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final != nil && final.Status == task.StatusSucceeded {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final == nil || final.Status != task.StatusSucceeded {
		t.Fatalf("retry never executed after available_at: %+v", final)
	}
	if final.Attempts != 2 {
		t.Fatalf("应共执行 2 次（1 失败 1 成功），实际 %d 次", final.Attempts)
	}
	if n := handled.Load(); n != 2 {
		t.Fatalf("handler 应共调用 2 次，实际 %d 次", n)
	}
	// PG jsonb 列读回为规范化文本（键值间含空格），须语义比较而非字节比较
	if !jsonEqual(t, final.Result, `{"ok":true}`) {
		t.Fatalf("result 不符: %s", final.Result)
	}
}

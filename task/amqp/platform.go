package amqp

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
)

// 统一任务平台（v0.13）的 RabbitMQ 传输层。与本包旧 NewHandler/NewConsumer
// 的 AlwaysAck 桥接相互独立：旧桥接语义不变，新平台使用 task.DispatchMessage
// 协议、publisher confirms + mandatory 与手动 ACK（ACK 时机由 task.Runtime 决定）。

// ExchangeName 平台执行通知 exchange（direct，durable），路由键为执行类别。
func ExchangeName(prefix string) string { return prefix + ".task" }

// QueueName 执行类别对应的 durable 队列。
func QueueName(prefix, class string) string { return prefix + ".task." + class }

// DeadLetterExchange 毒消息死信 exchange（fanout，durable）。
func DeadLetterExchange(prefix string) string { return prefix + ".task.dlx" }

// DeadLetterQueue 毒消息隔离队列，需人工处置并保留审计。
func DeadLetterQueue(prefix string) string { return prefix + ".task.dead" }

// PlatformTopology 返回平台拓扑：每个执行类别一条 durable 队列（死信指向 DLX），
// quorum 为 true 时声明 x-queue-type=quorum（副本数与故障域由基础设施确认）。
// prefix 按项目/环境区分（如 "example.prod"），与独立 vhost/权限配合使用。
func PlatformTopology(prefix string, classes []string, quorum bool) rabbitmq.Topology {
	topo := rabbitmq.Topology{
		Exchanges: []rabbitmq.ExchangeSpec{
			{Name: ExchangeName(prefix), Kind: "direct", Durable: true},
			{Name: DeadLetterExchange(prefix), Kind: "fanout", Durable: true},
		},
		Queues: []rabbitmq.QueueSpec{{Name: DeadLetterQueue(prefix), Durable: true}},
		Binds:  []rabbitmq.BindSpec{{Queue: DeadLetterQueue(prefix), Exchange: DeadLetterExchange(prefix)}},
	}
	for _, class := range classes {
		args := amqp.Table{"x-dead-letter-exchange": DeadLetterExchange(prefix)}
		if quorum {
			args["x-queue-type"] = "quorum"
		}
		topo.Queues = append(topo.Queues, rabbitmq.QueueSpec{Name: QueueName(prefix, class), Durable: true, Args: args})
		topo.Binds = append(topo.Binds, rabbitmq.BindSpec{Queue: QueueName(prefix, class), Key: class, Exchange: ExchangeName(prefix)})
	}
	return topo
}

// Transport 实现 task.Transport。
type Transport struct {
	client   *rabbitmq.Client
	producer *rabbitmq.Producer
	prefix   string
}

var _ task.Transport = (*Transport)(nil)

// NewTransport 创建平台传输层（独占一个 confirm 生产者）。client 须已 Connect，
// 拓扑由调用方用 PlatformTopology 声明。
func NewTransport(client *rabbitmq.Client, prefix string) *Transport {
	return &Transport{client: client, producer: client.NewProducer(rabbitmq.WithConfirm()), prefix: prefix}
}

// Publish 持久化 + confirm + mandatory 发布；无队列可路由返回包装 task.ErrUnroutable。
func (t *Transport) Publish(ctx context.Context, class string, msg task.DispatchMessage) error {
	err := t.producer.Publish(ctx, ExchangeName(t.prefix), class, msg.Encode(),
		rabbitmq.WithContentType(contentTypeJSON), rabbitmq.WithMessageID(msg.MessageID()), rabbitmq.WithMandatory())
	if errors.Is(err, rabbitmq.ErrUnroutable) {
		return fmt.Errorf("%w: class=%s", task.ErrUnroutable, class)
	}
	return err
}

// Consume 以 ManualAck 订阅类别队列，阻塞到 ctx 取消后 Stop 订阅（未确认消息重新入队），
// 并等待正在执行的 fn 返回。
func (t *Transport) Consume(ctx context.Context, class string, prefetch int, fn func(context.Context, task.Delivery)) error {
	// fn 收到外层 ctx：停机时等待执行名额的 fn 可感知取消并 Nack(requeue) 归还消息。
	cons := t.client.NewConsumer(QueueName(t.prefix, class), func(_ context.Context, d rabbitmq.Delivery) error {
		fn(ctx, d)
		return nil
	}, rabbitmq.WithNackPolicy(rabbitmq.ManualAck), rabbitmq.WithPrefetch(prefetch), rabbitmq.WithConsumerName("task-platform:"+QueueName(t.prefix, class)))
	done := make(chan error, 1)
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	go func() { done <- cons.Run(runCtx) }()
	<-ctx.Done()
	cons.Stop()
	err := <-done
	return err
}

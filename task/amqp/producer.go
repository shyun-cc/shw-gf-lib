package amqp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
)

// Publisher rabbitmq.Producer 的窄接口视图（仅 Publish 方法）：*rabbitmq.Producer
// 天然满足；收窄为接口使库与项目的单测均可注入 fake，不依赖真 broker。
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte, opts ...rabbitmq.PublishOption) error
}

// 编译期断言：*rabbitmq.Producer 可注入 Publisher；AmqpProducer 实现 task.QueueProducer
var (
	_ Publisher          = (*rabbitmq.Producer)(nil)
	_ task.QueueProducer = (*AmqpProducer)(nil)
)

// AmqpProducer task.QueueProducer 的 RabbitMQ 实现：把 task_id 包装为包 doc
// 契约的 JSON 载荷，发布到构造注入的 exchange/routingKey（库不做项目决策，
// 拓扑参数与消费端绑定保持一致，见 rabbitmq.TaskTopology）。
type AmqpProducer struct {
	publisher  Publisher
	exchange   string
	routingKey string
}

// NewProducer 创建任务消息生产者。p 为 client.NewProducer(...) 的产物
// （confirm 与否由项目按可靠性需求决定）；exchange/routingKey 须与拓扑声明
// 及消费队列的绑定一致。
func NewProducer(p Publisher, exchange, routingKey string) *AmqpProducer {
	return &AmqpProducer{publisher: p, exchange: exchange, routingKey: routingKey}
}

// Publish 实现 task.QueueProducer：序列化契约载荷并发布。
//
// ContentType 固定 application/json（契约，见包 doc）；消息 persistent 由
// rabbitmq.Producer 默认保证（本方法不传 WithTransient）。发布失败如实返回
// error（连接断开期间立即失败，绝不静默缓存重发），由调用方
// （Dispatcher/RetryScanner）按各自语义处置。
func (p *AmqpProducer) Publish(ctx context.Context, taskID string) error {
	body, err := json.Marshal(message{TaskID: taskID})
	if err != nil {
		return fmt.Errorf("task/amqp: 序列化消息载荷失败 (task_id=%s): %w", taskID, err)
	}
	if err := p.publisher.Publish(ctx, p.exchange, p.routingKey, body,
		rabbitmq.WithContentType(contentTypeJSON)); err != nil {
		return fmt.Errorf("task/amqp: 发布任务消息失败 (task_id=%s): %w", taskID, err)
	}
	return nil
}

package task

import "context"

// QueueProducer 队列生产者接口（队列选型是项目决策：AMQP/Kafka 由项目实现）。
// 消息只带 task_id——表是唯一真相源，队列丢消息不丢任务（RetryScanner 兜底拉起）。
type QueueProducer interface {
	// Publish 投递任务消息（task_id，uuid 文本）
	Publish(ctx context.Context, taskID string) error
}

// NullProducer 空实现（pull 模式默认；集成测试用计数 fake 替换）
type NullProducer struct{}

// Publish 空操作
func (n *NullProducer) Publish(_ context.Context, _ string) error { return nil }

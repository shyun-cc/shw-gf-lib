package task

import "context"

// Delivery 是传输层消息的最小确认接口（rabbitmq.Delivery 天然满足）。
type Delivery interface {
	Body() []byte
	Ack() error
	Nack(requeue bool) error
}

// Transport 是统一 Runtime 依赖的执行通知传输层。task 包不依赖具体 MQ，
// RabbitMQ 实现见 task/amqp.NewTransport。
type Transport interface {
	Publisher
	// Consume 以手动确认模式订阅 class 队列，阻塞到 ctx 取消；fn 串行调用，
	// 由 fn 决定 Ack/Nack。返回前必须停止订阅，使未确认消息由 broker 重新入队。
	Consume(ctx context.Context, class string, prefetch int, fn func(context.Context, Delivery)) error
}

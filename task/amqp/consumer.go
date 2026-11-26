package amqp

import (
	"context"
	"encoding/json"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
)

// NewHandler 构造任务消费骨架的 rabbitmq.HandlerFunc（固定语义，见包 doc）：
//
// 收消息 → 解析 JSON 提取 task_id → 非法载荷（解析失败 / 缺 task_id /
// 非 uuid 形态）记日志后按成功返回（Ack 丢弃，循环不中断）→ task.Worker.
// ExecuteByID 执行 → 无论执行成败均按成功返回。
//
// handler 恒返回 nil 是骨架的核心契约：rabbitmq.Consumer 对 err==nil 一律
// Ack（配合 AlwaysAck 双保险），从根上杜绝 Nack/Requeue——失败补偿与重试
// 由 task.RetryScanner 独占（表驱动），requeue 会与重试进程竞争并可能
// 造成消息风暴。项目自定义装配（自建 rabbitmq.Consumer）也应遵守此语义。
//
// w 须已完成装配（Repo/Handler 公开字段注入，见 task.Worker）。
func NewHandler(w *task.Worker) rabbitmq.HandlerFunc {
	return func(ctx context.Context, d rabbitmq.Delivery) error {
		handleDelivery(ctx, w, d)
		return nil
	}
}

// handleDelivery 单条消息处理：毒消息 Ack 丢弃，执行结果只记日志不回传错误
func handleDelivery(ctx context.Context, w *task.Worker, d rabbitmq.Delivery) {
	var msg message
	if err := json.Unmarshal(d.Body(), &msg); err != nil {
		g.Log().Warningf(ctx, "task/amqp: 消息载荷解析失败（Ack 丢弃）: %v, body=%s", err, d.Body())
		return
	}
	if _, err := uuid.Parse(msg.TaskID); err != nil {
		g.Log().Warningf(ctx, "task/amqp: task_id 非 uuid 形态（Ack 丢弃）: %q", msg.TaskID)
		return
	}
	if err := w.ExecuteByID(ctx, msg.TaskID); err != nil {
		// 执行失败不 Nack：已 Ack 的消息由任务表状态接管，重试独占走 RetryScanner
		g.Log().Errorf(ctx, "task/amqp: 任务执行失败（消息已 Ack，重试由 RetryScanner 独占）: task_id=%s: %v", msg.TaskID, err)
	}
}

// NewConsumer 便捷装配：把消费骨架包装为可注册进 worker.WorkerManager 的
// *rabbitmq.Consumer（Name/Run），固定注入 AlwaysAck 策略与骨架 handler。
//
// client 须为已完成 Connect 的客户端；queue 为任务队列名（与
// rabbitmq.TaskTopology 声明一致）；opts 透传 rabbitmq.ConsumerOption
// （如 WithPrefetch/WithConsumerName）。骨架保证始终 ACK（handler 恒成功
// 返回），调用方即使误传其他 NackPolicy 也不会引入 Nack/Requeue 路径。
func NewConsumer(client *rabbitmq.Client, queue string, w *task.Worker, opts ...rabbitmq.ConsumerOption) *rabbitmq.Consumer {
	return client.NewConsumer(queue, NewHandler(w),
		append([]rabbitmq.ConsumerOption{rabbitmq.WithNackPolicy(rabbitmq.AlwaysAck)}, opts...)...)
}

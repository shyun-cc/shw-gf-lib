// Package amqp task 引擎的 RabbitMQ 即时消费桥接（官方装配层：task ↔ rabbitmq）。
//
// 依赖方向：task/amqp → task + rabbitmq（单向，桥接包；两个被桥接包互不感知）。
//
// 消息格式契约（对外公示，生产与消费双方共同遵守）：
//
//		{"task_id": "018f3c2e-..."}
//
//	  - task_id 为任务表主键（uuidv7 文本）；
//	  - ContentType application/json；消息 persistent（rabbitmq.Producer 默认
//	    delivery_mode=2）；发布到构造注入的 exchange/routingKey；
//	  - task 表是唯一真相源，消息只是即时唤醒信号——丢消息只退化为 pull/重试
//	    延迟，不丢任务，队列不为任务可靠性负责。
//
// 消费语义（对齐 PHP Foundation/Task/Consumer/Amqp 的「始终 ACK」约定）：
//   - 无论任务执行成败，消息一律 ACK、永不 Nack/Requeue：失败补偿与重试由
//     task.RetryScanner 独占（表驱动），requeue 会与重试进程竞争并可能造成
//     消息风暴；
//   - 无法解析的消息（非法 JSON / 缺 task_id / 非 uuid 形态）记日志后 ACK
//     丢弃，消费循环不中断；
//   - 重复投递幂等：task.Worker.ExecuteByID 对非 pending 任务直接忽略。
package amqp

// contentTypeJSON 消息 ContentType 契约值（见包 doc）
const contentTypeJSON = "application/json"

// message 消息载荷（契约见包 doc：单字段 task_id）
type message struct {
	TaskID string `json:"task_id"`
}

package rabbitmq

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ExchangeSpec exchange 声明参数（值类型：durable 等由调用方决定，库不预设）
type ExchangeSpec struct {
	Name       string     // exchange 名
	Kind       string     // 类型：fanout / direct / topic / headers
	Durable    bool       // 是否持久化（broker 重启后存活）
	AutoDelete bool       // 最后一个解绑后是否自动删除
	Internal   bool       // 是否内部 exchange（客户端不可直接发布）
	Args       amqp.Table // 扩展参数（如 alternate-exchange）
}

// QueueSpec queue 声明参数（值类型）
type QueueSpec struct {
	Name       string     // queue 名
	Durable    bool       // 是否持久化
	AutoDelete bool       // 最后一个消费者取消后是否自动删除
	Exclusive  bool       // 是否连接级排他（连接断开即删除）
	Args       amqp.Table // 扩展参数（如 x-message-ttl / x-dead-letter-exchange）
}

// BindSpec queue → exchange 绑定参数（值类型）
type BindSpec struct {
	Queue    string     // queue 名
	Key      string     // routing key
	Exchange string     // exchange 名
	Args     amqp.Table // 扩展参数（headers 绑定的匹配表）
}

// Topology 拓扑声明集合（值类型）：经 Client.Declare 逐项直接声明并登记注册表，
// 重连后由 guardian 原样重放。
type Topology struct {
	Exchanges []ExchangeSpec
	Queues    []QueueSpec
	Binds     []BindSpec
}

// declareOn 在给定 channel 上按 exchanges → queues → binds 顺序逐项声明。
// AMQP 声明幂等：同名同参重复无害；同名异参 broker 返回 406 precondition failed，
// 错误如实透传（配置漂移早暴露，不静默吞并）。
func (t Topology) declareOn(ch amqpChannel) error {
	for _, e := range t.Exchanges {
		if err := ch.ExchangeDeclare(e.Name, e.Kind, e.Durable, e.AutoDelete, e.Internal, false, e.Args); err != nil {
			return fmt.Errorf("声明 exchange %s (kind=%s) 失败: %w", e.Name, e.Kind, err)
		}
	}
	for _, q := range t.Queues {
		if _, err := ch.QueueDeclare(q.Name, q.Durable, q.AutoDelete, q.Exclusive, false, q.Args); err != nil {
			return fmt.Errorf("声明 queue %s 失败: %w", q.Name, err)
		}
	}
	for _, b := range t.Binds {
		if err := ch.QueueBind(b.Queue, b.Key, b.Exchange, false, b.Args); err != nil {
			return fmt.Errorf("绑定 queue %s → exchange %s (key=%s) 失败: %w", b.Queue, b.Exchange, b.Key, err)
		}
	}
	return nil
}

// TaskTopology task 场景便捷拓扑：durable exchange + durable queue + 绑定。
// kind 为 exchange 类型（fanout / direct），routingKey 为绑定键（fanout 时
// 传空串即可）。durable 组合与任务表「表是真相源、消息只是唤醒信号」的
// 可靠性要求对齐（详见 task/amqp 包文档）。
func TaskTopology(exchange, kind, queue, routingKey string) Topology {
	return Topology{
		Exchanges: []ExchangeSpec{{Name: exchange, Kind: kind, Durable: true}},
		Queues:    []QueueSpec{{Name: queue, Durable: true}},
		Binds:     []BindSpec{{Queue: queue, Key: routingKey, Exchange: exchange}},
	}
}

package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Producer 消息生产者：独占一条 channel（confirm 是 channel 级开关，
// 独占避免与其他用途互相干扰）。channel 由 Client 在连接建立/重连时分配
// （attach），断开时释放（detach）——断开期间 Publish 立即返回 ErrNotConnected，
// 绝不静默缓存重发。
type Producer struct {
	confirm bool // 是否开启 publisher confirm（构造参数注入，默认关）

	mu       sync.Mutex
	ch       amqpChannel
	confirms chan amqp.Confirmation // confirm 模式下的 broker 确认流（随 channel 重建）
	returns  chan amqp.Return       // confirm 模式下的 mandatory 退回流（随 channel 重建）
}

// ErrUnroutable mandatory 发布被 broker 以 basic.return 退回（无匹配队列）。
// 消息未进入任何队列，调用方不得把它当作已投递。
var ErrUnroutable = errors.New("rabbitmq: 消息不可路由（mandatory 退回）")

// ProducerOption Producer 构造选项
type ProducerOption func(*Producer)

// WithConfirm 开启 publisher confirm：发布后等待 broker 确认
// （Channel.Confirm + NotifyPublish），broker nack 或等待期间连接断开
// 均如实返回 error。confirm 模式下 Publish 全程串行（confirm 按
// delivery tag 顺序回流，串行化避免确认归属错乱）。
func WithConfirm() ProducerOption {
	return func(p *Producer) { p.confirm = true }
}

// NewProducer 创建生产者并注册进 Client（channel 分配由 Client 托管，
// 含断线重建与 channel 级自愈）。已连接时立即分配 channel；
// 未连接时等待连接建立后自动分配。
func (c *Client) NewProducer(opts ...ProducerOption) *Producer {
	p := &Producer{}
	for _, o := range opts {
		o(p)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		c.attachProducerLocked(p, c.conn)
	}
	c.producers = append(c.producers, p)
	return p
}

// attach 绑定新分配的 channel（guardian/Client 调用）；confirm 模式下
// 在新 channel 上重新开启 Confirm 开关并重建确认流。
func (p *Producer) attach(ch amqpChannel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ch = ch
	if p.confirm {
		if err := ch.Confirm(false); err == nil {
			// returns 必须带缓冲：amqp091 按帧顺序先投递 basic.return 再投递 ack，
			// 无缓冲会阻塞连接读循环。
			p.returns = ch.NotifyReturn(make(chan amqp.Return, 16))
			p.confirms = ch.NotifyPublish(make(chan amqp.Confirmation, 1))
		}
	}
}

// detach 释放 channel（连接断开时由 guardian 调用）：此后 Publish 立即报错
func (p *Producer) detach() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ch = nil
	p.confirms = nil
	p.returns = nil
}

// PublishOption Publish 可选项
type PublishOption func(*publishOptions)

type publishOptions struct {
	contentType string
	persistent  bool
	messageID   string
	headers     amqp.Table
	mandatory   bool
}

// WithContentType 设置 MIME 内容类型（默认 application/octet-stream）
func WithContentType(ct string) PublishOption {
	return func(o *publishOptions) { o.contentType = ct }
}

// WithTransient 标记消息非持久化（默认持久化 delivery_mode=2）
func WithTransient() PublishOption {
	return func(o *publishOptions) { o.persistent = false }
}

// WithMessageId 设置消息标识
func WithMessageID(id string) PublishOption {
	return func(o *publishOptions) { o.messageID = id }
}

// WithMandatory 以 mandatory 标志发布：无队列可路由时 Publish 返回 ErrUnroutable。
// 仅在 WithConfirm 生产者上可用（判定依赖 confirm 前到达的 basic.return），
// 且必须同时用 WithMessageID 设置唯一消息标识。
func WithMandatory() PublishOption {
	return func(o *publishOptions) { o.mandatory = true }
}

// WithHeaders 设置消息头（AMQP headers）
func WithHeaders(h amqp.Table) PublishOption {
	return func(o *publishOptions) { o.headers = h }
}

// Publish 发布消息到 exchange（routingKey 路由）。
//
// 语义契约：
//   - 默认 persistent（delivery_mode=2）；连接断开期间立即返回 ErrNotConnected，
//     绝不静默缓存重发——调用方自行决定失败处理（重试/落库/告警）。
//   - confirm 模式（WithConfirm 构造）下等待 broker 确认：nack、channel
//     中途关闭、ctx 取消均如实返回 error。
func (p *Producer) Publish(ctx context.Context, exchange, routingKey string, body []byte, opts ...PublishOption) error {
	o := publishOptions{contentType: producerDefaultContentTyp, persistent: true}
	for _, fn := range opts {
		fn(&o)
	}
	deliveryMode := amqp.Transient
	if o.persistent {
		deliveryMode = amqp.Persistent
	}
	pub := amqp.Publishing{
		Body:         body,
		ContentType:  o.contentType,
		DeliveryMode: deliveryMode,
		MessageId:    o.messageID,
		Headers:      o.headers,
		Timestamp:    time.Now(),
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch == nil {
		return ErrNotConnected
	}
	if o.mandatory {
		if !p.confirm || o.messageID == "" {
			return errors.New("rabbitmq: mandatory 发布需要 confirm 模式与消息标识")
		}
		if p.returns == nil {
			return errors.New("rabbitmq: confirm 未开启，投递结果未知")
		}
		p.drainReturns("")
	}
	if err := p.ch.PublishWithContext(ctx, exchange, routingKey, o.mandatory, false, pub); err != nil {
		return fmt.Errorf("rabbitmq: 发布到 %s (key=%s) 失败: %w", exchange, routingKey, err)
	}
	if p.confirms != nil {
		select {
		case confirm, ok := <-p.confirms:
			if !ok {
				return errors.New("rabbitmq: 等待 publisher confirm 时 channel 已关闭，投递结果未知")
			}
			if !confirm.Ack {
				return fmt.Errorf("rabbitmq: broker nack 了消息 (delivery_tag=%d)", confirm.DeliveryTag)
			}
			if o.mandatory && p.drainReturns(o.messageID) {
				return ErrUnroutable
			}
		case <-ctx.Done():
			return fmt.Errorf("rabbitmq: 等待 publisher confirm 被取消: %w", ctx.Err())
		}
	}
	return nil
}

// drainReturns 非阻塞清空退回流，返回其中是否有 messageID 对应的退回。
// 调用方持有 p.mu；confirm 模式下 Publish 串行，ack 之前到达的退回必属本条或更早消息。
func (p *Producer) drainReturns(messageID string) bool {
	hit := false
	for {
		select {
		case r, ok := <-p.returns:
			if !ok {
				return hit
			}
			if messageID != "" && r.MessageId == messageID {
				hit = true
			}
		default:
			return hit
		}
	}
}

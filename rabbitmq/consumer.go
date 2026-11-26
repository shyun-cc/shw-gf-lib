package rabbitmq

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/shyun-cc/shw-gf-lib/worker"
)

// 编译期断言：Consumer 实现 worker.Worker，可注册进 worker.WorkerManager 托管
var _ worker.Worker = (*Consumer)(nil)

// Delivery 消息薄适配接口：隔离 amqp091-go 的 Delivery 结构体，
// handler 面向本接口编程（单测可注入 fake 消息，不依赖真 broker）。
type Delivery interface {
	// Body 消息体原文
	Body() []byte
	// Ack 确认消息（非批量）
	Ack() error
	// Nack 否认消息，requeue 决定是否重新入队
	Nack(requeue bool) error
}

// amqpDelivery amqp.Delivery → Delivery 适配（autoAck=false 场景下的手动确认）
type amqpDelivery struct{ d amqp.Delivery }

func (a amqpDelivery) Body() []byte            { return a.d.Body }
func (a amqpDelivery) Ack() error              { return a.d.Ack(false) }
func (a amqpDelivery) Nack(requeue bool) error { return a.d.Nack(false, requeue) }

// NackPolicy 消费失败时的处置策略（组件不预设策略，由调用方注入）。
type NackPolicy int

const (
	// AlwaysAck handler 无论成败均 Ack（task 场景语义：表是真相源，
	// 消息只是唤醒信号，失败走任务表重试而非消息重投）
	AlwaysAck NackPolicy = iota
	// NackRequeue 失败 Nack + requeue（通用重试场景）
	NackRequeue
	// NackDrop 失败 Nack 不 requeue（毒消息直丢，或由死信 exchange 接管）
	NackDrop
	// ManualAck handler 自行调用 Delivery.Ack/Nack，消费者不做任何确认处置
	// （handler 返回 error 或 panic 时也不确认；未确认消息在 channel 关闭后由 broker 重投）。
	ManualAck
)

// HandlerFunc 消息处理函数：返回非 nil error 视为处理失败，交由 NackPolicy 处置。
type HandlerFunc func(ctx context.Context, d Delivery) error

// Consumer 消息消费者：独占一条 channel（QoS 是 channel 级开关），
// 消费循环串行一次一条（handler 完成 → 按策略 Ack/Nack → 取下一条）；
// 并发消费 = 项目侧创建多个 Consumer 实例（RabbitMQ 共享队列原生负载均衡）。
//
// 实现 worker.Worker：Name/Run 可注册进 worker.WorkerManager，
// 由管理器统一 ctx 生命周期（ctx 取消即优雅停机）。
type Consumer struct {
	client   *Client
	name     string
	queue    string
	handler  HandlerFunc
	policy   NackPolicy
	prefetch int

	mu         sync.Mutex
	ch         amqpChannel
	deliveries <-chan amqp.Delivery
	stopped    bool

	handlerWG sync.WaitGroup // 在途 handler 完成追踪（ctx 取消时等待）
}

// ConsumerOption Consumer 构造选项
type ConsumerOption func(*Consumer)

// WithNackPolicy 设置失败处置策略（默认 NackRequeue）
func WithNackPolicy(p NackPolicy) ConsumerOption {
	return func(c *Consumer) { c.policy = p }
}

// WithPrefetch 设置 QoS prefetch（默认 10；0 表示不限）。
// 串行消费下 prefetch 决定 broker 预推的未确认消息量。
func WithPrefetch(n int) ConsumerOption {
	return func(c *Consumer) { c.prefetch = n }
}

// WithConsumerName 设置消费者名（worker 名与 AMQP consumer tag；
// 默认 "rabbitmq-consumer:<queue>"）
func WithConsumerName(name string) ConsumerOption {
	return func(c *Consumer) { c.name = name }
}

// NewConsumer 创建消费者并注册进 Client 订阅注册表（重连后自动重订阅，
// 含 channel 级自愈）。已连接时立即订阅；未连接时等待连接建立后由 Client 自动订阅。
func (c *Client) NewConsumer(queue string, handler HandlerFunc, opts ...ConsumerOption) *Consumer {
	cons := &Consumer{
		client:   c,
		name:     fmt.Sprintf("rabbitmq-consumer:%s", queue),
		queue:    queue,
		handler:  handler,
		policy:   NackRequeue,
		prefetch: consumerDefaultPrefetch,
	}
	for _, o := range opts {
		o(cons)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		c.subscribeConsumerLocked(cons, c.conn)
	}
	c.consumers = append(c.consumers, cons)
	return cons
}

// Name 实现 worker.Worker：返回消费者名
func (c *Consumer) Name() string { return c.name }

// subscribe 在给定连接上（重）订阅：独占 channel → QoS → Consume（autoAck=false）。
// 由 Client 在持锁（client.mu）上下文中调用。
func (c *Consumer) subscribe(conn amqpConn) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if err := ch.Qos(c.prefetch, 0, false); err != nil {
		ch.Close()
		return err
	}
	dch, err := ch.Consume(c.queue, c.name, false, false, false, false, nil)
	if err != nil {
		ch.Close()
		return err
	}
	c.mu.Lock()
	c.ch = ch
	c.deliveries = dch
	c.mu.Unlock()
	return nil
}

// detach 释放 channel 与订阅（连接断开时由 guardian 调用）：
// Run 检测到消息流关闭后自动等待重连恢复，循环不断。
func (c *Consumer) detach() {
	c.mu.Lock()
	c.ch = nil
	c.deliveries = nil
	c.mu.Unlock()
}

// Stop 取消订阅：从 Client 注册表移除（重连不再重订阅）并关闭独占 channel，
// broker 将该 channel 上未确认的消息重新入队。Run 随后在在途 handler 完成后返回。
// 用于有界停机的第一步“停止新拉取”；幂等。
func (c *Consumer) Stop() {
	cl := c.client
	cl.mu.Lock()
	for i, x := range cl.consumers {
		if x == c {
			cl.consumers = append(cl.consumers[:i], cl.consumers[i+1:]...)
			break
		}
	}
	c.mu.Lock()
	c.stopped = true
	ch := c.ch
	c.ch = nil
	c.deliveries = nil
	c.mu.Unlock()
	cl.mu.Unlock()
	if ch != nil {
		_ = ch.Close()
	}
}

func (c *Consumer) isStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// currentDeliveries 获取当前消息流（nil 表示未连接/未订阅）
func (c *Consumer) currentDeliveries() <-chan amqp.Delivery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deliveries
}

// Run 实现 worker.Worker：消费主循环。
//
// 停机语义：ctx 取消时停止取新消息、等待在途 handler 完成后返回 nil；
// 连接断开时循环不退出（等 guardian 重连恢复订阅后继续消费）。
func (c *Consumer) Run(ctx context.Context) error {
	for {
		if c.isStopped() {
			c.handlerWG.Wait()
			return nil
		}
		dch := c.currentDeliveries()
		if dch == nil {
			// 未连接/未订阅：等待连接就绪信号（200ms 轮询兜底，
			// 防 ready 信号已消费过导致的空转）
			select {
			case <-ctx.Done():
				c.handlerWG.Wait()
				return nil
			case <-c.client.ready():
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		select {
		case <-ctx.Done():
			c.handlerWG.Wait()
			return nil
		case d, ok := <-dch:
			if !ok {
				// 消息流关闭（channel/连接断开）：清空本地引用，等待重连重订阅
				c.detach()
				continue
			}
			c.handleOne(ctx, d)
		}
	}
}

// handleOne 串行处理一条消息：safeHandle（panic 隔离）→ 按 NackPolicy 处置
func (c *Consumer) handleOne(ctx context.Context, d amqp.Delivery) {
	c.handlerWG.Add(1)
	defer c.handlerWG.Done()
	dl := amqpDelivery{d}
	err := c.safeHandle(ctx, dl)
	c.applyPolicy(ctx, dl, err)
}

// safeHandle 执行 handler 并隔离 panic：panic recover 记日志后转为 error，
// 按策略处置该消息——消费循环绝不断掉（毒消息不拖垮 worker）。
func (c *Consumer) safeHandle(ctx context.Context, dl Delivery) (err error) {
	defer func() {
		if r := recover(); r != nil {
			g.Log().Errorf(ctx, "rabbitmq consumer %s handler panic（已隔离，消息按策略处置）: %v", c.name, r)
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return c.handler(ctx, dl)
}

// applyPolicy 按 NackPolicy 处置消息：
//   - AlwaysAck：无论 handler 成败均 Ack
//   - NackRequeue：失败 Nack + requeue，成功 Ack
//   - NackDrop：失败 Nack 不 requeue，成功 Ack
//
// Ack/Nack 自身失败（连接已断）仅记日志——消息会因未确认而在重连后
// 由 broker 重新投递（at-least-once）。
func (c *Consumer) applyPolicy(ctx context.Context, dl Delivery, err error) {
	if c.policy == ManualAck {
		if err != nil {
			g.Log().Warningf(ctx, "rabbitmq consumer %s ManualAck handler 失败（确认由 handler 负责）: %v", c.name, err)
		}
		return
	}
	if c.policy == AlwaysAck || err == nil {
		if ackErr := dl.Ack(); ackErr != nil {
			g.Log().Warningf(ctx, "rabbitmq consumer %s Ack 失败（broker 将重投）: %v", c.name, ackErr)
		}
		return
	}
	var requeue bool
	switch c.policy {
	case NackRequeue:
		requeue = true
	case NackDrop:
		requeue = false
	}
	if nackErr := dl.Nack(requeue); nackErr != nil {
		g.Log().Warningf(ctx, "rabbitmq consumer %s Nack(requeue=%v) 失败: %v", c.name, requeue, nackErr)
	}
}

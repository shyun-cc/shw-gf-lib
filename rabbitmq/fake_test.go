package rabbitmq

// fake conn/channel 测试替身（设计 D2）：单测注入 scriptedDialer 触发
// 断连/重连/投毒消息，覆盖 guardian 状态机与策略分派，不依赖真 broker。

import (
	"context"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ---- 拨号脚本 ----

// scriptedDialer 拨号脚本：fn(n) 决定第 n 次拨号（从 1 起）的返回
type scriptedDialer struct {
	mu    sync.Mutex
	calls int
	fn    func(n int) (amqpConn, error)
}

func (d *scriptedDialer) dial(_ string, _ amqp.Config) (amqpConn, error) {
	d.mu.Lock()
	d.calls++
	n := d.calls
	d.mu.Unlock()
	return d.fn(n)
}

func (d *scriptedDialer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// setFn 并发安全地替换拨号脚本（guardian goroutine 正在读 fn）
func (d *scriptedDialer) setFn(fn func(n int) (amqpConn, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fn = fn
}

// ---- channel 替身 ----

// 各类调用的记录（参数序列化取证）
type exchangeRecord struct {
	name, kind string
	durable    bool
}
type queueRecord struct {
	name    string
	durable bool
}
type bindRecord struct {
	queue, key, exchange string
}
type qosRecord struct {
	prefetch int
}
type consumeRecord struct {
	queue, consumer string
	autoAck         bool
}
type publishRecord struct {
	exchange, key, contentType string
	body                       []byte
	deliveryMode               uint8
	mandatory                  bool
	messageID                  string
}

// fakeChannel amqpChannel 替身：记录声明/QoS/Consume/Publish 调用与参数，
// 支持注入各类错误、confirm 流与消费消息流。
type fakeChannel struct {
	mu sync.Mutex

	exchanges []exchangeRecord
	queues    []queueRecord
	binds     []bindRecord
	qos       []qosRecord
	consumes  []consumeRecord
	publishes []publishRecord
	confirmed bool // Confirm(noWait=false) 是否调用过
	closed    bool

	// 可注入错误
	declareErr error // ExchangeDeclare/QueueDeclare/QueueBind 统一失败
	publishErr error
	consumeErr error

	consumeCh  chan amqp.Delivery     // Consume 返回的消息流（测试投递消息用）
	confirmCh  chan amqp.Confirmation // NotifyPublish 注册的确认流
	returnCh   chan amqp.Return       // NotifyReturn 注册的退回流
	unroutable bool                   // mandatory 发布时模拟 broker basic.return
	closeCh    chan *amqp.Error       // NotifyClose 注册
}

func (ch *fakeChannel) ExchangeDeclare(name, kind string, durable, _, _, _ bool, _ amqp.Table) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.declareErr != nil {
		return ch.declareErr
	}
	ch.exchanges = append(ch.exchanges, exchangeRecord{name, kind, durable})
	return nil
}

func (ch *fakeChannel) QueueDeclare(name string, durable, _, _, _ bool, _ amqp.Table) (amqp.Queue, error) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.declareErr != nil {
		return amqp.Queue{}, ch.declareErr
	}
	ch.queues = append(ch.queues, queueRecord{name, durable})
	return amqp.Queue{Name: name}, nil
}

func (ch *fakeChannel) QueueBind(name, key, exchange string, _ bool, _ amqp.Table) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.declareErr != nil {
		return ch.declareErr
	}
	ch.binds = append(ch.binds, bindRecord{name, key, exchange})
	return nil
}

func (ch *fakeChannel) Qos(prefetchCount, _ int, _ bool) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.qos = append(ch.qos, qosRecord{prefetchCount})
	return nil
}

func (ch *fakeChannel) Consume(queue, consumer string, autoAck, _, _, _ bool, _ amqp.Table) (<-chan amqp.Delivery, error) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.consumeErr != nil {
		return nil, ch.consumeErr
	}
	ch.consumes = append(ch.consumes, consumeRecord{queue, consumer, autoAck})
	return ch.consumeCh, nil
}

func (ch *fakeChannel) PublishWithContext(_ context.Context, exchange, key string, mandatory, _ bool, msg amqp.Publishing) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.publishErr != nil {
		return ch.publishErr
	}
	ch.publishes = append(ch.publishes, publishRecord{exchange, key, msg.ContentType, msg.Body, msg.DeliveryMode, mandatory, msg.MessageId})
	if mandatory && ch.unroutable && ch.returnCh != nil {
		ch.returnCh <- amqp.Return{ReplyCode: 312, ReplyText: "NO_ROUTE", Exchange: exchange, RoutingKey: key, MessageId: msg.MessageId}
	}
	return nil
}

func (ch *fakeChannel) NotifyReturn(c chan amqp.Return) chan amqp.Return {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.returnCh = c
	return c
}

func (ch *fakeChannel) Confirm(noWait bool) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if noWait {
		return nil
	}
	ch.confirmed = true
	return nil
}

func (ch *fakeChannel) NotifyPublish(confirm chan amqp.Confirmation) chan amqp.Confirmation {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.confirmCh = confirm
	return confirm
}

func (ch *fakeChannel) NotifyClose(c chan *amqp.Error) chan *amqp.Error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.closeCh = c
	return c
}

func (ch *fakeChannel) Close() error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return amqp.ErrClosed
	}
	ch.closed = true
	if ch.consumeCh != nil {
		close(ch.consumeCh) // 对齐真实行为：channel 关闭时消息流关闭
	}
	if ch.confirmCh != nil {
		close(ch.confirmCh)
	}
	if ch.closeCh != nil {
		close(ch.closeCh) // 对齐真实行为：NotifyClose 订阅者收到通知
	}
	return nil
}

// 各类记录的只读快照（测试断言用）
func (ch *fakeChannel) snapshot() (ex []exchangeRecord, q []queueRecord, b []bindRecord,
	qos []qosRecord, cs []consumeRecord, ps []publishRecord, confirmed, closed bool) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return append([]exchangeRecord{}, ch.exchanges...),
		append([]queueRecord{}, ch.queues...),
		append([]bindRecord{}, ch.binds...),
		append([]qosRecord{}, ch.qos...),
		append([]consumeRecord{}, ch.consumes...),
		append([]publishRecord{}, ch.publishes...),
		ch.confirmed, ch.closed
}

// ---- 连接替身 ----

// fakeConn amqpConn 替身：Channel() 每次新建 fakeChannel 并记录；
// NotifyClose 注册的 chan 由测试 triggerClose 触发（模拟 broker 断连）。
type fakeConn struct {
	mu         sync.Mutex
	closed     bool
	closing    bool        // 断连/关闭是否已发生（防重复触发）
	pendingErr *amqp.Error // NotifyClose 注册前断连已发生：待补投的错误
	channels   []*fakeChannel
	closeCh    chan *amqp.Error
	chOpts     func(*fakeChannel) // 新建 channel 时应用（注入错误/替换消息流）
}

func newFakeConn(opts ...func(*fakeChannel)) *fakeConn {
	c := &fakeConn{chOpts: func(fc *fakeChannel) {
		for _, o := range opts {
			o(fc)
		}
	}}
	return c
}

func (c *fakeConn) Channel() (amqpChannel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.closing {
		// 对齐真实库：连接已关闭/断连后无法再开 channel
		return nil, amqp.ErrClosed
	}
	fc := &fakeChannel{consumeCh: make(chan amqp.Delivery, 16)}
	if c.chOpts != nil {
		c.chOpts(fc)
	}
	c.channels = append(c.channels, fc)
	return fc, nil
}

func (c *fakeConn) NotifyClose(ch chan *amqp.Error) chan *amqp.Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		// 断连早于注册（测试在 guardian serve 前触发）：立即补投，防事件丢失
		if c.pendingErr == nil {
			c.pendingErr = &amqp.Error{Code: amqp.ConnectionForced, Reason: "connection closed"}
		}
		ch <- c.pendingErr
		close(ch)
		return ch
	}
	c.closeCh = ch
	return ch
}

// triggerClose 模拟连接断开：通知 NotifyClose 订阅者并关闭全部 channel
// （对齐真实库语义：连接断开时所有 channel 随之关闭、消息流随之 close）。
func (c *fakeConn) triggerClose(err *amqp.Error) {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return
	}
	c.closing = true
	c.pendingErr = err
	channels := append([]*fakeChannel{}, c.channels...)
	closeCh := c.closeCh
	c.closeCh = nil
	c.mu.Unlock()

	for _, fc := range channels {
		fc.Close()
	}
	if closeCh != nil {
		closeCh <- err
		close(closeCh)
	}
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return amqp.ErrClosed
	}
	c.closed = true
	closeCh := c.closeCh
	closing := c.closing
	c.closing = true
	channels := append([]*fakeChannel{}, c.channels...)
	c.closeCh = nil
	c.mu.Unlock()

	// 对齐真实库：连接关闭时所有 channel 随之关闭
	for _, fc := range channels {
		fc.Close()
	}
	// 对齐真实库：主动 Close 后 NotifyClose 订阅者也会收到通知
	if closeCh != nil && !closing {
		closeCh <- &amqp.Error{Code: amqp.ConnectionForced, Reason: "close requested"}
		close(closeCh)
	}
	return nil
}

// setDeclareErr 动态设置「之后新建 channel」的声明错误注入（模拟 broker 406）
func (c *fakeConn) setDeclareErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.chOpts
	c.chOpts = func(fc *fakeChannel) {
		if old != nil {
			old(fc)
		}
		fc.declareErr = err
	}
}

// channelCount 已分配 channel 数
func (c *fakeConn) channelCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.channels)
}

// lastChannel 最近分配的 channel（consumer/producer 的宿主）
func (c *fakeConn) lastChannel() *fakeChannel {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.channels) == 0 {
		return nil
	}
	return c.channels[len(c.channels)-1]
}

// ---- 消息确认替身 ----

// nackRecord Nack 调用记录
type nackRecord struct {
	tag     uint64
	requeue bool
}

// fakeAcker amqp.Acknowledger 替身：记录 Ack/Nack 的 tag 与 requeue
type fakeAcker struct {
	mu    sync.Mutex
	acks  []uint64
	nacks []nackRecord
}

func (a *fakeAcker) Ack(tag uint64, _ bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.acks = append(a.acks, tag)
	return nil
}

func (a *fakeAcker) Nack(tag uint64, _, requeue bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nacks = append(a.nacks, nackRecord{tag, requeue})
	return nil
}

func (a *fakeAcker) Reject(uint64, bool) error { return nil }

func (a *fakeAcker) snapshot() ([]uint64, []nackRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]uint64{}, a.acks...), append([]nackRecord{}, a.nacks...)
}

// makeDelivery 构造带独立 acker 的测试消息
func makeDelivery(tag uint64, body []byte) (amqp.Delivery, *fakeAcker) {
	acker := &fakeAcker{}
	return amqp.Delivery{DeliveryTag: tag, Body: body, Acknowledger: acker}, acker
}

// ---- 公共辅助 ----

// newTestClient 快速退避配置的测试客户端（base 5ms / cap 20ms）+ 拨号注入
func newTestClient(d dialer) *Client {
	c := NewClient(Config{
		ReconnectBackoffBase: 5 * time.Millisecond,
		ReconnectBackoffCap:  20 * time.Millisecond,
	})
	c.dial = d
	return c
}

// waitFor 轮询等待条件成立（超时 fail）
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", msg)
}

package rabbitmq

// producer_test.go：正常发布/断开失败/confirm 等待（fake conn 取证）。

import (
	"context"
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// connectedProducer 已连接客户端上的 producer（返回宿主 channel 供断言）
func connectedProducer(t *testing.T, conn *fakeConn, opts ...ProducerOption) (*Client, *Producer) {
	t.Helper()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
	c := newTestClient(d.dial)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, c.NewProducer(opts...)
}

// TestProducerPublishDefaults 默认 persistent=2 + octet-stream + 立即成功
func TestProducerPublishDefaults(t *testing.T) {
	conn := newFakeConn()
	_, prod := connectedProducer(t, conn)
	ctx := context.Background()
	if err := prod.Publish(ctx, "ex", "rk", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, ps, confirmed, _ := conn.lastChannel().snapshot()
	if len(ps) != 1 {
		t.Fatal("应发布一条")
	}
	p := ps[0]
	if p.exchange != "ex" || p.key != "rk" || string(p.body) != "hello" {
		t.Fatalf("发布目标与消息体不符: %+v", p)
	}
	if p.deliveryMode != amqp.Persistent {
		t.Fatalf("默认应 persistent delivery_mode=2，实际 %d", p.deliveryMode)
	}
	if p.contentType != "application/octet-stream" {
		t.Fatalf("默认 ContentType 应 octet-stream: %s", p.contentType)
	}
	if confirmed {
		t.Fatal("默认不开 confirm，不应调用 Channel.Confirm")
	}
}

// TestProducerPublishOptions WithContentType/WithTransient/WithMessageId/WithHeaders
func TestProducerPublishOptions(t *testing.T) {
	conn := newFakeConn()
	_, prod := connectedProducer(t, conn)
	err := prod.Publish(context.Background(), "ex", "rk", []byte(`{"a":1}`),
		WithContentType("application/json"),
		WithTransient(),
		WithMessageID("m-1"),
		WithHeaders(amqp.Table{"x-trace": "abc"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, ps, _, _ := conn.lastChannel().snapshot()
	p := ps[0]
	if p.contentType != "application/json" {
		t.Fatalf("ContentType 应 json: %s", p.contentType)
	}
	if p.deliveryMode != amqp.Transient {
		t.Fatalf("WithTransient 应 delivery_mode=1，实际 %d", p.deliveryMode)
	}
}

// TestProducerNotConnected 断开期间 Publish 立即返回 ErrNotConnected，绝不静默缓存
func TestProducerNotConnected(t *testing.T) {
	prod := &Producer{} // 未 attach 任何 channel
	err := prod.Publish(context.Background(), "ex", "rk", []byte("m"))
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("断开期间应返回 ErrNotConnected，实际 %v", err)
	}
}

// TestProducerDetachOnDisconnect 连接断开时 channel 被释放（guardian 调度）
func TestProducerDetachOnDisconnect(t *testing.T) {
	conn := newFakeConn()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
	c := newTestClient(d.dial)
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	prod := c.NewProducer()
	if err := prod.Publish(ctx, "ex", "rk", []byte("m1")); err != nil {
		t.Fatal(err)
	}
	conn.triggerClose(&amqp.Error{Code: amqp.ConnectionForced, Reason: "boom"})
	waitFor(t, time.Second, func() bool {
		return errors.Is(prod.Publish(ctx, "ex", "rk", []byte("m2")), ErrNotConnected)
	}, "断开后 Publish 应立即失败")
	c.Close()
}

// confirmChOf 等 producer channel 分配并注册 confirm 流后返回该流（无则 nil）
func confirmChOf(t *testing.T, conn *fakeConn) chan amqp.Confirmation {
	t.Helper()
	var ch chan amqp.Confirmation
	waitFor(t, time.Second, func() bool {
		fc := conn.lastChannel()
		if fc == nil {
			return false
		}
		fc.mu.Lock()
		defer fc.mu.Unlock()
		if fc.confirmed && fc.confirmCh != nil {
			ch = fc.confirmCh
			return true
		}
		return false
	}, "producer channel 应分配并开启 confirm")
	return ch
}

// TestProducerConfirmAck confirm 模式：Confirm 开关 + 等 broker ack 后返回
func TestProducerConfirmAck(t *testing.T) {
	conn := newFakeConn()
	_, prod := connectedProducer(t, conn, WithConfirm())
	ctx := context.Background()

	go func() {
		if ch := confirmChOf(t, conn); ch != nil {
			ch <- amqp.Confirmation{DeliveryTag: 1, Ack: true}
		}
	}()
	if err := prod.Publish(ctx, "ex", "rk", []byte("m")); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, _, confirmed, _ := conn.lastChannel().snapshot()
	if !confirmed {
		t.Fatal("confirm 模式应调用 Channel.Confirm(false)")
	}
}

// TestProducerConfirmNack broker nack 如实返回 error
func TestProducerConfirmNack(t *testing.T) {
	conn := newFakeConn()
	_, prod := connectedProducer(t, conn, WithConfirm())
	go func() {
		if ch := confirmChOf(t, conn); ch != nil {
			ch <- amqp.Confirmation{DeliveryTag: 1, Ack: false}
		}
	}()
	err := prod.Publish(context.Background(), "ex", "rk", []byte("m"))
	if err == nil || !contains(err.Error(), "nack") {
		t.Fatalf("broker nack 应返回 error，实际 %v", err)
	}
}

// TestProducerConfirmCtxCancel 等待 confirm 期间 ctx 取消如实返回
func TestProducerConfirmCtxCancel(t *testing.T) {
	conn := newFakeConn()
	_, prod := connectedProducer(t, conn, WithConfirm())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		confirmChOf(t, conn) // 等 confirm 就绪
		cancel()             // 不回 confirm，直接取消
	}()
	err := prod.Publish(ctx, "ex", "rk", []byte("m"))
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("confirm 等待中 ctx 取消应返回取消错误，实际 %v", err)
	}
}

// TestProducerPublishError 底层 PublishWithContext 失败（连接已断）如实返回
func TestProducerPublishError(t *testing.T) {
	conn := newFakeConn(func(fc *fakeChannel) { fc.publishErr = errors.New("connection closed") })
	_, prod := connectedProducer(t, conn)
	err := prod.Publish(context.Background(), "ex", "rk", []byte("m"))
	if err == nil || !contains(err.Error(), "connection closed") {
		t.Fatalf("底层发布失败应如实返回，实际 %v", err)
	}
}

// contains 简易子串断言辅助
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestProducerMandatoryUnroutable mandatory 发布被退回时，即便随后 ack 也返回 ErrUnroutable
func TestProducerMandatoryUnroutable(t *testing.T) {
	conn := newFakeConn()
	_, prod := connectedProducer(t, conn, WithConfirm())
	ctx := context.Background()
	ch := confirmChOf(t, conn)
	fc := conn.lastChannel()
	fc.mu.Lock()
	fc.unroutable = true
	fc.mu.Unlock()
	go func() { ch <- amqp.Confirmation{DeliveryTag: 1, Ack: true} }()
	err := prod.Publish(ctx, "ex", "missing", []byte("m"), WithMandatory(), WithMessageID("task-1:1"))
	if !errors.Is(err, ErrUnroutable) {
		t.Fatalf("无路由应返回 ErrUnroutable，实际 %v", err)
	}
	_, _, _, _, _, ps, _, _ := fc.snapshot()
	if !ps[0].mandatory || ps[0].messageID != "task-1:1" {
		t.Fatalf("应以 mandatory+messageID 发布: %+v", ps[0])
	}
	// 可路由时同一生产者正常成功（旧退回不串到后续消息）
	fc.mu.Lock()
	fc.unroutable = false
	fc.mu.Unlock()
	go func() { ch <- amqp.Confirmation{DeliveryTag: 2, Ack: true} }()
	if err := prod.Publish(ctx, "ex", "rk", []byte("m"), WithMandatory(), WithMessageID("task-1:2")); err != nil {
		t.Fatal(err)
	}
}

// TestProducerMandatoryRequiresConfirm 非 confirm 生产者无法判定路由结果，拒绝 mandatory
func TestProducerMandatoryRequiresConfirm(t *testing.T) {
	conn := newFakeConn()
	_, prod := connectedProducer(t, conn)
	if err := prod.Publish(context.Background(), "ex", "rk", []byte("m"), WithMandatory(), WithMessageID("x")); err == nil {
		t.Fatal("非 confirm 模式 mandatory 应报错")
	}
}

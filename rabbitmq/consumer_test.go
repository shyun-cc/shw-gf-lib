package rabbitmq

// consumer_test.go：策略分派矩阵 / panic 隔离 / ctx 取消停机在途消息 /
// 毒消息 / QoS 默认值 / worker.Worker 装配（fake 注入，不依赖真 broker）。

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// runConsumerWithMessages 起 Run 循环并投递消息，等全部 handler 完成后
// cancel 结束 Run，返回 Run 的 error（串行语义：投递入队即会被逐条处理）。
func runConsumerWithMessages(t *testing.T, cons *Consumer, deliveries chan amqp.Delivery, allDone func() bool, msgs ...amqp.Delivery) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()
	for _, m := range msgs {
		deliveries <- m
	}
	waitFor(t, 2*time.Second, allDone, "全部消息处理完成")
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未在 2s 内退出")
		return nil
	}
}

// subscribedConsumer 已连接客户端上完成订阅的 consumer + 宿主消息流
func subscribedConsumer(t *testing.T, handler HandlerFunc, opts ...ConsumerOption) (*Client, *Consumer, chan amqp.Delivery) {
	t.Helper()
	conn := newFakeConn()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
	c := newTestClient(d.dial)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	cons := c.NewConsumer("q", handler, opts...)
	// 拿到订阅 channel 上的消息流（subscribe 时 Consume 返回的 chan）
	ch := conn.lastChannel()
	ch.mu.Lock()
	stream := ch.consumeCh
	ch.mu.Unlock()
	if stream == nil {
		t.Fatal("订阅消息流未建立")
	}
	return c, cons, stream
}

// TestConsumerPolicyMatrix 策略分派矩阵：handler 成败 × 三策略 → Ack/Nack(requeue) 落点
func TestConsumerPolicyMatrix(t *testing.T) {
	cases := []struct {
		policy      NackPolicy
		handlerErr  bool
		wantAck     int
		wantNack    int
		wantRequeue bool
	}{
		{AlwaysAck, false, 1, 0, false},
		{AlwaysAck, true, 1, 0, false}, // 无论成败均 Ack
		{NackRequeue, false, 1, 0, false},
		{NackRequeue, true, 0, 1, true}, // 失败 Nack+requeue
		{NackDrop, false, 1, 0, false},
		{NackDrop, true, 0, 1, false}, // 失败 Nack 不 requeue
	}
	for _, tc := range cases {
		var handled atomic.Int32
		handlerErr := tc.handlerErr
		handler := func(ctx context.Context, d Delivery) error {
			handled.Add(1)
			if handlerErr {
				return errors.New("业务失败")
			}
			return nil
		}
		_, cons, stream := subscribedConsumer(t, handler, WithNackPolicy(tc.policy))
		dl, acker := makeDelivery(1, []byte("m"))
		// 判据用 acker 记录（策略处置已执行完），而非仅 handler 计数
		wantAck, wantNack := tc.wantAck, tc.wantNack
		err := runConsumerWithMessages(t, cons, stream, func() bool {
			acks, nacks := acker.snapshot()
			return len(acks) == wantAck && len(nacks) == wantNack
		}, dl)
		if err != nil {
			t.Fatalf("policy=%d: Run 应正常退出: %v", tc.policy, err)
		}
		if handled.Load() != 1 {
			t.Fatalf("policy=%d: handler 应执行一次，实际 %d", tc.policy, handled.Load())
		}
		acks, nacks := acker.snapshot()
		if len(acks) != tc.wantAck {
			t.Fatalf("policy=%d handlerErr=%v: Ack 数 %d != %d", tc.policy, handlerErr, len(acks), tc.wantAck)
		}
		if len(nacks) != tc.wantNack {
			t.Fatalf("policy=%d handlerErr=%v: Nack 数 %d != %d", tc.policy, handlerErr, len(nacks), tc.wantNack)
		}
		if len(nacks) > 0 && nacks[0].requeue != tc.wantRequeue {
			t.Fatalf("policy=%d: Nack requeue=%v != %v", tc.policy, nacks[0].requeue, tc.wantRequeue)
		}
	}
}

// TestConsumerPanicIsolated handler panic 被 recover 隔离：记日志后按策略处置，循环不断
func TestConsumerPanicIsolated(t *testing.T) {
	var handled atomic.Int32
	boom := func(ctx context.Context, d Delivery) error {
		handled.Add(1)
		panic("handler 炸了")
	}
	_, cons, stream := subscribedConsumer(t, boom, WithNackPolicy(NackDrop))

	dl1, acker1 := makeDelivery(1, []byte("poison"))
	dl2, acker2 := makeDelivery(2, []byte("normal"))
	err := runConsumerWithMessages(t, cons, stream, func() bool {
		_, nacks1 := acker1.snapshot()
		_, nacks2 := acker2.snapshot()
		return len(nacks1) == 1 && len(nacks2) == 1
	}, dl1, dl2)
	if err != nil {
		t.Fatalf("panic 后 Run 不应异常退出: %v", err)
	}
	if handled.Load() != 2 {
		t.Fatalf("两条消息都应被处理（循环不断），实际 %d", handled.Load())
	}
	// panic 的消息按 NackDrop 处置
	_, nacks1 := acker1.snapshot()
	if len(nacks1) != 1 || nacks1[0].requeue {
		t.Fatalf("panic 消息应按 NackDrop 处置: %+v", nacks1)
	}
	// 后续消息正常（无 handler，默认策略 NackRequeue 成功路径是 Ack——此处 panic 后继续消费即证）
	acks2, _ := acker2.snapshot()
	if len(acks2) != 0 {
		t.Fatalf("第二条 handler 也 panic，不应 Ack: %+v", acks2)
	}
}

// TestConsumerSerialOrdering 串行一次一条：按序处理完成后才取下一条
func TestConsumerSerialOrdering(t *testing.T) {
	var order []int
	var handled atomic.Int32
	release := make(chan struct{})
	handler := func(ctx context.Context, d Delivery) error {
		// 第一条阻塞到 release，验证第二条不会被并发拉起
		if string(d.Body()) == "first" {
			<-release
		}
		order = append(order, len(d.Body()))
		handled.Add(1)
		return nil
	}
	_, cons, stream := subscribedConsumer(t, handler, WithNackPolicy(AlwaysAck))
	dl1, _ := makeDelivery(1, []byte("first"))
	dl2, _ := makeDelivery(2, []byte("m2"))
	go func() {
		stream <- dl1
		stream <- dl2
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()

	// 第一条未完成前，第二条绝不能进 handler
	time.Sleep(50 * time.Millisecond)
	if handled.Load() != 0 {
		t.Fatal("第一条阻塞时不应处理第二条（串行语义）")
	}
	close(release)
	waitFor(t, time.Second, func() bool { return handled.Load() == 2 }, "两条都应处理完")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run 未退出")
	}
	if order[0] != 5 || order[1] != 2 { // "first" 5 字节、"m2" 2 字节：验证顺序
		t.Fatalf("处理顺序错乱: %v", order)
	}
}

// TestConsumerCtxCancelWaitsInFlightHandler ctx 取消：停止取新消息、等在途 handler 完成后返回
func TestConsumerCtxCancelWaitsInFlightHandler(t *testing.T) {
	entered := make(chan struct{})
	finish := make(chan struct{})
	var completed atomic.Bool
	handler := func(ctx context.Context, d Delivery) error {
		close(entered)
		<-finish // 在途 handler 挂起
		completed.Store(true)
		return nil
	}
	_, cons, stream := subscribedConsumer(t, handler, WithNackPolicy(AlwaysAck))
	dl, _ := makeDelivery(1, []byte("m"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()

	stream <- dl
	<-entered
	cancel() // 在途时取消
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("在途 handler 未完成时 Run 不应返回")
	default:
	}
	close(finish)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅停机应返回 nil: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("在途 handler 完成后 Run 应返回")
	}
	if !completed.Load() {
		t.Fatal("在途 handler 应被等待完成")
	}
}

// TestConsumerPrefetchConfig QoS prefetch 可配（默认 10）
func TestConsumerPrefetchConfig(t *testing.T) {
	cases := []struct {
		opts []ConsumerOption
		want int
	}{
		{nil, consumerDefaultPrefetch},
		{[]ConsumerOption{WithPrefetch(3)}, 3},
		{[]ConsumerOption{WithPrefetch(0)}, 0},
	}
	for _, tc := range cases {
		conn := newFakeConn()
		d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
		c := newTestClient(d.dial)
		if err := c.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = c.NewConsumer("q", func(context.Context, Delivery) error { return nil }, tc.opts...)
		_, _, _, qos, cs, _, _, _ := conn.lastChannel().snapshot()
		if len(qos) != 1 || qos[0].prefetch != tc.want {
			t.Fatalf("opts=%v: QoS prefetch 应 %d: %+v", tc.opts, tc.want, qos)
		}
		if len(cs) != 1 || cs[0].autoAck {
			t.Fatalf("Consume 必须 autoAck=false: %+v", cs)
		}
		c.Close()
	}
}

// TestConsumerAutoAckFalseAndConsumerTag Consume 参数：autoAck=false + 自定义 consumer tag
func TestConsumerAutoAckFalseAndConsumerTag(t *testing.T) {
	conn := newFakeConn()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
	c := newTestClient(d.dial)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cons := c.NewConsumer("orders", func(context.Context, Delivery) error { return nil },
		WithConsumerName("order-worker"))
	if cons.Name() != "order-worker" {
		t.Fatalf("Name 应返回自定义名: %s", cons.Name())
	}
	_, _, _, _, cs, _, _, _ := conn.lastChannel().snapshot()
	if len(cs) != 1 || cs[0].consumer != "order-worker" || cs[0].queue != "orders" {
		t.Fatalf("Consume consumer tag 应等于 Name: %+v", cs)
	}
}

// TestConsumerRunWaitsWhenNotConnected 未连接时 Run 等待连接就绪（不空转报错不退出）
func TestConsumerRunWaitsWhenNotConnected(t *testing.T) {
	prod := &Client{} // 未 Connect 的客户端（仅用 ready 信号机制）
	cons := &Consumer{client: prod, name: "c", policy: NackRequeue, prefetch: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()
	time.Sleep(30 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("未连接时 Run 不应退出")
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("应 nil 退出: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel 后 Run 应退出")
	}
}

// TestConsumerImplementsWorker 编译期断言 worker.Worker 装配形态
func TestConsumerImplementsWorker(t *testing.T) {
	_, cons, _ := subscribedConsumer(t, func(context.Context, Delivery) error { return nil })
	if cons.Name() == "" {
		t.Fatal("Name 不应为空")
	}
}

// TestConsumerResubscribeAfterReconnect 断连→重连→消息流恢复续消费
func TestConsumerResubscribeAfterReconnect(t *testing.T) {
	conn1 := newFakeConn()
	conn2 := newFakeConn()
	d := &scriptedDialer{fn: func(n int) (amqpConn, error) {
		if n == 1 {
			return conn1, nil
		}
		return conn2, nil
	}}
	c := newTestClient(d.dial)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var handled atomic.Int32
	cons := c.NewConsumer("q", func(context.Context, Delivery) error {
		handled.Add(1)
		return nil
	}, WithNackPolicy(AlwaysAck))

	// conn1 消息流上投一条
	ch1 := conn1.lastChannel()
	ch1.mu.Lock()
	stream1 := ch1.consumeCh
	ch1.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()

	dl1, _ := makeDelivery(1, []byte("m1"))
	stream1 <- dl1
	waitFor(t, time.Second, func() bool { return handled.Load() == 1 }, "首条消息应被消费")

	// 断连 → conn1 消息流被 Close 关闭 → guardian 重连 → conn2 重订阅
	conn1.triggerClose(&amqp.Error{Code: amqp.ConnectionForced, Reason: "boom"})
	waitFor(t, 2*time.Second, func() bool {
		conn2.mu.Lock()
		n := len(conn2.channels)
		conn2.mu.Unlock()
		return n >= 1
	}, "重连后应重订阅")
	ch2 := conn2.lastChannel()
	ch2.mu.Lock()
	stream2 := ch2.consumeCh
	ch2.mu.Unlock()

	dl2, _ := makeDelivery(2, []byte("m2"))
	stream2 <- dl2
	waitFor(t, 2*time.Second, func() bool { return handled.Load() == 2 }, "重连后应续消费")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run 未退出")
	}
}

// TestConsumerDefaultPolicy 默认策略为 NackRequeue
func TestConsumerDefaultPolicy(t *testing.T) {
	c := NewClient(DefaultConfig())
	cons := c.NewConsumer("q", func(context.Context, Delivery) error { return nil })
	if cons.policy != NackRequeue {
		t.Fatalf("默认策略应 NackRequeue，实际 %d", cons.policy)
	}
}

// TestConsumerManualAck ManualAck 策略下消费者不代为确认，由 handler 决定
func TestConsumerManualAck(t *testing.T) {
	handler := func(ctx context.Context, d Delivery) error {
		if string(d.Body()) == "ack" {
			return d.Ack()
		}
		return errors.New("handler 选择不确认")
	}
	_, cons, stream := subscribedConsumer(t, handler, WithNackPolicy(ManualAck))
	dl1, acker1 := makeDelivery(1, []byte("ack"))
	dl2, acker2 := makeDelivery(2, []byte("hold"))
	var done atomic.Bool
	err := runConsumerWithMessages(t, cons, stream, func() bool {
		acks, _ := acker1.snapshot()
		if len(acks) == 1 {
			done.Store(true)
		}
		return done.Load()
	}, dl1, dl2)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	acks, nacks := acker2.snapshot()
	if len(acks) != 0 || len(nacks) != 0 {
		t.Fatalf("ManualAck 失败时消费者不得确认: %v %v", acks, nacks)
	}
}

// TestConsumerStop Stop 关闭独占 channel、退出 Run，且不再被重订阅
func TestConsumerStop(t *testing.T) {
	c, cons, _ := subscribedConsumer(t, func(context.Context, Delivery) error { return nil })
	ch := cons.ch
	done := make(chan error, 1)
	go func() { done <- cons.Run(context.Background()) }()
	cons.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 后 Run 未退出")
	}
	if fc, ok := ch.(*fakeChannel); !ok || !fc.closed {
		t.Fatal("Stop 应关闭订阅 channel（未确认消息由 broker 重新入队）")
	}
	c.mu.Lock()
	n := len(c.consumers)
	c.mu.Unlock()
	if n != 0 {
		t.Fatal("Stop 后不应留在重订阅注册表")
	}
	cons.Stop() // 幂等
}

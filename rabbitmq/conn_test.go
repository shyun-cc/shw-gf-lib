package rabbitmq

// conn_test.go：guardian 重连状态机覆盖——断连→退避重连→拓扑重放→
// producer channel 重建→consumer 重订阅的完整调用序列（fake conn 取证）。

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TestConnectFirstDialFails 首连失败如实返回 error（不做内部重试）
func TestConnectFirstDialFails(t *testing.T) {
	d := &scriptedDialer{fn: func(int) (amqpConn, error) {
		return nil, errors.New("dial tcp refused")
	}}
	c := newTestClient(d.dial)
	err := c.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "dial tcp refused") {
		t.Fatalf("首连失败应原样返回底层错误，实际 %v", err)
	}
	if d.callCount() != 1 {
		t.Fatalf("首连只应拨号一次，实际 %d 次", d.callCount())
	}
	c.Close()
}

// TestConnectEstablishFailRollback establish 失败（拓扑重放撞 406）应回滚并返回错误
func TestConnectEstablishFailRollback(t *testing.T) {
	conn := newFakeConn(func(fc *fakeChannel) { fc.declareErr = errors.New("406 PRECONDITION_FAILED") })
	c := newTestClient(func(string, amqp.Config) (amqpConn, error) { return conn, nil })
	// 预注册一条拓扑（模拟「断连后重连」场景：establish 需重放注册表）
	c.mu.Lock()
	c.topologies = append(c.topologies, TaskTopology("ex", "direct", "q", "rk"))
	c.mu.Unlock()

	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("拓扑重放失败应返回 error")
	}
	// 回滚验证：guardianDone 复位，Close 不死锁；连接被关闭
	c.mu.Lock()
	done := c.guardianDone
	connected := c.connected
	c.mu.Unlock()
	if done != nil {
		t.Fatal("establish 失败后 guardianDone 应复位为 nil")
	}
	if connected {
		t.Fatal("establish 失败后不应标记已连接")
	}
	if conn.channelCount() == 0 {
		t.Fatal("establish 应尝试过声明拓扑")
	}
	c.Close() // 不应死锁
}

// TestChannelLevelSelfHeal channel 单独被关闭（连接仍在）时自愈：
// consumer 重订阅、producer 重建 channel，无需等到连接级断连
func TestChannelLevelSelfHeal(t *testing.T) {
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
	base := conn.channelCount() // Connect(0) + producer(1)

	// 单独关闭 producer 的 channel（连接不断）
	prod.mu.Lock()
	prodCh := prod.ch
	prod.mu.Unlock()
	if err := prodCh.Close(); err != nil {
		t.Fatal(err)
	}
	// 自愈：同一连接上新开 channel，Publish 恢复
	waitFor(t, 2*time.Second, func() bool {
		return conn.channelCount() == base+1 && prod.Publish(ctx, "ex", "rk", []byte("m2")) == nil
	}, "channel 级自愈后 Publish 应恢复")
	c.Close()
}

// TestChannelLevelSelfHealConsumer consumer 的 channel 单独被关（如 queue 删除触发 404）：
// 自动重订阅新 channel，消费继续
func TestChannelLevelSelfHealConsumer(t *testing.T) {
	conn := newFakeConn()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
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
	base := conn.channelCount()

	// 拿当前订阅 channel 并单独关闭它（连接不断）
	cons.mu.Lock()
	subCh := cons.ch
	cons.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()
	if err := subCh.Close(); err != nil {
		t.Fatal(err)
	}
	// 自愈：同连接新 channel 重订阅
	waitFor(t, 2*time.Second, func() bool {
		return conn.channelCount() == base+1
	}, "channel 级自愈应重订阅")
	// 在新流上投递消息，消费继续
	conn.mu.Lock()
	newCh := conn.channels[len(conn.channels)-1]
	conn.mu.Unlock()
	newCh.mu.Lock()
	newStream := newCh.consumeCh
	newCh.mu.Unlock()
	if newStream == nil {
		t.Fatal("自愈后消息流未恢复")
	}
	dl, _ := makeDelivery(1, []byte("m"))
	newStream <- dl
	waitFor(t, 2*time.Second, func() bool { return handled.Load() == 1 }, "自愈后应续消费")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run 未退出")
	}
}

// TestGuardianReconnectFlow 断连→重连→重声明拓扑→重建 channel→重订阅 全链路
func TestGuardianReconnectFlow(t *testing.T) {
	conn1 := newFakeConn()
	conn2 := newFakeConn()
	d := &scriptedDialer{fn: func(n int) (amqpConn, error) {
		if n == 1 {
			return conn1, nil
		}
		return conn2, nil
	}}
	c := newTestClient(d.dial)
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	// 注册拓扑 + producer + consumer（conn1 上各占一条 channel）
	if err := c.Declare(ctx, TaskTopology("ex", "direct", "q", "rk")); err != nil {
		t.Fatal(err)
	}
	prod := c.NewProducer()
	cons := c.NewConsumer("q", func(context.Context, Delivery) error { return nil })
	if got := conn1.channelCount(); got != 3 { // Declare 1 + producer 1 + consumer 1
		t.Fatalf("conn1 channel 数应 3，实际 %d", got)
	}

	// producer 正常发布
	if err := prod.Publish(ctx, "ex", "rk", []byte("m1")); err != nil {
		t.Fatal(err)
	}

	// 触发断连：producer 立即失败，随后自动重连
	conn1.triggerClose(&amqp.Error{Code: amqp.ConnectionForced, Reason: "boom"})
	waitFor(t, time.Second, func() bool {
		return errors.Is(prod.Publish(ctx, "ex", "rk", []byte("m2")), ErrNotConnected)
	}, "断开后 Publish 应立即返回 ErrNotConnected")

	// 重连完成：conn2 上拓扑重放（1 channel）+ producer 重建（1）+ consumer 重订阅（1）
	waitFor(t, 2*time.Second, func() bool {
		return conn2.channelCount() >= 3
	}, "重连后 conn2 应重建全部 channel")

	// 拓扑重放断言：conn2 的第一条 channel 上 exchange/queue/bind 各一次
	ex, q, b, _, _, _, _, _ := conn2.channels[0].snapshot()
	if len(ex) != 1 || ex[0].name != "ex" || ex[0].kind != "direct" || !ex[0].durable {
		t.Fatalf("重连后 exchange 未按注册表重声明: %+v", ex)
	}
	if len(q) != 1 || q[0].name != "q" || !q[0].durable {
		t.Fatalf("重连后 queue 未按注册表重声明: %+v", q)
	}
	if len(b) != 1 || b[0].queue != "q" || b[0].exchange != "ex" || b[0].key != "rk" {
		t.Fatalf("重连后 bind 未按注册表重声明: %+v", b)
	}

	// consumer 重订阅断言：conn2 的订阅 channel 上 Qos + Consume(autoAck=false)
	_, _, _, qos, cs, _, _, _ := conn2.channels[2].snapshot()
	if len(qos) != 1 || qos[0].prefetch != consumerDefaultPrefetch {
		t.Fatalf("重订阅应设置 QoS prefetch=%d: %+v", consumerDefaultPrefetch, qos)
	}
	if len(cs) != 1 || cs[0].queue != "q" || cs[0].autoAck {
		t.Fatalf("重订阅应 Consume(queue=q, autoAck=false): %+v", cs)
	}
	if cons == nil {
		t.Fatal("consumer 应存在")
	}

	// 重连后 producer 恢复发布
	waitFor(t, 2*time.Second, func() bool {
		return prod.Publish(ctx, "ex", "rk", []byte("m3")) == nil
	}, "重连后 Publish 应恢复成功")

	c.Close()
}

// TestGuardianReconnectRetryAfterEstablishFail 重连成功但拓扑重放失败（406 配置漂移）→
// 不放弃，继续退避重试直到成功
func TestGuardianReconnectRetryAfterEstablishFail(t *testing.T) {
	conn1 := newFakeConn()
	conn2 := newFakeConn(func(fc *fakeChannel) { fc.declareErr = errors.New("406") })
	conn3 := newFakeConn()
	d := &scriptedDialer{fn: func(n int) (amqpConn, error) {
		switch n {
		case 1:
			return conn1, nil
		case 2:
			return conn2, nil // 重连成功但 establish 失败
		default:
			return conn3, nil
		}
	}}
	c := newTestClient(d.dial)
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Declare(ctx, TaskTopology("ex", "fanout", "q", "")); err != nil {
		t.Fatal(err)
	}

	conn1.triggerClose(&amqp.Error{Code: amqp.ConnectionForced, Reason: "boom"})
	// conn2 establish 失败后继续重试到 conn3 成功
	waitFor(t, 2*time.Second, func() bool {
		return conn3.channelCount() >= 1 && d.callCount() >= 3
	}, "establish 失败后应继续退避重连")
	c.Close()
}

// TestGuardianDialRetryBackoff 重连拨号失败按退避继续重试
func TestGuardianDialRetryBackoff(t *testing.T) {
	conn1 := newFakeConn()
	conn2 := newFakeConn()
	d := &scriptedDialer{fn: func(n int) (amqpConn, error) {
		if n == 1 {
			return conn1, nil
		}
		if n <= 3 {
			return nil, errors.New("broker 未就绪")
		}
		return conn2, nil
	}}
	c := newTestClient(d.dial)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn1.triggerClose(&amqp.Error{Code: amqp.ConnectionForced, Reason: "boom"})
	// 该 Client 无拓扑/订阅：重连成功后 conn2 不会开 channel，
	// 以「拨号推进到第 4 次且不再增加」判定（第 4 次成功 establish 后 guardian 进入 serve）
	waitFor(t, 2*time.Second, func() bool {
		n := d.callCount()
		return n >= 4
	}, "拨号失败后应退避重试到成功")
	time.Sleep(50 * time.Millisecond) // 若 establish 失败会继续拨第 5 次
	if got := d.callCount(); got != 4 {
		t.Fatalf("第 4 次拨号成功后不应继续拨，实际 %d 次", got)
	}
	c.Close()
}

// TestCloseGracefulShutdown Close 停机：关闭连接、guardian 退出、幂等可重复
func TestCloseGracefulShutdown(t *testing.T) {
	conn := newFakeConn()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
	c := newTestClient(d.dial)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c.Close() // 幂等

	c.mu.Lock()
	done := c.guardianDone
	connected := c.connected
	c.mu.Unlock()
	select {
	case <-done: // guardian 已退出
	default:
		t.Fatal("Close 后 guardian 应退出")
	}
	if connected {
		t.Fatal("Close 后不应保持连接状态")
	}
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if !closed {
		t.Fatal("Close 应关闭底层连接")
	}
}

// TestConnectCtxCancelCancelShutsDown lifecycle ctx 取消等同 Close（优雅停机）
func TestConnectCtxCancelCancelShutsDown(t *testing.T) {
	conn := newFakeConn()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
	c := newTestClient(d.dial)
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()

	c.mu.Lock()
	done := c.guardianDone
	c.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后 guardian 应退出")
	}
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if !closed {
		t.Fatal("ctx 取消应关闭底层连接")
	}
}

// TestConnectIdempotent 重复 Connect 直接成功且丢弃新连接（不 double-establish）
func TestConnectIdempotent(t *testing.T) {
	conn1 := newFakeConn()
	conn2 := newFakeConn()
	d := &scriptedDialer{fn: func(n int) (amqpConn, error) {
		if n == 1 {
			return conn1, nil
		}
		return conn2, nil
	}}
	c := newTestClient(d.dial)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("重复 Connect 不应报错: %v", err)
	}
	if conn2.channelCount() != 0 {
		t.Fatal("重复 Connect 的新连接应被丢弃，不应 establish")
	}
	c.Close()
}

// TestDeclareRequiresConnection 未连接时 Declare 返回 ErrNotConnected
func TestDeclareRequiresConnection(t *testing.T) {
	c := NewClient(DefaultConfig())
	if err := c.Declare(context.Background(), TaskTopology("ex", "direct", "q", "rk")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("未连接 Declare 应返回 ErrNotConnected，实际 %v", err)
	}
}

// TestDeclareErrorNotRegistered 声明失败（406）不进注册表：重连后不重放坏拓扑
func TestDeclareErrorNotRegistered(t *testing.T) {
	conn := newFakeConn()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
	c := newTestClient(d.dial)
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// 先声明一条好拓扑
	if err := c.Declare(ctx, TaskTopology("good-ex", "direct", "good-q", "rk")); err != nil {
		t.Fatal(err)
	}
	// 再注入 406 声明坏拓扑：失败且不登记（之后新建的 channel 都带此错误）
	conn.setDeclareErr(errors.New("406 PRECONDITION_FAILED"))
	if err := c.Declare(ctx, TaskTopology("bad-ex", "direct", "bad-q", "rk")); err == nil {
		t.Fatal("同名异参 406 应如实报错")
	}
	// 清除注入后重连，验证只有好拓扑被重放
	conn.setDeclareErr(nil)
	conn2 := newFakeConn()
	d.setFn(func(int) (amqpConn, error) { return conn2, nil })
	conn.triggerClose(&amqp.Error{Code: amqp.ConnectionForced, Reason: "boom"})
	waitFor(t, 2*time.Second, func() bool {
		return conn2.channelCount() >= 1
	}, "重连后应重放拓扑")
	ex, _, _, _, _, _, _, _ := conn2.channels[0].snapshot()
	if len(ex) != 1 || ex[0].name != "good-ex" {
		t.Fatalf("只应重放登记过的拓扑: %+v", ex)
	}
	c.Close()
}

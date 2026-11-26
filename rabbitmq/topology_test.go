package rabbitmq

// topology_test.go：Topology 声明参数序列化与幂等重放（fake channel 记录调用）。

import (
	"context"
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// TestTopologyDeclareOrderAndParams 声明顺序（exchange→queue→bind）与参数逐项落库
func TestTopologyDeclareOrderAndParams(t *testing.T) {
	ch := &fakeChannel{}
	topo := Topology{
		Exchanges: []ExchangeSpec{
			{Name: "ex1", Kind: "direct", Durable: true},
			{Name: "ex2", Kind: "fanout", AutoDelete: true},
		},
		Queues: []QueueSpec{
			{Name: "q1", Durable: true},
			{Name: "q2", Exclusive: true},
		},
		Binds: []BindSpec{
			{Queue: "q1", Key: "rk1", Exchange: "ex1"},
			{Queue: "q2", Key: "", Exchange: "ex2"},
		},
	}
	if err := topo.declareOn(ch); err != nil {
		t.Fatal(err)
	}
	ex, q, b, _, _, _, _, _ := ch.snapshot()
	if len(ex) != 2 || ex[0].name != "ex1" || ex[0].kind != "direct" || !ex[0].durable {
		t.Fatalf("exchange 声明参数不符: %+v", ex)
	}
	if ex[1].name != "ex2" || ex[1].durable {
		t.Fatalf("ex2 应 AutoDelete 非 durable: %+v", ex[1])
	}
	if len(q) != 2 || q[0].name != "q1" || !q[0].durable {
		t.Fatalf("queue 声明参数不符: %+v", q)
	}
	if len(b) != 2 || b[0].queue != "q1" || b[0].exchange != "ex1" || b[0].key != "rk1" {
		t.Fatalf("bind 声明参数不符: %+v", b)
	}
}

// TestTopologyDeclareErrorPropagated 声明失败（406 同名异参）如实透传且带上下文
func TestTopologyDeclareErrorPropagated(t *testing.T) {
	ch := &fakeChannel{declareErr: errors.New("406 PRECONDITION_FAILED")}
	topo := TaskTopology("ex", "direct", "q", "rk")
	err := topo.declareOn(ch)
	if err == nil {
		t.Fatal("406 应如实透传")
	}
	if !errors.Is(err, ch.declareErr) {
		t.Fatalf("应携带底层错误: %v", err)
	}
}

// TestTopologyIdempotentReplay 幂等重放：同一拓扑重复声明无副作用（fake 记录双份调用，均成功）
func TestTopologyIdempotentReplay(t *testing.T) {
	conn := newFakeConn()
	d := &scriptedDialer{fn: func(int) (amqpConn, error) { return conn, nil }}
	c := newTestClient(d.dial)
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	topo := TaskTopology("ex", "direct", "q", "rk")
	// 同一拓扑声明两次（同名同参幂等）
	if err := c.Declare(ctx, topo); err != nil {
		t.Fatal(err)
	}
	if err := c.Declare(ctx, topo); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	n := len(c.topologies)
	c.mu.Unlock()
	if n != 2 {
		t.Fatalf("两次 Declare 应登记两条（值语义），实际 %d", n)
	}

	// 重连重放：两条拓扑各自重放（声明幂等，broker 侧无副作用）
	conn2 := newFakeConn()
	d.setFn(func(int) (amqpConn, error) { return conn2, nil })
	conn.triggerClose(&amqp.Error{Code: amqp.ConnectionForced, Reason: "boom"})
	waitFor(t, 2*time.Second, func() bool {
		return conn2.channelCount() >= 2
	}, "重连后应逐条重放拓扑")
	ex, _, _, _, _, _, _, _ := conn2.channels[0].snapshot()
	if len(ex) != 1 || ex[0].name != "ex" {
		t.Fatalf("拓扑重放参数应与注册表一致: %+v", ex)
	}
	c.Close()
}

// TestTaskTopology便捷构造 durable exchange + durable queue + bind，kind 由参数定
func TestTaskTopology(t *testing.T) {
	for _, tc := range []struct {
		kind string
		key  string
	}{
		{"fanout", ""},
		{"direct", "task.wake"},
	} {
		topo := TaskTopology("t-ex", tc.kind, "t-q", tc.key)
		if len(topo.Exchanges) != 1 || !topo.Exchanges[0].Durable || topo.Exchanges[0].Kind != tc.kind {
			t.Fatalf("kind=%s: exchange 应 durable 且 kind 由参数定: %+v", tc.kind, topo.Exchanges)
		}
		if len(topo.Queues) != 1 || !topo.Queues[0].Durable {
			t.Fatalf("kind=%s: queue 应 durable: %+v", tc.kind, topo.Queues)
		}
		if len(topo.Binds) != 1 || topo.Binds[0].Queue != "t-q" || topo.Binds[0].Exchange != "t-ex" || topo.Binds[0].Key != tc.key {
			t.Fatalf("kind=%s: bind 参数不符: %+v", tc.kind, topo.Binds)
		}
	}
}

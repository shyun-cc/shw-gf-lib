//go:build integration

package tests

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
	taskamqp "github.com/shyun-cc/shw-gf-lib/task/amqp"
)

type platformAmqp struct {
	ctx       context.Context
	store     *task.Store
	clock     *atomic.Int64
	transport *taskamqp.Transport
	admin     *amqp.Connection
	prefix    string
}

func withOutbox(c *task.StoreConfig) { c.Outbox = true }

// setupPlatformAmqp 装配 TypeControl+Outbox Store 与独占前缀的平台拓扑（类别 default 与 batch）。
func setupPlatformAmqp(t *testing.T) *platformAmqp {
	t.Helper()
	cfg := amqpTestConfig(t)
	ctx, s, _, clock := setupPlatform(t, withOutbox)
	client := rabbitmq.NewClient(cfg)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	prefix := "gflib-itest-" + uuid.NewString()[:8]
	if err := client.Declare(ctx, taskamqp.PlatformTopology(prefix, []string{task.DefaultClass, "batch"}, false)); err != nil {
		t.Fatal(err)
	}
	admin := openAmqpAdmin(t)
	t.Cleanup(func() {
		ch, err := admin.Channel()
		if err != nil {
			return
		}
		defer ch.Close()
		for _, q := range []string{taskamqp.QueueName(prefix, task.DefaultClass), taskamqp.QueueName(prefix, "batch"), taskamqp.DeadLetterQueue(prefix)} {
			_, _ = ch.QueueDelete(q, false, false, false)
		}
		_ = ch.ExchangeDelete(taskamqp.ExchangeName(prefix), false, false)
		_ = ch.ExchangeDelete(taskamqp.DeadLetterExchange(prefix), false, false)
	})
	return &platformAmqp{ctx: ctx, store: s, clock: clock, transport: taskamqp.NewTransport(client, prefix), admin: admin, prefix: prefix}
}

type getDelivery struct{ d amqp.Delivery }

func (g getDelivery) Body() []byte            { return g.d.Body }
func (g getDelivery) Ack() error              { return g.d.Ack(false) }
func (g getDelivery) Nack(requeue bool) error { return g.d.Nack(false, requeue) }

// fetch basic.get 一条消息（等待最多 3s），手动确认由调用方决定。
func (p *platformAmqp) fetch(t *testing.T, queue string) (getDelivery, bool) {
	t.Helper()
	ch, err := p.admin.Channel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d, ok, err := ch.Get(queue, false)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return getDelivery{d}, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return getDelivery{}, false
}

func (p *platformAmqp) relay() *task.Relay {
	return &task.Relay{Store: p.store, Publisher: p.transport, Owner: "relay-1", Lease: 30 * time.Second}
}

func (p *platformAmqp) outboxRow(t *testing.T, taskID string) gdb.Record {
	t.Helper()
	row, err := g.DB().GetOne(p.ctx, `SELECT * FROM task_outbox WHERE task_id=? ORDER BY generation DESC LIMIT 1`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestPlatformOutboxAtomicConfirmAndUnroutable(t *testing.T) {
	p := setupPlatformAmqp(t)
	ctx, s := p.ctx, p.store
	openType(t, ctx, s, "sync", 10, nil)
	openType(t, ctx, s, "orphan", 10, func(c *task.TypeConfig) { c.Class = "nobody" })
	instance := platformInstance(t, ctx, s, 10, map[string]string{"sync": "v1", "orphan": "v1"})
	queue := taskamqp.QueueName(p.prefix, task.DefaultClass)

	// 调用方事务回滚：任务与通知都不落库。
	boom := errors.New("business rollback")
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := s.SubmitTx(ctx, tx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, "tx-rollback", nil); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if n, _ := g.DB().GetCount(ctx, `SELECT 1 FROM task_outbox`); n != 0 {
		t.Fatal("rolled back submit left outbox rows", n)
	}
	var first *task.Run
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		first, err = s.SubmitTx(ctx, tx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, "tx-commit", nil)
		return err
	})
	if err != nil || first.DispatchGeneration != 1 {
		t.Fatal(first, err)
	}
	if row := p.outboxRow(t, first.ID); row["status"].String() != "pending" || row["generation"].Int64() != 1 {
		t.Fatal(row)
	}
	if n, err := p.relay().RunOnce(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if row := p.outboxRow(t, first.ID); row["status"].String() != "sent" {
		t.Fatal(row)
	}

	// confirm 后写回前崩溃：租约过期后被再次投递，队列里同一代次两条消息。
	second, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{"n":2}`)}, "dup", nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ClaimOutbox(ctx, "crashed-relay", 10, 30*time.Second)
	if err != nil || len(items) != 1 || items[0].Message.TaskID != second.ID {
		t.Fatal(items, err)
	}
	if err = p.transport.Publish(ctx, items[0].Class, items[0].Message); err != nil {
		t.Fatal(err)
	}
	p.clock.Add(int64(31 * time.Second))
	if err = s.HeartbeatInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	if n, err := p.relay().RunOnce(ctx); err != nil || n != 1 {
		t.Fatal("expired publish lease must be re-published", n, err)
	}
	claimed, stale := 0, 0
	for i := 0; i < 3; i++ {
		d, ok := p.fetch(t, queue)
		if !ok {
			t.Fatalf("expected 3 messages, got %d", i)
		}
		lease, decision, err := s.Accept(ctx, instance, d.Body(), "")
		if err != nil {
			t.Fatal(err)
		}
		switch decision {
		case task.DeliveryClaimed:
			claimed++
			if lease == nil {
				t.Fatal("claimed without lease")
			}
		case task.DeliveryStale:
			stale++
		default:
			t.Fatal("unexpected decision", decision)
		}
		if err = d.Ack(); err != nil {
			t.Fatal(err)
		}
	}
	if claimed != 2 || stale != 1 {
		t.Fatalf("duplicate delivery must execute once per task: claimed=%d stale=%d", claimed, stale)
	}

	// 无路由：broker 退回，保留待重试且不记已投递。
	orphan, err := s.Submit(ctx, task.Definition{Type: "orphan", Payload: []byte(`{}`)}, "orphan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := p.relay().RunOnce(ctx); err != nil || n != 0 {
		t.Fatal("unroutable must not count as sent", n, err)
	}
	row := p.outboxRow(t, orphan.ID)
	if row["status"].String() != "pending" || row["publish_attempts"].Int() != 1 || !strings.Contains(row["last_error"].String(), "unroutable") {
		t.Fatal(row)
	}
}

func TestPlatformOutboxDeferRetryAndRepair(t *testing.T) {
	p := setupPlatformAmqp(t)
	ctx, s := p.ctx, p.store
	typ, err := s.RegisterTypes(ctx, []task.TypeSpec{{Type: "sync", HandlerVersion: "v1", Policy: platformPolicy()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := typ[0].TypeConfig
	cfg.SubmitEnabled = true
	cur, err := s.ConfigureType(ctx, "sync", typ[0].Version, cfg, "submit-only", nil)
	if err != nil {
		t.Fatal(err)
	}
	instance := platformInstance(t, ctx, s, 10, map[string]string{"sync": "v1"})
	queue := taskamqp.QueueName(p.prefix, task.DefaultClass)
	run, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, "r1", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 消费关闭：投递器不发布，积压保留（无热循环）。
	for i := 0; i < 3; i++ {
		if n, err := p.relay().RunOnce(ctx); err != nil || n != 0 {
			t.Fatal("paused type published", n, err)
		}
	}
	if cur, err = s.SetTypeSwitches(ctx, "sync", cur.Version, true, true, "open", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := p.relay().RunOnce(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	// 投递后又暂停：消费者持久化延期通知后 ACK，暂停期间不再发布。
	if cur, err = s.SetTypeSwitches(ctx, "sync", cur.Version, true, false, "pause", nil); err != nil {
		t.Fatal(err)
	}
	d, ok := p.fetch(t, queue)
	if !ok {
		t.Fatal("no message")
	}
	if _, decision, err := s.Accept(ctx, instance, d.Body(), ""); err != nil || decision != task.DeliveryDeferred {
		t.Fatal(decision, err)
	}
	_ = d.Ack()
	if row := p.outboxRow(t, run.ID); row["status"].String() != "pending" || row["deferrals"].Int() != 1 {
		t.Fatal(row)
	}
	p.clock.Add(int64(5 * time.Second))
	if n, _ := p.relay().RunOnce(ctx); n != 0 {
		t.Fatal("paused type re-published after deferral")
	}
	if cur, err = s.SetTypeSwitches(ctx, "sync", cur.Version, true, true, "resume", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := p.relay().RunOnce(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = s.HeartbeatInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	d, _ = p.fetch(t, queue)
	oldBody := d.Body()
	lease, decision, err := s.Accept(ctx, instance, oldBody, "")
	if err != nil || decision != task.DeliveryClaimed {
		t.Fatal(decision, err)
	}
	_ = d.Ack()
	// 失败到期重试：同事务生成第 2 代通知；旧代次消息按 stale 处理。
	if err = s.Complete(ctx, lease, task.Completion{Outcome: task.OutcomeRetryableFailure}, nil); err != nil {
		t.Fatal(err)
	}
	p.clock.Add(int64(2 * time.Second))
	if _, n, err := s.Recover(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if r, _ := s.GetRun(ctx, run.ID); r.DispatchGeneration != 2 {
		t.Fatal("retry must create next generation", r.DispatchGeneration)
	}
	if err = s.HeartbeatInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	if _, decision, err = s.Accept(ctx, instance, oldBody, ""); err != nil || decision != task.DeliveryStale {
		t.Fatal("old generation must be stale", decision, err)
	}
	// 投递后消息丢失（清空队列）：超过 staleAfter 才补投新代次。
	if n, err := p.relay().RunOnce(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	ch, err := p.admin.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ch.QueuePurge(queue, false); err != nil {
		t.Fatal(err)
	}
	_ = ch.Close()
	if n, err := s.RepairDispatch(ctx, 100, time.Minute); err != nil || n != 0 {
		t.Fatal("fresh sent notification must not be repaired", n, err)
	}
	p.clock.Add(int64(61 * time.Second))
	if n, err := s.RepairDispatch(ctx, 100, time.Minute); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err := p.relay().RunOnce(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = s.HeartbeatInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	d, ok = p.fetch(t, queue)
	if !ok {
		t.Fatal("repaired notification not delivered")
	}
	if _, decision, err = s.Accept(ctx, instance, d.Body(), ""); err != nil || decision != task.DeliveryClaimed {
		t.Fatal(decision, err)
	}
	_ = d.Ack()
}

func TestPlatformPoisonToDeadLetterViaConsume(t *testing.T) {
	p := setupPlatformAmqp(t)
	ctx, s := p.ctx, p.store
	openType(t, ctx, s, "sync", 10, nil)
	instance := platformInstance(t, ctx, s, 10, map[string]string{"sync": "v1"})
	run, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, "ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := p.relay().RunOnce(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	foreign := task.DispatchMessage{Version: task.DispatchProtocol, Namespace: "other", TaskID: uuid.NewString(), Generation: 1}
	missing := task.DispatchMessage{Version: task.DispatchProtocol, Namespace: "test", TaskID: uuid.NewString(), Generation: 1}
	for _, body := range [][]byte{[]byte("not-json"), foreign.Encode(), missing.Encode()} {
		publishViaAdmin(t, p.admin, taskamqp.ExchangeName(p.prefix), task.DefaultClass, body)
	}
	var claimed, poisoned atomic.Int32
	consumeCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- p.transport.Consume(consumeCtx, task.DefaultClass, 4, func(ctx context.Context, d task.Delivery) {
			lease, decision, err := s.Accept(ctx, instance, d.Body(), "")
			if err != nil {
				_ = d.Nack(true)
				return
			}
			switch decision {
			case task.DeliveryPoison:
				poisoned.Add(1)
				_ = d.Nack(false)
			case task.DeliveryClaimed:
				claimed.Add(1)
				_ = d.Ack()
				_ = s.Complete(ctx, lease, task.Completion{Outcome: task.OutcomeSuccess}, nil)
			default:
				_ = d.Ack()
			}
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (claimed.Load() < 1 || poisoned.Load() < 3) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if claimed.Load() != 1 || poisoned.Load() != 3 {
		t.Fatal(claimed.Load(), poisoned.Load())
	}
	for i := 0; i < 3; i++ {
		if _, ok := p.fetch(t, taskamqp.DeadLetterQueue(p.prefix)); !ok {
			t.Fatalf("poison message %d not dead-lettered", i)
		}
	}
	if r, _ := s.GetRun(ctx, run.ID); r.Status != task.StatusSucceeded {
		t.Fatal(r.Status)
	}
}

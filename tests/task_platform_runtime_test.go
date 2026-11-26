//go:build integration

package tests

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
	taskamqp "github.com/shyun-cc/shw-gf-lib/task/amqp"
)

// setupRuntimeStore 复用 setupPersistent 建表，返回真实时钟的 TypeControl+Outbox Store。
func setupRuntimeStore(t *testing.T) (context.Context, *task.Store) {
	t.Helper()
	ctx, _, _ := setupPersistent(t, task.TimeInstant)
	s, err := task.NewStore(task.StoreConfig{DB: g.DB(), Namespace: "test", TypeControl: true, Outbox: true})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s
}

func runtimePolicy(lease time.Duration, recovery string) task.TypePolicy {
	return task.TypePolicy{Lease: lease, Retry: task.RetryOptions{MaxAttempts: 3, Base: time.Minute, Factor: 1, Max: time.Minute}, Recovery: recovery}
}

func openRuntimeType(t *testing.T, ctx context.Context, s *task.Store, typ string, concurrency int, policy task.TypePolicy) *task.TaskType {
	t.Helper()
	got, err := s.RegisterTypes(ctx, []task.TypeSpec{{Type: typ, HandlerVersion: "v1", Policy: policy}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := got[0].TypeConfig
	cfg.SubmitEnabled, cfg.ConsumeEnabled, cfg.GlobalConcurrency = true, true, concurrency
	v, err := s.ConfigureType(ctx, typ, got[0].Version, cfg, "open-"+typ, nil)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func waitUntil(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timeout: " + msg)
}

func countStatus(ctx context.Context, s *task.Store, status string) int64 {
	c, _ := s.Counts(ctx)
	return c[status]
}

// tracker 记录跨 Runtime 的同时执行峰值。
type tracker struct{ now, peak atomic.Int32 }

func (k *tracker) enter() {
	n := k.now.Add(1)
	for {
		p := k.peak.Load()
		if n <= p || k.peak.CompareAndSwap(p, n) {
			return
		}
	}
}
func (k *tracker) leave() { k.now.Add(-1) }

func startRuntime(t *testing.T, ctx context.Context, cfg task.RuntimeConfig) (context.CancelFunc, <-chan error, *task.Runtime) {
	t.Helper()
	rt, err := task.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- rt.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
		}
	})
	return cancel, done, rt
}

func TestRuntimePullPoolAndBoundedShutdown(t *testing.T) {
	ctx, s := setupRuntimeStore(t)
	openRuntimeType(t, ctx, s, "sync", 10, runtimePolicy(3*time.Second, task.RecoveryRetry))
	openRuntimeType(t, ctx, s, "block", 10, runtimePolicy(3*time.Second, task.RecoveryRetry))
	var k tracker
	handlers := map[string]task.PlatformHandler{
		"sync": {Version: "v1", Run: func(ctx context.Context, _ *task.Execution) (task.Completion, error) {
			k.enter()
			defer k.leave()
			time.Sleep(200 * time.Millisecond)
			return task.Completion{Outcome: task.OutcomeSuccess}, nil
		}},
		"block": {Version: "v1", Run: func(ctx context.Context, _ *task.Execution) (task.Completion, error) {
			<-ctx.Done()
			return task.Completion{}, ctx.Err()
		}},
	}
	// 不配 Transport：Outbox Store 仍可用 DB pull 执行（同一原子领取路径）。
	s2, err := task.NewStore(task.StoreConfig{DB: g.DB(), Namespace: "test", TypeControl: true})
	if err != nil {
		t.Fatal(err)
	}
	cancel, done, rt := startRuntime(t, ctx, task.RuntimeConfig{Store: s2, Handlers: handlers, Concurrency: 2, Poll: 50 * time.Millisecond, Refresh: time.Second, ShutdownGrace: 300 * time.Millisecond, CancelGrace: 3 * time.Second})
	for i := 0; i < 6; i++ {
		if _, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, uuid.NewString(), nil); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, 20*time.Second, "all sync tasks succeed", func() bool { return countStatus(ctx, s, task.StatusSucceeded) == 6 })
	if k.peak.Load() > 2 || k.peak.Load() < 1 {
		t.Fatal("pool cap 2 violated", k.peak.Load())
	}
	blocked, err := s.Submit(ctx, task.Definition{Type: "block", Payload: []byte(`{}`)}, "block", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "block running", func() bool { r, _ := s.GetRun(ctx, blocked.ID); return r.Status == task.StatusRunning })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runtime did not stop within grace")
	}
	r, _ := s.GetRun(ctx, blocked.ID)
	if r.Status != task.StatusFailed || r.Outcome != task.OutcomeInterrupted {
		t.Fatal("bounded shutdown must persist interrupted with retry", r.Status, r.Outcome)
	}
	inst, err := s.GetInstance(ctx, rt.Status().InstanceID)
	if err != nil || inst.StoppedAt == nil {
		t.Fatal("instance must be stopped", inst, err)
	}
}

func TestRuntimeCancelRequestedAndConfigRevision(t *testing.T) {
	ctx, s := setupRuntimeStore(t)
	typ := openRuntimeType(t, ctx, s, "wait", 10, runtimePolicy(1500*time.Millisecond, task.RecoveryRetry))
	plain, err := task.NewStore(task.StoreConfig{DB: g.DB(), Namespace: "test", TypeControl: true})
	if err != nil {
		t.Fatal(err)
	}
	handlers := map[string]task.PlatformHandler{"wait": {Version: "v1", Run: func(ctx context.Context, _ *task.Execution) (task.Completion, error) {
		<-ctx.Done()
		return task.Completion{}, ctx.Err()
	}}}
	_, _, rt := startRuntime(t, ctx, task.RuntimeConfig{Store: plain, Handlers: handlers, Concurrency: 1, Poll: 50 * time.Millisecond, Refresh: 500 * time.Millisecond})
	run, err := s.Submit(ctx, task.Definition{Type: "wait", Payload: []byte(`{}`)}, "wait", nil)
	if err != nil {
		t.Fatal(err)
	}
	var cur *task.Run
	waitUntil(t, 10*time.Second, "wait running", func() bool { cur, _ = s.GetRun(ctx, run.ID); return cur.Status == task.StatusRunning })
	if _, err = s.CancelRun(ctx, run.ID, cur.Version, "cancel-1", nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "cooperative cancel", func() bool { cur, _ = s.GetRun(ctx, run.ID); return cur.Status == task.StatusCancelled })
	// 配置修订下发：变更后 5s 内实例上报已应用修订号。
	cfg := typ.TypeConfig
	cfg.GlobalConcurrency = 5
	if _, err = s.ConfigureType(ctx, "wait", typ.Version, cfg, "grow", nil); err != nil {
		t.Fatal(err)
	}
	rev, _ := s.ConfigRevision(ctx)
	waitUntil(t, 6*time.Second, "applied revision", func() bool {
		inst, err := s.GetInstance(ctx, rt.Status().InstanceID)
		return err == nil && inst.AppliedRevision == rev && rt.Status().AppliedRevision == rev
	})
	// 未开始的运行立即取消（先暂停消费，保证不被领取）。
	sw, _ := s.GetType(ctx, "wait")
	if _, err = s.SetTypeSwitches(ctx, "wait", sw.Version, true, false, "pause", nil); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Submit(ctx, task.Definition{Type: "wait", Payload: []byte(`{}`)}, "later", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.CancelRun(ctx, pending.ID, pending.Version, "cancel-2", nil); err != nil || got.Status != task.StatusCancelled {
		t.Fatal(got, err)
	}
	if again, err := s.CancelRun(ctx, pending.ID, pending.Version, "cancel-2", nil); err != nil || again.Status != task.StatusCancelled {
		t.Fatal("idempotent cancel", again, err)
	}
}

func TestRuntimeAmqpGlobalConcurrencyAcrossInstances(t *testing.T) {
	amqpCfg := amqpTestConfig(t)
	ctx, s := setupRuntimeStore(t)
	openRuntimeType(t, ctx, s, "sync", 3, runtimePolicy(3*time.Second, task.RecoveryRetry))
	prefix := "gflib-rt-" + uuid.NewString()[:8]
	var k tracker
	handlers := map[string]task.PlatformHandler{"sync": {Version: "v1", Run: func(ctx context.Context, _ *task.Execution) (task.Completion, error) {
		k.enter()
		defer k.leave()
		time.Sleep(300 * time.Millisecond)
		return task.Completion{Outcome: task.OutcomeSuccess}, nil
	}}}
	admin := openAmqpAdmin(t)
	t.Cleanup(func() {
		ch, err := admin.Channel()
		if err != nil {
			return
		}
		defer ch.Close()
		_, _ = ch.QueueDelete(taskamqp.QueueName(prefix, task.DefaultClass), false, false, false)
		_, _ = ch.QueueDelete(taskamqp.DeadLetterQueue(prefix), false, false, false)
		_ = ch.ExchangeDelete(taskamqp.ExchangeName(prefix), false, false)
		_ = ch.ExchangeDelete(taskamqp.DeadLetterExchange(prefix), false, false)
	})
	for i := 0; i < 2; i++ {
		client := rabbitmq.NewClient(amqpCfg)
		if err := client.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		if err := client.Declare(ctx, taskamqp.PlatformTopology(prefix, []string{task.DefaultClass}, false)); err != nil {
			t.Fatal(err)
		}
		startRuntime(t, ctx, task.RuntimeConfig{Store: s, Transport: taskamqp.NewTransport(client, prefix), Handlers: handlers, Concurrency: 4, Poll: 100 * time.Millisecond, Refresh: time.Second})
	}
	for i := 0; i < 12; i++ {
		if _, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, uuid.NewString(), nil); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, 60*time.Second, "all tasks via RabbitMQ succeed", func() bool { return countStatus(ctx, s, task.StatusSucceeded) == 12 })
	if p := k.peak.Load(); p > 3 || p < 1 {
		t.Fatal("type global concurrency 3 violated across instances", p)
	}
	if n, _ := g.DB().GetCount(ctx, `SELECT 1 FROM task_attempt`); n != 12 {
		t.Fatal("each task must execute exactly once", n)
	}
}

func TestRuntimeRefusesToStartWithoutConfig(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	db, err := gdb.New(gdb.ConfigNode{Type: "pgsql", Host: "127.0.0.1", Port: "1", User: "none", Pass: "none", Name: "none"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := task.NewStore(task.StoreConfig{DB: db, Namespace: "test", TypeControl: true})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := task.NewRuntime(task.RuntimeConfig{Store: s, Handlers: map[string]task.PlatformHandler{"x": {Version: "v1", Run: func(context.Context, *task.Execution) (task.Completion, error) { return task.Completion{}, nil }}}, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = rt.Run(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("runtime must refuse to start when config is unreadable", err)
	}
}

//go:build integration

package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gtrace"
	"github.com/google/uuid"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
	taskamqp "github.com/shyun-cc/shw-gf-lib/task/amqp"
)

// traceSeen 记录 Handler/Splitter 内看到的 trace，按 attempt 比对。
type traceSeen struct {
	mu   sync.Mutex
	seen map[string][2]string // taskID -> {gtrace, AttemptTraceID}
}

func (s *traceSeen) record(ctx context.Context, taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[taskID] = [2]string{gtrace.GetTraceID(ctx), task.AttemptTraceID(ctx)}
}

// assertAttemptTrace 每个已执行运行的最新 attempt trace 必须非空，且与 Handler 内 gtrace / AttemptTraceID 一致。
func assertAttemptTrace(t *testing.T, ctx context.Context, s *task.Store, seen *traceSeen, ids []string) {
	t.Helper()
	seen.mu.Lock()
	defer seen.mu.Unlock()
	for _, id := range ids {
		attempts, err := s.Attempts(ctx, id, task.Page{Limit: 1})
		if err != nil || len(attempts) != 1 {
			t.Fatal(id, attempts, err)
		}
		got, ok := seen.seen[id]
		if !ok {
			t.Fatal("handler never saw task", id)
		}
		if attempts[0].TraceID == "" || got[0] != attempts[0].TraceID || got[1] != attempts[0].TraceID {
			t.Fatalf("task %s: attempt trace %q, gtrace %q, AttemptTraceID %q", id, attempts[0].TraceID, got[0], got[1])
		}
	}
}

func TestRuntimeAttemptTracePullAndSplitter(t *testing.T) {
	ctx, s := setupRuntimeStore(t)
	openRuntimeType(t, ctx, s, "parent", 10, runtimePolicy(3*time.Second, task.RecoveryRetry))
	openRuntimeType(t, ctx, s, "child", 10, runtimePolicy(3*time.Second, task.RecoveryRetry))
	seen := &traceSeen{seen: map[string][2]string{}}
	handlers := map[string]task.PlatformHandler{
		"parent": {Version: "v1", Splitter: func(ctx context.Context, parent *task.Run, _ []byte, _ int) (task.SplitBatch, error) {
			seen.record(ctx, parent.ID)
			return task.SplitBatch{Done: true, Children: []task.ChildSpec{{Key: "a", Type: "child"}, {Key: "b", Type: "child"}}}, nil
		}},
		"child": {Version: "v1", Run: func(ctx context.Context, e *task.Execution) (task.Completion, error) {
			seen.record(ctx, e.Lease.Run.ID)
			g.Log().Info(ctx, "child executed") // 业务日志与 attempt trace 同值
			return task.Completion{Outcome: task.OutcomeSuccess}, nil
		}},
	}
	plain, err := task.NewStore(task.StoreConfig{DB: g.DB(), Namespace: "test", TypeControl: true})
	if err != nil {
		t.Fatal(err)
	}
	// 不配置 TraceID 回调：每次 attempt 也必须生成独立 trace，不再记录为空。
	startRuntime(t, ctx, task.RuntimeConfig{Store: plain, Handlers: handlers, Concurrency: 2, Poll: 50 * time.Millisecond})
	parent, err := s.Submit(ctx, task.Definition{Type: "parent", Payload: []byte(`{}`)}, "trace-parent", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 20*time.Second, "parent aggregated", func() bool { r, _ := s.GetRun(ctx, parent.ID); return r.Status == task.StatusSucceeded })
	children, err := s.Children(ctx, parent.ID, task.Page{})
	if err != nil || len(children) != 2 {
		t.Fatal(children, err)
	}
	ids := []string{parent.ID, children[0].ID, children[1].ID}
	assertAttemptTrace(t, ctx, s, seen, ids)
	traces := map[string]bool{}
	for _, id := range ids {
		a, _ := s.Attempts(ctx, id, task.Page{Limit: 1})
		traces[a[0].TraceID] = true
	}
	if len(traces) != 3 {
		t.Fatal("each attempt must have its own trace, not an instance-wide one", traces)
	}
}

func TestRuntimeAttemptTraceViaRabbitMQWithCallback(t *testing.T) {
	amqpCfg := amqpTestConfig(t)
	ctx, s := setupRuntimeStore(t)
	openRuntimeType(t, ctx, s, "sync", 10, runtimePolicy(3*time.Second, task.RecoveryRetry))
	prefix := "gflib-trace-" + uuid.NewString()[:8]
	client := rabbitmq.NewClient(amqpCfg)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if err := client.Declare(ctx, taskamqp.PlatformTopology(prefix, []string{task.DefaultClass}, false)); err != nil {
		t.Fatal(err)
	}
	admin := openAmqpAdmin(t)
	t.Cleanup(func() {
		if ch, err := admin.Channel(); err == nil {
			_, _ = ch.QueueDelete(taskamqp.QueueName(prefix, task.DefaultClass), false, false, false)
			_, _ = ch.QueueDelete(taskamqp.DeadLetterQueue(prefix), false, false, false)
			_ = ch.ExchangeDelete(taskamqp.ExchangeName(prefix), false, false)
			_ = ch.ExchangeDelete(taskamqp.DeadLetterExchange(prefix), false, false)
			_ = ch.Close()
		}
	})
	seen := &traceSeen{seen: map[string][2]string{}}
	var mu sync.Mutex
	generated := map[string]bool{}
	handlers := map[string]task.PlatformHandler{"sync": {Version: "v1", Run: func(ctx context.Context, e *task.Execution) (task.Completion, error) {
		seen.record(ctx, e.Lease.Run.ID)
		return task.Completion{Outcome: task.OutcomeSuccess}, nil
	}}}
	startRuntime(t, ctx, task.RuntimeConfig{Store: s, Transport: taskamqp.NewTransport(client, prefix), Handlers: handlers, Concurrency: 2, Poll: 100 * time.Millisecond,
		TraceID: func(context.Context) string {
			id := uuid.NewString()
			id = id[0:8] + id[9:13] + id[14:18] + id[19:23] + id[24:]
			mu.Lock()
			generated[id] = true
			mu.Unlock()
			return id
		}})
	var ids []string
	for i := 0; i < 3; i++ {
		r, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, uuid.NewString(), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	waitUntil(t, 30*time.Second, "all via RabbitMQ", func() bool { return countStatus(ctx, s, task.StatusSucceeded) == 3 })
	assertAttemptTrace(t, ctx, s, seen, ids)
	for _, id := range ids {
		a, _ := s.Attempts(ctx, id, task.Page{Limit: 1})
		mu.Lock()
		ok := generated[a[0].TraceID]
		mu.Unlock()
		if !ok {
			t.Fatal("attempt trace must come from the TraceID callback", a[0].TraceID)
		}
	}
}

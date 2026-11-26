//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/task"
)

func claimByID(t *testing.T, ctx context.Context, s *task.Store, instance, id string) *task.Lease {
	t.Helper()
	l, err := s.Claim(ctx, instance, id, "")
	if err != nil || l == nil {
		t.Fatal("claim", id, l, err)
	}
	return l
}

// childByKey 返回某逻辑子任务的最新运行（重放优先）。
func childByKey(t *testing.T, ctx context.Context, s *task.Store, parent, key string) *task.Run {
	t.Helper()
	row, err := g.DB().GetOne(ctx, `SELECT id FROM task WHERE parent_id=? AND child_key=? ORDER BY created_at DESC,id DESC LIMIT 1`, parent, key)
	if err != nil || row.IsEmpty() {
		t.Fatal("child not found", key, err)
	}
	r, err := s.GetRun(ctx, row["id"].String())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSplitResumeNoDuplicatesAndAggregateOnce(t *testing.T) {
	ctx, s, _, clock := setupPlatform(t, withOutbox)
	openType(t, ctx, s, "parent", 10, nil)
	openType(t, ctx, s, "child", 10, func(c *task.TypeConfig) { c.Policy.Recovery = task.RecoveryReview })
	instance := platformInstance(t, ctx, s, 10, map[string]string{"parent": "v1", "child": "v1"})
	parent, err := s.Submit(ctx, task.Definition{Type: "parent", Payload: []byte(`{}`)}, "p1", nil)
	if err != nil {
		t.Fatal(err)
	}
	l := claimByID(t, ctx, s, instance, parent.ID)
	if cur, err := s.SplitCursor(ctx, l); err != nil || cur != nil {
		t.Fatal(cur, err)
	}
	batch := func(cursor string, done bool, keys ...string) task.SplitBatch {
		b := task.SplitBatch{Cursor: []byte(cursor), Done: done}
		for _, k := range keys {
			b.Children = append(b.Children, task.ChildSpec{Key: k, Type: "child", Payload: []byte(`{"k":"` + k + `"}`)})
		}
		return b
	}
	if n, err := s.AppendSplit(ctx, l, batch(`{"after":"b"}`, false, "a", "b"), nil); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if v, err := s.SplitProgress(ctx, parent.ID); err != nil || v.Discovered != 2 || v.Total != nil || v.SplitCompleted {
		t.Fatal("unknown total must report discovered only", v, err)
	}
	// 拆分进程崩溃：租约过期 → 恢复重试，游标保留。
	clock.Add(int64(61 * time.Second))
	if _, _, err = s.Recover(ctx, 100); err != nil {
		t.Fatal(err)
	}
	clock.Add(int64(2 * time.Second))
	if _, n, err := s.Recover(ctx, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = s.HeartbeatInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	l = claimByID(t, ctx, s, instance, parent.ID)
	if cur, err := s.SplitCursor(ctx, l); err != nil || string(cur) != `{"after": "b"}` && string(cur) != `{"after":"b"}` {
		t.Fatal("cursor must survive crash", string(cur), err)
	}
	// 重复发送已生成的键只插入新键。
	if n, err := s.AppendSplit(ctx, l, batch(`{"after":"c"}`, false, "a", "b", "c"), nil); err != nil || n != 1 {
		t.Fatal("re-split must not duplicate children", n, err)
	}
	if n, err := s.AppendSplit(ctx, l, batch(`{"after":"d"}`, true, "d"), nil); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err = s.FinishSplit(ctx, l); err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetRun(ctx, parent.ID)
	if p.Status != task.StatusWaitingChildren {
		t.Fatal(p.Status)
	}
	v, _ := s.SplitProgress(ctx, parent.ID)
	if v.Total == nil || *v.Total != 4 || v.Pending != 4 {
		t.Fatal(v)
	}
	if n, _ := g.DB().GetCount(ctx, `SELECT 1 FROM task WHERE parent_id=?`, parent.ID); n != 4 {
		t.Fatal("children", n)
	}
	// 部分子任务先完成：父任务不提前完成。
	for _, k := range []string{"a", "b"} {
		cl := claimByID(t, ctx, s, instance, childByKey(t, ctx, s, parent.ID, k).ID)
		if err = s.Complete(ctx, cl, task.Completion{Outcome: task.OutcomeSuccess}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if p, _ = s.GetRun(ctx, parent.ID); p.Status != task.StatusWaitingChildren {
		t.Fatal("parent closed early", p.Status)
	}
	cl := claimByID(t, ctx, s, instance, childByKey(t, ctx, s, parent.ID, "c").ID)
	if err = s.Complete(ctx, cl, task.Completion{Outcome: task.OutcomePermanentFailure}, nil); err != nil {
		t.Fatal(err)
	}
	cl = claimByID(t, ctx, s, instance, childByKey(t, ctx, s, parent.ID, "d").ID)
	if err = s.Complete(ctx, cl, task.Completion{Outcome: task.OutcomeUnknown}, nil); err != nil {
		t.Fatal(err)
	}
	p, _ = s.GetRun(ctx, parent.ID)
	if p.Status != task.StatusSucceeded || p.Outcome != task.OutcomePartial {
		t.Fatal(p.Status, p.Outcome)
	}
	var summary task.Completion
	if err = json.Unmarshal(p.Summary, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Stats["total"] != 4 || summary.Stats["succeeded"] != 2 || summary.Stats["abnormal"] != 2 {
		t.Fatal(summary.Stats)
	}
	v, err = s.SplitProgress(ctx, parent.ID)
	if err != nil || v.Abnormal != 2 || v.NeedsReview != 0 || v.Pending != 0 {
		t.Fatal(v, err)
	}
	if n, _ := g.DB().GetCount(ctx, `SELECT 1 FROM task WHERE parent_id=?`, parent.ID); n != 4 {
		t.Fatal("no manual replay required", n)
	}

}

func TestSplitCancelTreeScope(t *testing.T) {
	ctx, s, _, _ := setupPlatform(t, withOutbox)
	openType(t, ctx, s, "parent", 10, nil)
	openType(t, ctx, s, "child", 10, nil)
	instance := platformInstance(t, ctx, s, 10, map[string]string{"parent": "v1", "child": "v1"})
	mk := func(key string) *task.Run {
		parent, err := s.Submit(ctx, task.Definition{Type: "parent", Payload: []byte(`{}`)}, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		l := claimByID(t, ctx, s, instance, parent.ID)
		if _, err = s.AppendSplit(ctx, l, task.SplitBatch{Done: true, Children: []task.ChildSpec{{Key: "x", Type: "child"}, {Key: "y", Type: "child"}}}, nil); err != nil {
			t.Fatal(err)
		}
		if err = s.FinishSplit(ctx, l); err != nil {
			t.Fatal(err)
		}
		p, _ := s.GetRun(ctx, parent.ID)
		return p
	}
	keep := mk("keep")
	if got, err := s.CancelTree(ctx, keep.ID, keep.Version, false, "cancel-keep", nil); err != nil || got.Status != task.StatusCancelled {
		t.Fatal(got, err)
	}
	if n, _ := g.DB().GetCount(ctx, `SELECT 1 FROM task WHERE parent_id=? AND status='pending'`, keep.ID); n != 2 {
		t.Fatal("children must survive parent-only cancel", n)
	}
	all := mk("all")
	running := claimByID(t, ctx, s, instance, childByKey(t, ctx, s, all.ID, "x").ID)
	if _, err := s.CancelTree(ctx, all.ID, all.Version, true, "cancel-all", nil); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetRun(ctx, childByKey(t, ctx, s, all.ID, "y").ID); r.Status != task.StatusCancelled {
		t.Fatal("pending child must be cancelled", r.Status)
	}
	if err := s.Renew(ctx, running); err != task.ErrCancelRequested {
		t.Fatal("running child must be asked to cancel cooperatively", err)
	}
}

func TestRuntimeSplitContinuationAndAggregate(t *testing.T) {
	ctx, s := setupRuntimeStore(t)
	openRuntimeType(t, ctx, s, "parent", 10, runtimePolicy(3*time.Second, task.RecoveryRetry))
	openRuntimeType(t, ctx, s, "child", 10, runtimePolicy(3*time.Second, task.RecoveryRetry))
	const total = 25
	handlers := map[string]task.PlatformHandler{
		"parent": {Version: "v1", SplitBatchSize: 5, SplitBudget: 2, Splitter: func(ctx context.Context, _ *task.Run, cursor []byte, limit int) (task.SplitBatch, error) {
			next := 0
			if cursor != nil {
				var c struct{ Next int }
				if err := json.Unmarshal(cursor, &c); err != nil {
					return task.SplitBatch{}, err
				}
				next = c.Next
			}
			var b task.SplitBatch
			for i := next; i < total && len(b.Children) < limit; i++ {
				b.Children = append(b.Children, task.ChildSpec{Key: "item-" + strconv.Itoa(i), Type: "child"})
				next = i + 1
			}
			b.Cursor = []byte(fmt.Sprintf(`{"Next":%d}`, next))
			b.Done = next >= total
			return b, nil
		}},
		"child": {Version: "v1", Run: func(context.Context, *task.Execution) (task.Completion, error) {
			return task.Completion{Outcome: task.OutcomeSuccess}, nil
		}},
	}
	plain, err := task.NewStore(task.StoreConfig{DB: g.DB(), Namespace: "test", TypeControl: true})
	if err != nil {
		t.Fatal(err)
	}
	startRuntime(t, ctx, task.RuntimeConfig{Store: plain, Handlers: handlers, Concurrency: 4, Poll: 50 * time.Millisecond})
	parent, err := s.Submit(ctx, task.Definition{Type: "parent", Payload: []byte(`{}`)}, "big", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 30*time.Second, "parent aggregated", func() bool { r, _ := s.GetRun(ctx, parent.ID); return r.Status == task.StatusSucceeded })
	v, err := s.SplitProgress(ctx, parent.ID)
	if err != nil || v.Total == nil || *v.Total != total || v.Succeeded != total || v.Continuations < 2 {
		t.Fatal(v, err)
	}
	if n, _ := g.DB().GetCount(ctx, `SELECT 1 FROM task_attempt a JOIN task c ON c.id=a.task_id WHERE c.parent_id=?`, parent.ID); n != total {
		t.Fatal("each child executes exactly once", n)
	}
	if r, _ := s.GetRun(ctx, parent.ID); r.Outcome != task.OutcomeSuccess || r.Attempts != v.Continuations+1 {
		t.Fatal(r.Outcome, r.Attempts, v.Continuations)
	}
}

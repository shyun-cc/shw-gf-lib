//go:build integration

package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/task"
)

func TestOrderingGateRetryAbnormalAndSkip(t *testing.T) {
	ctx, s, _, clock := setupPlatform(t, withOutbox)
	openType(t, ctx, s, "dev", 10, nil)
	instance := platformInstance(t, ctx, s, 10, map[string]string{"dev": "v1"})
	submit := func(key, ordering string, prio int) *task.Run {
		r, err := s.Submit(ctx, task.Definition{Type: "dev", Payload: []byte(`{"k":"` + key + `"}`), OrderingKey: ordering, Priority: prio}, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a1, a2, a3 := submit("a1", "A", 0), submit("a2", "A", 0), submit("a3", "A", 100)
	b1 := submit("b1", "B", 0)
	if a1.OrderSeq != 1 || a2.OrderSeq != 2 || a3.OrderSeq != 3 || b1.OrderSeq != 1 {
		t.Fatal(a1.OrderSeq, a2.OrderSeq, a3.OrderSeq, b1.OrderSeq)
	}
	// 投递器只投递各键队首：a1 与 b1。
	items, err := s.ClaimOutbox(ctx, "relay", 10, 30*time.Second)
	if err != nil || len(items) != 2 {
		t.Fatal("blocked runs must not be published", items, err)
	}
	got := map[string]*task.Lease{}
	for i := 0; i < 3; i++ {
		l, err := s.Claim(ctx, instance, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if l != nil {
			got[l.Run.ID] = l
		}
	}
	// 高优先级 a3 不越过同键顺序；不同键并发。
	if len(got) != 2 || got[a1.ID] == nil || got[b1.ID] == nil {
		t.Fatal("different keys run concurrently, same key strictly ordered", len(got))
	}
	if err = s.Complete(ctx, got[a1.ID], task.Completion{Outcome: task.OutcomeRetryableFailure}, nil); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.Claim(ctx, instance, a2.ID, ""); l != nil {
		t.Fatal("retry-waiting predecessor must block successor")
	}
	clock.Add(int64(2 * time.Second))
	if _, _, err = s.Recover(ctx, 100); err != nil {
		t.Fatal(err)
	}
	l := claimByID(t, ctx, s, instance, a1.ID)
	if err = s.Complete(ctx, l, task.Completion{Outcome: task.OutcomeUnknown}, nil); err != nil {
		t.Fatal(err)
	}
	cur, _ := s.GetRun(ctx, a1.ID)
	if cur.Status != task.StatusAbnormal {
		t.Fatal(cur.Status)
	}
	// 异常自动放行；人工重试作为新序号，不能插队到已推进位置。
	replay, err := s.Retry(ctx, a1.ID, cur.Version, "retry-a1", nil)
	if err != nil || replay.OrderSeq != 4 {
		t.Fatal(replay, err)
	}
	l = claimByID(t, ctx, s, instance, a2.ID)
	if err = s.Complete(ctx, l, task.Completion{Outcome: task.OutcomePermanentFailure}, nil); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("audit down")
	if _, err = s.SkipOrder(ctx, replay.ID, replay.Version, "skip-retry", func(context.Context, gdb.TX) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	skipped, err := s.SkipOrder(ctx, replay.ID, replay.Version, "skip-retry", nil)
	if err != nil || skipped.Status != task.StatusCancelled || skipped.Outcome != task.OutcomeSkipped {
		t.Fatal(skipped, err)
	}

	l = claimByID(t, ctx, s, instance, a3.ID)
	if _, err = s.SkipOrder(ctx, a3.ID, l.Run.Version, "skip-running", nil); !errors.Is(err, task.ErrConflict) {
		t.Fatal("running run cannot be skipped", err)
	}
}

func TestOrderingHolesAndRollback(t *testing.T) {
	ctx, s, _, _ := setupPlatform(t)
	openType(t, ctx, s, "dev", 10, nil)
	instance := platformInstance(t, ctx, s, 10, map[string]string{"dev": "v1"})
	c1, err := s.Submit(ctx, task.Definition{Type: "dev", Payload: []byte(`{}`), OrderingKey: "C"}, "c1", nil)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.Submit(ctx, task.Definition{Type: "dev", Payload: []byte(`{}`), OrderingKey: "C"}, "c2", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 回滚的创建不消耗序号。
	boom := errors.New("rollback")
	if _, err = s.Submit(ctx, task.Definition{Type: "dev", Payload: []byte(`{}`), OrderingKey: "C"}, "c-rolled", func(context.Context, gdb.TX) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	c3, err := s.Submit(ctx, task.Definition{Type: "dev", Payload: []byte(`{}`), OrderingKey: "C"}, "c3", nil)
	if err != nil || c3.OrderSeq != 3 {
		t.Fatal("rolled back creation must not leave a hole", c3, err)
	}
	// 前项取消后被清理删除（序号空洞）：后项不永久等待。
	if _, err = s.CancelRun(ctx, c1.ID, c1.Version, "cancel-c1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = g.DB().Exec(ctx, `DELETE FROM task_command WHERE target_id=?`, c1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = g.DB().Exec(ctx, `DELETE FROM task WHERE id=?`, c1.ID); err != nil {
		t.Fatal(err)
	}
	if l := claimByID(t, ctx, s, instance, c2.ID); l == nil {
		t.Fatal("hole must not block")
	}
}

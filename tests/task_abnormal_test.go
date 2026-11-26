//go:build integration

package tests

import (
	"context"
	"errors"
	"github.com/gogf/gf/v2/database/gdb"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/shyun-cc/shw-gf-lib/task"
)

// 复现 #131：周期执行租约丢失后，历史 needs_review 保留互斥导致所有后续轮次停摆。
func TestHistoricalReviewRecoveryUnblocksSchedule(t *testing.T) {
	for _, mode := range []string{task.TimeInstant, task.TimeWall} {
		t.Run(mode, func(t *testing.T) {
			ctx, s, clock := setupPersistent(t, mode)
			p := runtimePlan(t, ctx, s, clock)
			def := p.Definition
			def.ConcurrencyKey = "sync"
			def.Recovery = task.RecoveryReconcile
			p.Definition = def
			if err := s.Configure(ctx, p.ID, p.Version, *p, "policy", nil); err != nil {
				t.Fatal(err)
			}
			runs, err := s.Tick(ctx, 10, nil)
			if err != nil || len(runs) != 1 {
				t.Fatal(runs, err)
			}
			instance := runtimeInstance(t, ctx, s)
			l := claimByID(t, ctx, s, instance, runs[0].ID)
			// 模拟旧版本已经结算、保留错误与 attempt trace 的异常运行。
			if err = s.Complete(ctx, l, task.Completion{Outcome: task.OutcomeUnknown, ErrorCode: "lease_expired", ErrorSummary: "execution lease or deadline expired", BusinessRef: "sync-target"}, nil); err != nil {
				t.Fatal(err)
			}
			if _, err = g.DB().Exec(ctx, `UPDATE task SET status='needs_review' WHERE id=?`, l.Run.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = g.DB().Exec(ctx, `INSERT INTO task_concurrency(namespace,key,active_task_id) VALUES('test','sync',?)`, l.Run.ID); err != nil {
				t.Fatal(err)
			}
			clock.Add(int64(time.Hour))
			if runs, err = s.Tick(ctx, 10, nil); err != nil || len(runs) != 0 {
				t.Fatal("legacy lock must be converted atomically before scheduling", runs, err)
			}
			old, err := s.GetRun(ctx, l.Run.ID)
			if err != nil || old.Status != task.StatusAbnormal {
				t.Fatal(old, err)
			}
			for _, filter := range []string{task.StatusAbnormal, task.StatusNeedsReview} {
				rows, err := s.Runs(ctx, p.ID, filter, task.Page{})
				if err != nil || len(rows) != 1 {
					t.Fatal(filter, rows, err)
				}
			}
			counts, err := s.Counts(ctx)
			if err != nil || counts[task.StatusAbnormal] != 1 || counts[task.StatusNeedsReview] != 0 {
				t.Fatal(counts, err)
			}
			if n, _, err := s.Recover(ctx, 1); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			if n, _, err := s.Recover(ctx, 1); err != nil || n != 0 {
				t.Fatal("recovery must be idempotent", n, err)
			}
			cur, err := s.GetRun(ctx, l.Run.ID)
			if err != nil || cur.Status != task.StatusAbnormal || cur.Outcome != task.OutcomeUnknown || cur.ErrorCode != old.ErrorCode || cur.LastError != old.LastError || cur.Attempts != old.Attempts || string(cur.Summary) != string(old.Summary) {
				t.Fatal("history changed", cur, err)
			}
			attempts, err := s.Attempts(ctx, cur.ID, task.Page{})
			if err != nil || len(attempts) != 1 || attempts[0].Error != old.LastError {
				t.Fatal(attempts, err)
			}
			if _, err = s.CancelRun(ctx, cur.ID, cur.Version, "cancel-abnormal", nil); !errors.Is(err, task.ErrConflict) {
				t.Fatal("abnormal is immutable", err)
			}
			if err = s.Complete(ctx, l, task.Completion{Outcome: task.OutcomeSuccess}, nil); !errors.Is(err, task.ErrLeaseLost) {
				t.Fatal("stale write accepted", err)
			}
			runs, err = s.Tick(ctx, 10, nil)
			if err != nil || len(runs) != 1 {
				t.Fatal("next scheduled round must proceed", runs, err)
			}
			if err = s.HeartbeatInstance(ctx, instance); err != nil {
				t.Fatal(err)
			}
			next := claimByID(t, ctx, s, instance, runs[0].ID)
			if err = s.Complete(ctx, next, task.Completion{Outcome: task.OutcomeSuccess}, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAbnormalRetryKeepsHistoryAndReaggregatesParent(t *testing.T) {
	ctx, s, _, _ := setupPlatform(t, withOutbox)
	openType(t, ctx, s, "parent", 4, nil)
	openType(t, ctx, s, "child", 4, nil)
	instance := platformInstance(t, ctx, s, 4, map[string]string{"parent": "v1", "child": "v1"})
	parent, err := s.Submit(ctx, task.Definition{Type: "parent"}, "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	l := claimByID(t, ctx, s, instance, parent.ID)
	if _, err = s.AppendSplit(ctx, l, task.SplitBatch{Done: true, Children: []task.ChildSpec{{Key: "one", Type: "child"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishSplit(ctx, l); err != nil {
		t.Fatal(err)
	}
	child := childByKey(t, ctx, s, parent.ID, "one")
	l = claimByID(t, ctx, s, instance, child.ID)
	if err = s.Complete(ctx, l, task.Completion{Outcome: task.OutcomeUnknown, ErrorCode: "unknown_side_effect", ErrorSummary: "external result unknown"}, nil); err != nil {
		t.Fatal(err)
	}
	original, _ := s.GetRun(ctx, child.ID)
	p, _ := s.GetRun(ctx, parent.ID)
	if p.Status != task.StatusAbnormal {
		t.Fatal("parent must not wait for review", p.Status)
	}
	boom := errors.New("audit unavailable")
	if _, err = s.Retry(ctx, original.ID, original.Version, "retry", func(context.Context, gdb.TX) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	afterRollback, _ := s.GetRun(ctx, original.ID)
	if afterRollback.Version != original.Version {
		t.Fatal("retry CAS must roll back with audit", afterRollback.Version)
	}
	retry, err := s.Retry(ctx, original.ID, original.Version, "retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := s.Retry(ctx, original.ID, original.Version, "retry", nil)
	if err != nil || repeat.ID != retry.ID {
		t.Fatal("idempotent retry", repeat, err)
	}
	if _, err = s.Retry(ctx, original.ID, original.Version, "duplicate", nil); !errors.Is(err, task.ErrConflict) {
		t.Fatal("same version cannot spawn twice", err)
	}
	saved, _ := s.GetRun(ctx, original.ID)
	if saved.Status != original.Status || saved.ErrorCode != original.ErrorCode || saved.LastError != original.LastError || string(saved.Summary) != string(original.Summary) {
		t.Fatal("retry overwrote history", saved)
	}
	l = claimByID(t, ctx, s, instance, retry.ID)
	if err = s.Complete(ctx, l, task.Completion{Outcome: task.OutcomeSuccess}, nil); err != nil {
		t.Fatal(err)
	}
	p, _ = s.GetRun(ctx, parent.ID)
	view, err := s.SplitProgress(ctx, parent.ID)
	if err != nil || p.Status != task.StatusSucceeded || view.Succeeded != 1 || view.Abnormal != 0 || view.Discovered != 1 {
		t.Fatal("latest logical child must count once", p, view, err)
	}
}

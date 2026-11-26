//go:build integration

package tests

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/shyun-cc/shw-gf-lib/task"
)

func setupPersistent(t *testing.T, mode string) (context.Context, *task.Store, *atomic.Int64) {
	t.Helper()
	ctx, _, oldCleanup := setupTaskPG(t)
	if _, err := g.DB().Exec(ctx, `DROP TABLE task_attempt,task`); err != nil {
		t.Fatal(err)
	}
	ddl, err := task.Schema(mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.DB().Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = g.DB().Exec(ctx, `DROP TABLE IF EXISTS `+platformTables)
		oldCleanup()
	})
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	loc, _ := time.LoadLocation("Asia/Shanghai")
	store, err := task.NewStore(task.StoreConfig{DB: g.DB(), Namespace: "test", TimeMode: mode, Location: loc, Now: func() time.Time { return time.Unix(0, clock.Load()).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, store, clock
}

// platformTables 为 Schema 额外创建、需在用例结束时删除的表（task/task_attempt 由 setupTaskPG 清理）。
const platformTables = `task_order,task_split,task_outbox,task_concurrency,task_command,task_schedule,task_instance,task_type,task_config_revision`

func runtimeDef() task.Definition {
	return task.Definition{Type: "sync", HandlerVersion: "v1", Lease: time.Minute, Retry: task.RetryOptions{MaxAttempts: 3, Base: time.Second, Factor: 2, Max: time.Minute}, Recovery: task.RecoveryRetry, Payload: []byte(`{"hello":1}`)}
}
func runtimeInstance(t *testing.T, ctx context.Context, s *task.Store) string {
	t.Helper()
	v, e := s.RegisterInstance(ctx, task.Instance{Role: "executor", BuildVersion: "test", Capacity: 4, Capabilities: map[string]string{"sync": "v1"}})
	if e != nil {
		t.Fatal(e)
	}
	return v.ID
}
func runtimePlan(t *testing.T, ctx context.Context, s *task.Store, clock *atomic.Int64) *task.Schedule {
	t.Helper()
	p, e := s.RegisterSchedule(ctx, task.Schedule{Name: "hourly", Enabled: true, DefinitionVersion: 1, Definition: runtimeDef(), Kind: "interval", Interval: time.Hour, Timezone: "Asia/Shanghai", Anchor: time.Unix(0, clock.Load()), Misfire: task.MisfireCoalesce, MaxCatchUp: 2})
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestPersistentPGAtomicScheduleAndLease(t *testing.T) {
	for _, mode := range []string{task.TimeInstant, task.TimeWall} {
		t.Run(mode, func(t *testing.T) {
			ctx, s, clock := setupPersistent(t, mode)
			p := runtimePlan(t, ctx, s, clock)
			instance := runtimeInstance(t, ctx, s)
			// Dispatch transaction rollback must roll back task AND cursor.
			rollback := errors.New("injected transaction failure")
			if _, e := s.Tick(ctx, 10, func(context.Context, gdb.TX) error { return rollback }); !errors.Is(e, rollback) {
				t.Fatal(e)
			}
			runs, e := s.Runs(ctx, "", "", task.Page{})
			if e != nil || len(runs) != 0 {
				t.Fatal(runs, e)
			}
			var wg sync.WaitGroup
			var dispatched atomic.Int32
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, e := s.Tick(ctx, 10, nil)
					if e != nil {
						t.Error(e)
						return
					}
					dispatched.Add(int32(len(r)))
				}()
			}
			wg.Wait()
			if dispatched.Load() != 1 {
				t.Fatal(dispatched.Load())
			}
			runs, e = s.Runs(ctx, p.ID, "", task.Page{})
			if e != nil || len(runs) != 1 {
				t.Fatal(runs, e)
			}
			id := runs[0].ID
			var claims atomic.Int32
			var lease *task.Lease
			var mu sync.Mutex
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					wanted := ""
					if i%2 == 0 {
						wanted = id
					}
					l, e := s.Claim(ctx, instance, wanted, "")
					if e != nil {
						t.Error(e)
						return
					}
					if l != nil {
						claims.Add(1)
						mu.Lock()
						lease = l
						mu.Unlock()
					}
				}(i)
			}
			wg.Wait()
			if claims.Load() != 1 {
				t.Fatal(claims.Load())
			}
			attempts, e := s.Attempts(ctx, id, task.Page{})
			if e != nil || len(attempts) != 1 {
				t.Fatal(attempts, e)
			}
			if e = s.Progress(ctx, lease, 20, []byte(`{"after":"a"}`), nil); e != nil {
				t.Fatal(e)
			}
			clock.Add(int64(20 * time.Second))
			if e = s.Renew(ctx, lease); e != nil {
				t.Fatal(e)
			}
			// All three operations share ownership CAS: completed execution cannot be recovered.
			if e = s.Complete(ctx, lease, task.Completion{Outcome: "partial", Summary: []byte(`{"succeeded":2,"failed":1}`)}, nil); e != nil {
				t.Fatal(e)
			}
			clock.Add(int64(2 * time.Minute))
			if n, _, e := s.Recover(ctx, 100); e != nil || n != 0 {
				t.Fatal(n, e)
			}
			if e = s.Complete(ctx, lease, task.Completion{Outcome: "success"}, nil); !errors.Is(e, task.ErrLeaseLost) {
				t.Fatal(e)
			}
			r, e := s.GetRun(ctx, id)
			if e != nil || r.Status != task.StatusSucceeded || r.Outcome != "partial" {
				t.Fatal(r, e)
			}
			after, e := s.GetSchedule(ctx, p.ID)
			if e != nil || !after.NextRunAt.Equal(p.Anchor.Add(time.Hour)) {
				t.Fatal(after, e)
			}
			if e = s.SetEnabled(ctx, p.ID, after.Version, false, "pause", nil); e != nil {
				t.Fatal(e)
			}
			unchanged, e := s.RegisterSchedule(ctx, *p)
			if e != nil || unchanged.Enabled || !unchanged.NextRunAt.Equal(after.NextRunAt) {
				t.Fatal(unchanged, e)
			}
			if e = s.SetEnabled(ctx, p.ID, after.Version, true, "stale", nil); !errors.Is(e, task.ErrConflict) {
				t.Fatal(e)
			}
			if e = s.SetEnabled(ctx, p.ID, after.Version, false, "pause", nil); e != nil {
				t.Fatal("idempotent pause", e)
			}
		})
	}
}

func TestPersistentPGRecoveryAndConcurrency(t *testing.T) {
	ctx, s, clock := setupPersistent(t, task.TimeInstant)
	instance := runtimeInstance(t, ctx, s)
	def := runtimeDef()
	def.ConcurrencyKey = "shared"
	first, e := s.Submit(ctx, def, "first", nil)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Submit(ctx, def, "first", nil)
	if e != nil || again.ID != first.ID {
		t.Fatal(again, e)
	}
	second, e := s.Submit(ctx, def, "second", nil)
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.Claim(ctx, instance, first.ID, "")
	if e != nil || l == nil {
		t.Fatal(l, e)
	}
	if v, e := s.Claim(ctx, instance, second.ID, ""); e != nil || v != nil {
		t.Fatal("group overlap", v, e)
	}
	if e = s.Complete(ctx, l, task.Completion{Outcome: "failed", ErrorCode: "temporary"}, nil); e != nil {
		t.Fatal(e)
	}
	if v, e := s.Claim(ctx, instance, second.ID, ""); e != nil || v != nil {
		t.Fatal("retry wait released group", v, e)
	}
	clock.Add(int64(2 * time.Second))
	if _, n, e := s.Recover(ctx, 100); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if _, n, e := s.Recover(ctx, 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	l, e = s.Claim(ctx, instance, first.ID, "")
	if e != nil || l == nil {
		t.Fatal(l, e)
	}
	clock.Add(int64(61 * time.Second))
	if e = s.Renew(ctx, l); !errors.Is(e, task.ErrLeaseLost) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var recovered atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, _, e := s.Recover(ctx, 100)
			if e != nil {
				t.Error(e)
			}
			recovered.Add(int32(n))
		}()
	}
	wg.Wait()
	if recovered.Load() != 1 {
		t.Fatal(recovered.Load())
	}
	if e = s.Progress(ctx, l, 100, nil, nil); !errors.Is(e, task.ErrLeaseLost) {
		t.Fatal(e)
	}
	// 不安全的本轮异常终结，保留未知结果并自动释放互斥，不等待确认。
	def.ConcurrencyKey = "unsafe"
	def.Recovery = task.RecoveryReconcile
	unsafe, e := s.Submit(ctx, def, "unsafe", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.HeartbeatInstance(ctx, instance); e != nil {
		t.Fatal(e)
	}
	l, e = s.Claim(ctx, instance, unsafe.ID, "")
	if e != nil || l == nil {
		t.Fatal(l, e)
	}
	clock.Add(int64(61 * time.Second))
	if _, _, e = s.Recover(ctx, 100); e != nil {
		t.Fatal(e)
	}
	r, e := s.GetRun(ctx, unsafe.ID)
	if e != nil || r.Status != task.StatusAbnormal {
		t.Fatal(r, e)
	}
	if _, e = s.Retry(ctx, r.ID, r.Version, "unsafe-retry", nil); !errors.Is(e, task.ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.Resolve(ctx, r.ID, r.Version, "reviewed", "success", nil); !errors.Is(e, task.ErrConflict) {
		t.Fatal("manual success must be rejected", e)
	}
	if e = s.Complete(ctx, l, task.Completion{Outcome: task.OutcomeSuccess}, nil); !errors.Is(e, task.ErrLeaseLost) {
		t.Fatal("old execution must not overwrite abnormal", e)
	}
	next, e := s.Submit(ctx, def, "next-unsafe", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.HeartbeatInstance(ctx, instance); e != nil {
		t.Fatal(e)
	}
	if l, e = s.Claim(ctx, instance, next.ID, ""); e != nil || l == nil {
		t.Fatal("concurrency must be released", l, e)
	}

}

func TestPersistentPGClaimRollbackAndRouting(t *testing.T) {
	ctx, s, clock := setupPersistent(t, task.TimeInstant)
	instance := runtimeInstance(t, ctx, s)
	def := runtimeDef()
	def.ConcurrencyKey = "rollback"
	r, e := s.Submit(ctx, def, "fault", nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.DB().Exec(ctx, `CREATE FUNCTION task_fail_attempt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected attempt failure'; END $$; CREATE TRIGGER task_fail BEFORE INSERT ON task_attempt FOR EACH ROW EXECUTE FUNCTION task_fail_attempt();`)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = g.DB().Exec(ctx, `DROP FUNCTION IF EXISTS task_fail_attempt() CASCADE`) })
	if _, e = s.Claim(ctx, instance, r.ID, ""); e == nil {
		t.Fatal("expected injected failure")
	}
	if _, e = g.DB().Exec(ctx, `DROP TRIGGER task_fail ON task_attempt`); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetRun(ctx, r.ID)
	if e != nil || got.Status != task.StatusPending || got.Attempts != 0 || got.ActiveAttemptID != "" {
		t.Fatal(got, e)
	}
	n, e := g.DB().GetValue(ctx, `SELECT COUNT(*) FROM task_concurrency`)
	if e != nil || n.Int() != 0 {
		t.Fatal(n, e)
	}
	def.HandlerVersion = "v2"
	wrong, e := s.Submit(ctx, def, "wrong-version", nil)
	if e != nil {
		t.Fatal(e)
	}
	if l, e := s.Claim(ctx, instance, wrong.ID, ""); e != nil || l != nil {
		t.Fatal(l, e)
	}
	if _, e = g.DB().Exec(ctx, `UPDATE task SET available_at=? WHERE id=?`, time.Unix(0, clock.Load()).Add(time.Hour), r.ID); e != nil {
		t.Fatal(e)
	}
	if l, e := s.Claim(ctx, instance, r.ID, ""); e != nil || l != nil {
		t.Fatal(l, e)
	}
	empty, e := s.RegisterInstance(ctx, task.Instance{Capacity: 1})
	if e != nil {
		t.Fatal(e)
	}
	if l, e := s.Claim(ctx, empty.ID, "", ""); e != nil || l != nil {
		t.Fatal("empty capabilities must fail closed", l, e)
	}
}

func TestPersistentPGRenewFinishRecoverRace(t *testing.T) {
	ctx, s, clock := setupPersistent(t, task.TimeInstant)
	instance := runtimeInstance(t, ctx, s)
	r, e := s.Submit(ctx, runtimeDef(), "race", nil)
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.Claim(ctx, instance, r.ID, "")
	if e != nil || l == nil {
		t.Fatal(l, e)
	}
	clock.Add(int64(59 * time.Second))
	var wg sync.WaitGroup
	for _, fn := range []func() error{func() error { return s.Renew(ctx, l) }, func() error { return s.Complete(ctx, l, task.Completion{Outcome: "success"}, nil) }, func() error { _, _, e := s.Recover(ctx, 100); return e }} {
		wg.Add(1)
		go func(fn func() error) {
			defer wg.Done()
			if e := fn(); e != nil && !errors.Is(e, task.ErrLeaseLost) {
				t.Error(e)
			}
		}(fn)
	}
	wg.Wait()
	got, e := s.GetRun(ctx, r.ID)
	if e != nil || got.Status != task.StatusSucceeded {
		t.Fatal(got, e)
	}
	clock.Add(int64(2 * time.Minute))
	if _, _, e = s.Recover(ctx, 100); e != nil {
		t.Fatal(e)
	}
	got, e = s.GetRun(ctx, r.ID)
	if e != nil || got.Status != task.StatusSucceeded {
		t.Fatal("success overwritten", got, e)
	}
}

func TestLegacyPGClaimByIDAtomic(t *testing.T) {
	ctx, repo, cleanup := setupTaskPG(t)
	defer cleanup()
	r, e := submitTask(ctx, repo)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var n atomic.Int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := ""
			if i%2 == 0 {
				id = r.ID
			}
			tsk, a, e := repo.ClaimExecution(ctx, id, "worker")
			if e != nil {
				t.Error(e)
				return
			}
			if tsk != nil {
				if a == nil {
					t.Error("orphan running")
				}
				n.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatal(n.Load())
	}
	count, e := g.DB().GetValue(ctx, `SELECT COUNT(*) FROM task_attempt WHERE task_id=?`, r.ID)
	if e != nil || count.Int() != 1 {
		t.Fatal(count, e)
	}
}

func TestPersistentPGManualAuditAndCleanup(t *testing.T) {
	ctx, s, clock := setupPersistent(t, task.TimeInstant)
	p := runtimePlan(t, ctx, s, clock)
	var audits int
	fn := func(context.Context, gdb.TX) error { audits++; return nil }
	r, e := s.Trigger(ctx, p.ID, "manual", fn)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Trigger(ctx, p.ID, "manual", fn)
	if e != nil || r.ID != again.ID || audits != 1 {
		t.Fatal(again, audits, e)
	}
	after, e := s.GetSchedule(ctx, p.ID)
	if e != nil || !after.NextRunAt.Equal(p.NextRunAt) {
		t.Fatal(after, e)
	}
	p.Definition.HandlerVersion = "v2"
	p.DefinitionVersion++
	if e = s.Configure(ctx, p.ID, after.Version, *p, "configure", nil); e != nil {
		t.Fatal(e)
	}
	old, e := s.GetRun(ctx, r.ID)
	if e != nil || old.Definition.HandlerVersion != "v1" {
		t.Fatal(old, e)
	}
	if n, e := s.Cleanup(ctx, time.Unix(0, clock.Load()).Add(time.Hour), 100); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	instance := runtimeInstance(t, ctx, s)
	l, e := s.Claim(ctx, instance, r.ID, "")
	if e != nil || l == nil {
		t.Fatal(l, e)
	}
	failure := errors.New("audit failed")
	if e = s.Complete(ctx, l, task.Completion{Outcome: "success"}, func(context.Context, gdb.TX) error { return failure }); !errors.Is(e, failure) {
		t.Fatal(e)
	}
	old, e = s.GetRun(ctx, r.ID)
	if e != nil || old.Status != task.StatusRunning {
		t.Fatal(old, e)
	}
	if e = s.Complete(ctx, l, task.Completion{Outcome: "success"}, nil); e != nil {
		t.Fatal(e)
	}
}

func TestPersistentPGExecutorHeartbeat(t *testing.T) {
	ctx, _, _ := setupPersistent(t, task.TimeInstant)
	s, e := task.NewStore(task.StoreConfig{DB: g.DB(), Namespace: "executor", Now: time.Now})
	if e != nil {
		t.Fatal(e)
	}
	id := runtimeInstance(t, ctx, s)
	d := runtimeDef()
	d.Lease = time.Second
	run, e := s.Submit(ctx, d, "long", nil)
	if e != nil {
		t.Fatal(e)
	}
	executor := task.Executor{Store: s, InstanceID: id, Handlers: map[string]task.HandlerRegistration{"sync": {Version: "v1", Handler: func(ctx context.Context, _ *task.Execution) (task.Completion, error) {
		select {
		case <-time.After(1400 * time.Millisecond):
			return task.Completion{Outcome: "success"}, nil
		case <-ctx.Done():
			return task.Completion{}, ctx.Err()
		}
	}}}}
	if worked, e := executor.RunOnce(ctx, run.ID); e != nil || !worked {
		t.Fatal(worked, e)
	}
	got, e := s.GetRun(ctx, run.ID)
	if e != nil || got.Status != task.StatusSucceeded {
		t.Fatal(got, e)
	}
	d.Timeout = 100 * time.Millisecond
	run, e = s.Submit(ctx, d, "deadline", nil)
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := executor.RunOnce(ctx, run.ID); !worked || e == nil {
		t.Fatal(worked, e)
	}
	if _, _, e = s.Recover(ctx, 100); e != nil {
		t.Fatal(e)
	}
	got, e = s.GetRun(ctx, run.ID)
	if e != nil || got.Status == task.StatusSucceeded || got.Status == task.StatusRunning {
		t.Fatal(got, e)
	}
}

func TestPersistentPGBlockedPlanDoesNotStarveOtherPlans(t *testing.T) {
	ctx, s, clock := setupPersistent(t, task.TimeInstant)
	anchor := time.Unix(0, clock.Load())
	first, e := s.RegisterSchedule(ctx, task.Schedule{Name: "backlog", Enabled: true, DefinitionVersion: 1, Definition: runtimeDef(), Kind: "interval", Interval: time.Hour, Timezone: "UTC", Anchor: anchor.Add(-2 * time.Hour), Misfire: task.MisfireCatchUp, MaxCatchUp: 2})
	if e != nil {
		t.Fatal(e)
	}
	if r, e := s.Tick(ctx, 1, nil); e != nil || len(r) != 1 || r[0].ScheduleID != first.ID {
		t.Fatal(r, e)
	}
	second, e := s.RegisterSchedule(ctx, task.Schedule{Name: "other", Enabled: true, DefinitionVersion: 1, Definition: runtimeDef(), Kind: "interval", Interval: time.Hour, Timezone: "UTC", Anchor: anchor, Misfire: task.MisfireCoalesce, MaxCatchUp: 1})
	if e != nil {
		t.Fatal(e)
	}
	if r, e := s.Tick(ctx, 1, nil); e != nil || len(r) != 1 || r[0].ScheduleID != second.ID {
		t.Fatal("blocked plan starved independent plan", r, e)
	}
}

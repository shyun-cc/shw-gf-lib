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

// setupPlatform 在 setupPersistent 的同一张表与时钟上构造 TypeControl Store。
func setupPlatform(t *testing.T, opts ...func(*task.StoreConfig)) (context.Context, *task.Store, *task.Store, *atomic.Int64) {
	t.Helper()
	ctx, legacy, clock := setupPersistent(t, task.TimeInstant)
	cfg := task.StoreConfig{DB: g.DB(), Namespace: "test", TypeControl: true, Now: func() time.Time { return time.Unix(0, clock.Load()).UTC() }}
	for _, o := range opts {
		o(&cfg)
	}
	s, err := task.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s, legacy, clock
}

func platformPolicy() task.TypePolicy {
	return task.TypePolicy{Lease: time.Minute, Retry: task.RetryOptions{MaxAttempts: 3, Base: time.Second, Factor: 2, Max: time.Minute}, Recovery: task.RecoveryRetry}
}

func openType(t *testing.T, ctx context.Context, s *task.Store, typ string, concurrency int, mutate func(*task.TypeConfig)) *task.TaskType {
	t.Helper()
	got, err := s.RegisterTypes(ctx, []task.TypeSpec{{Type: typ, HandlerVersion: "v1", Policy: platformPolicy()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := got[0].TypeConfig
	cfg.SubmitEnabled, cfg.ConsumeEnabled, cfg.GlobalConcurrency = true, true, concurrency
	if mutate != nil {
		mutate(&cfg)
	}
	v, err := s.ConfigureType(ctx, typ, got[0].Version, cfg, "open-"+typ, nil)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func platformInstance(t *testing.T, ctx context.Context, s *task.Store, capacity int, caps map[string]string) string {
	t.Helper()
	v, err := s.RegisterInstance(ctx, task.Instance{Role: "executor", Capacity: capacity, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func TestPlatformTypeCatalogDefaultsAndAudit(t *testing.T) {
	ctx, s, _, _ := setupPlatform(t)
	instance := platformInstance(t, ctx, s, 4, map[string]string{"sync": "v1"})
	audits := 0
	count := func(context.Context, gdb.TX) error { audits++; return nil }
	types, err := s.RegisterTypes(ctx, []task.TypeSpec{{Type: "sync", HandlerVersion: "v1", Policy: platformPolicy()}}, count)
	if err != nil || len(types) != 1 || types[0].SubmitEnabled || types[0].ConsumeEnabled || audits != 1 {
		t.Fatal(types, err, audits)
	}
	rev1, err := s.ConfigRevision(ctx)
	if err != nil || rev1 != 1 {
		t.Fatal(rev1, err)
	}
	// 新定义默认关闭：不可提交。
	if _, err = s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, "k1", nil); !errors.Is(err, task.ErrSubmitDisabled) {
		t.Fatal(err)
	}
	// 未知类型拒绝提交。
	if _, err = s.Submit(ctx, task.Definition{Type: "ghost"}, "k0", nil); !errors.Is(err, task.ErrTypeUnavailable) {
		t.Fatal(err)
	}
	cfg := types[0].TypeConfig
	cfg.SubmitEnabled = true
	cfg.GlobalConcurrency = 3
	// 审计失败整体回滚：版本与修订号不变。
	boom := errors.New("audit down")
	if _, err = s.ConfigureType(ctx, "sync", types[0].Version, cfg, "cfg-1", func(context.Context, gdb.TX) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if v, _ := s.GetType(ctx, "sync"); v.Version != types[0].Version || v.SubmitEnabled {
		t.Fatal("audit failure must roll back", v)
	}
	if r, _ := s.ConfigRevision(ctx); r != rev1 {
		t.Fatal("revision bumped by rolled back change", r)
	}
	v, err := s.ConfigureType(ctx, "sync", types[0].Version, cfg, "cfg-1", count)
	if err != nil || !v.SubmitEnabled || v.GlobalConcurrency != 3 || audits != 2 {
		t.Fatal(v, err, audits)
	}
	// 幂等重复不再审计；同键不同内容冲突；旧版本 CAS 冲突。
	if again, err := s.ConfigureType(ctx, "sync", types[0].Version, cfg, "cfg-1", count); err != nil || again.Version != v.Version || audits != 2 {
		t.Fatal(again, err, audits)
	}
	cfg.GlobalConcurrency = 9
	if _, err = s.ConfigureType(ctx, "sync", types[0].Version, cfg, "cfg-1", count); !errors.Is(err, task.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.ConfigureType(ctx, "sync", types[0].Version, cfg, "cfg-2", count); !errors.Is(err, task.ErrConflict) {
		t.Fatal(err)
	}
	// 重新登记不覆盖已配置值。
	again, err := s.RegisterTypes(ctx, []task.TypeSpec{{Type: "sync", HandlerVersion: "v2", GlobalConcurrency: 50, Policy: platformPolicy()}}, count)
	if err != nil || again[0].HandlerVersion != "v1" || again[0].GlobalConcurrency != 3 || audits != 2 {
		t.Fatal(again, err, audits)
	}
	run, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{"a":1}`)}, "k1", nil)
	if err != nil || run.Definition.HandlerVersion != "v1" || run.Definition.Lease != time.Minute {
		t.Fatal(run, err)
	}
	// 消费仍关闭：不可领取。
	if l, err := s.Claim(ctx, instance, "", ""); err != nil || l != nil {
		t.Fatal("consume disabled must not claim", l, err)
	}
	sw, err := s.SetTypeSwitches(ctx, "sync", v.Version, true, true, "switch-1", count)
	if err != nil || !sw.ConsumeEnabled || audits != 3 {
		t.Fatal(sw, err, audits)
	}
	if l, err := s.Claim(ctx, instance, "", ""); err != nil || l == nil {
		t.Fatal("consume enabled must claim", l, err)
	}
	rev, _ := s.ConfigRevision(ctx)
	if rev != rev1+2 {
		t.Fatal("expected two committed config changes", rev)
	}
	if err = s.HeartbeatApplied(ctx, instance, rev); err != nil {
		t.Fatal(err)
	}
	if inst, err := s.GetInstance(ctx, instance); err != nil || inst.AppliedRevision != rev {
		t.Fatal(inst, err)
	}
}

func TestPlatformGlobalConcurrencyAcrossInstances(t *testing.T) {
	ctx, s, _, _ := setupPlatform(t)
	typ := openType(t, ctx, s, "sync", 2, nil)
	caps := map[string]string{"sync": "v1"}
	instances := []string{platformInstance(t, ctx, s, 4, caps), platformInstance(t, ctx, s, 4, caps)}
	for i := 0; i < 6; i++ {
		if _, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, "c"+string(rune('a'+i)), nil); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var leases []*task.Lease
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(inst string) {
			defer wg.Done()
			l, err := s.Claim(ctx, inst, "", "")
			if err != nil {
				t.Error(err)
				return
			}
			if l != nil {
				mu.Lock()
				leases = append(leases, l)
				mu.Unlock()
			}
		}(instances[i%2])
	}
	wg.Wait()
	if len(leases) != 2 {
		t.Fatalf("global concurrency 2 across instances, got %d", len(leases))
	}
	// 调小：在途不受影响，完成一项后仍不补位。
	cfg := typ.TypeConfig
	cfg.GlobalConcurrency = 1
	typ, err := s.ConfigureType(ctx, "sync", typ.Version, cfg, "shrink", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Renew(ctx, leases[1]); err != nil {
		t.Fatal("shrink must not revoke in-flight lease", err)
	}
	if err = s.Complete(ctx, leases[0], task.Completion{Outcome: task.OutcomeSuccess}, nil); err != nil {
		t.Fatal(err)
	}
	if l, err := s.Claim(ctx, instances[0], "", ""); err != nil || l != nil {
		t.Fatal("shrunk limit still full", l, err)
	}
	// 调大：立即可新增领取。
	cfg.GlobalConcurrency = 3
	if _, err = s.ConfigureType(ctx, "sync", typ.Version, cfg, "grow", nil); err != nil {
		t.Fatal(err)
	}
	got := 0
	for i := 0; i < 4; i++ {
		if l, err := s.Claim(ctx, instances[i%2], "", ""); err != nil {
			t.Fatal(err)
		} else if l != nil {
			got++
		}
	}
	if got != 2 {
		t.Fatalf("grow to 3 with 1 running should admit 2, got %d", got)
	}
}

func TestPlatformRateLimitAndOutcomes(t *testing.T) {
	ctx, s, legacy, clock := setupPlatform(t)
	openType(t, ctx, s, "sync", 10, func(c *task.TypeConfig) { c.RateLimit = 2; c.RateWindow = time.Minute })
	instance := platformInstance(t, ctx, s, 10, map[string]string{"sync": "v1", "ghost": "v1"})
	for _, k := range []string{"r1", "r2", "r3"} {
		if _, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, k, nil); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.Claim(ctx, instance, "", "")
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	second, err := s.Claim(ctx, instance, "", "")
	if err != nil || second == nil {
		t.Fatal(second, err)
	}
	if l, err := s.Claim(ctx, instance, "", ""); err != nil || l != nil {
		t.Fatal("rate limit exceeded", l, err)
	}
	if err = s.Complete(ctx, first, task.Completion{Outcome: task.OutcomeSkipped, Stats: map[string]int64{"rows": 0}}, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(ctx, second, task.Completion{Outcome: task.OutcomePermanentFailure, ErrorCode: "bad_input"}, nil); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetRun(ctx, first.Run.ID); r.Status != task.StatusSucceeded || r.Outcome != task.OutcomeSkipped {
		t.Fatal(r)
	}
	if r, _ := s.GetRun(ctx, second.Run.ID); r.Status != task.StatusAbnormal || r.Outcome != task.OutcomePermanentFailure {
		t.Fatal("permanent failure must not retry", r)
	}
	clock.Add(int64(61 * time.Second))
	if err = s.HeartbeatInstance(ctx, instance); err != nil {
		t.Fatal(err)
	}
	if l, err := s.Claim(ctx, instance, "", ""); err != nil || l == nil {
		t.Fatal("window passed", l, err)
	}
	// 类型目录缺定义：旧路径写入的运行在 TypeControl 下拒绝领取。
	ghost, err := legacy.Submit(ctx, task.Definition{Type: "ghost", HandlerVersion: "v1", Lease: time.Minute, Retry: task.RetryOptions{MaxAttempts: 1, Factor: 1}, Recovery: task.RecoveryReview}, "ghost", nil)
	if err != nil {
		t.Fatal(err)
	}
	if l, err := s.Claim(ctx, instance, ghost.ID, ""); err != nil || l != nil {
		t.Fatal("missing type definition must not be claimed", l, err)
	}
	// 缺类型的计划不派发、不推进游标。
	plan, err := s.RegisterSchedule(ctx, task.Schedule{Name: "ghost-plan", Enabled: true, DefinitionVersion: 1, Definition: ghost.Definition, Kind: "interval", Interval: time.Hour, Timezone: "UTC", Anchor: time.Unix(0, clock.Load()), Misfire: task.MisfireCoalesce, MaxCatchUp: 1})
	if err != nil {
		t.Fatal(err)
	}
	if runs, err := s.Tick(ctx, 10, nil); err != nil || len(runs) != 0 {
		t.Fatal(runs, err)
	}
	if p, _ := s.GetSchedule(ctx, plan.ID); !p.NextRunAt.Equal(plan.NextRunAt) {
		t.Fatal("cursor advanced without type", p.NextRunAt, plan.NextRunAt)
	}
}

package verifycode

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

func TestVerifyConcurrentOnce(t *testing.T) {
	mr := testredis.Start(t)
	svc := New()
	ctx := context.Background()
	const key, code, scene = "synthetic-concurrent", "123456", SceneLogin
	if err := mr.Set(svc.codeKey(scene, key), code); err != nil {
		t.Fatal(err)
	}
	if err := mr.Set(svc.failKey(scene, key), "2"); err != nil {
		t.Fatal(err)
	}
	if err := mr.Set(svc.rateLimitKey(scene, key), "1"); err != nil {
		t.Fatal(err)
	}
	mr.SetTTL(svc.rateLimitKey(scene, key), time.Minute)
	var successes, verified atomic.Int32
	d := events.New()
	svc.SetDispatcher(d)
	d.Subscribe(EventVerified, 0, func(_ context.Context, e events.Event) {
		p := e.Payload.(VerifiedPayload)
		if p.Key != key || p.Scene != scene {
			t.Error("incorrect verified payload")
		}
		if mr.Exists(svc.codeKey(scene, key)) || mr.Exists(svc.failKey(scene, key)) {
			t.Error("event before consumption")
		}
		verified.Add(1)
	})
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := svc.Verify(ctx, key, code, scene)
			if err != nil {
				t.Errorf("atomic consume failed: %v", err)
			}
			if ok {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 || verified.Load() != 1 {
		t.Fatalf("successes=%d events=%d", successes.Load(), verified.Load())
	}
	if !mr.Exists(svc.rateLimitKey(scene, key)) || mr.TTL(svc.rateLimitKey(scene, key)) != time.Minute {
		t.Fatal("rate state changed")
	}
}

func TestVerifyFailureStateAndIsolation(t *testing.T) {
	mr := testredis.Start(t)
	setConfigForTest(t, Config{MaxAttempts: 3, FailCountTTL: 20, LockDuration: 10})
	svc := New()
	d := events.New()
	svc.SetDispatcher(d)
	verified := 0
	d.Subscribe(EventVerified, 0, func(context.Context, events.Event) { verified++ })
	ctx := context.Background()
	const key, code, scene = "synthetic-failure", "123456", SceneLogin
	if err := mr.Set(svc.codeKey(scene, key), code); err != nil {
		t.Fatal(err)
	}
	mr.SetTTL(svc.codeKey(scene, key), time.Minute)
	reject := func(value string, inScene Scene) {
		t.Helper()
		if ok, err := svc.Verify(ctx, key, value, inScene); ok || err != nil {
			t.Fatalf("want rejection: %v %v", ok, err)
		}
	}
	reject(code, SceneRegister)
	if mr.Exists(svc.failKey(SceneRegister, key)) {
		t.Fatal("absent code counted")
	}
	reject("wrong", scene)
	if v, _ := mr.Get(svc.failKey(scene, key)); v != "1" {
		t.Fatal("first failure not counted")
	}
	if mr.TTL(svc.failKey(scene, key)) != 20*time.Second {
		t.Fatal("missing failure TTL")
	}
	mr.FastForward(5 * time.Second)
	reject("wrong", scene)
	if mr.TTL(svc.failKey(scene, key)) != 15*time.Second {
		t.Fatal("failure window extended")
	}
	reject("wrong", scene)
	if !mr.Exists(svc.lockKey(scene, key)) || mr.Exists(svc.failKey(scene, key)) {
		t.Fatal("threshold lock/cleanup mismatch")
	}
	if mr.TTL(svc.lockKey(scene, key)) != 10*time.Second {
		t.Fatal("lock TTL mismatch")
	}
	reject(code, scene)
	if !mr.Exists(svc.codeKey(scene, key)) {
		t.Fatal("locked code consumed")
	}
	if verified != 0 {
		t.Fatal("rejection dispatched verified")
	}
	mr.FastForward(11 * time.Second)
	if ok, err := svc.Verify(ctx, key, code, scene); !ok || err != nil {
		t.Fatalf("unlock: %v %v", ok, err)
	}
	if verified != 1 {
		t.Fatal("missing success event")
	}
	if err := mr.Set(svc.codeKey(scene, key), code); err != nil {
		t.Fatal(err)
	}
	mr.SetTTL(svc.codeKey(scene, key), time.Second)
	mr.FastForward(2 * time.Second)
	reject(code, scene)
	reject("", scene)
	if mr.Exists(svc.failKey(scene, key)) || verified != 1 {
		t.Fatal("expired code changed state")
	}
}

func TestInvalidateIfMatchesPreservesNewCodeAndAbuseState(t *testing.T) {
	mr := testredis.Start(t)
	svc := New()
	d := events.New()
	svc.SetDispatcher(d)
	for _, name := range []string{EventGenerated, EventVerified, EventRateLimited} {
		d.Subscribe(name, 0, func(context.Context, events.Event) { t.Error("invalidation dispatched event") })
	}
	ctx := context.Background()
	const key, scene = "synthetic-resend", SceneLogin
	codeKey := svc.codeKey(scene, key)
	// Simulate a new challenge replacing one whose delivery failure arrives late.
	if err := mr.Set(codeKey, "new-challenge"); err != nil {
		t.Fatal(err)
	}
	mr.SetTTL(codeKey, time.Minute)
	abuseKeys := []string{svc.failKey(scene, key), svc.rateLimitKey(scene, key), svc.lockKey(scene, key)}
	for _, k := range abuseKeys {
		if err := mr.Set(k, "1"); err != nil {
			t.Fatal(err)
		}
		mr.SetTTL(k, time.Minute)
	}
	if ok, err := svc.InvalidateIfMatches(ctx, key, "old-challenge", scene); ok || err != nil {
		t.Fatalf("stale revoke: %v %v", ok, err)
	}
	if v, _ := mr.Get(codeKey); v != "new-challenge" || mr.TTL(codeKey) != time.Minute {
		t.Fatal("new challenge altered")
	}
	if ok, err := svc.InvalidateIfMatches(ctx, key, "new-challenge", SceneRegister); ok || err != nil {
		t.Fatalf("scene isolation: %v %v", ok, err)
	}
	if ok, err := svc.InvalidateIfMatches(ctx, key, "new-challenge", scene); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if mr.Exists(codeKey) {
		t.Fatal("matching challenge retained")
	}
	for _, k := range abuseKeys {
		if v, _ := mr.Get(k); v != "1" || mr.TTL(k) != time.Minute {
			t.Fatal("abuse state altered")
		}
	}
	if ok, err := svc.InvalidateIfMatches(ctx, key, "", scene); ok || err != nil {
		t.Fatalf("empty absent challenge: %v %v", ok, err)
	}
}

func TestAtomicAPIsStorageErrorsAreRedacted(t *testing.T) {
	mr := testredis.Start(t)
	svc := New()
	d := events.New()
	svc.SetDispatcher(d)
	d.Subscribe(EventVerified, 0, func(context.Context, events.Event) { t.Error("storage error dispatched verified") })
	// Simulates adapter errors containing command arguments and Cluster routing errors.
	const secret = "synthetic-private-challenge"
	mr.SetError("CROSSSLOT injected " + secret)
	defer mr.SetError("")
	for _, call := range []func() (bool, error){
		func() (bool, error) {
			return svc.Verify(context.Background(), "synthetic-error", secret, SceneLogin)
		},
		func() (bool, error) {
			return svc.InvalidateIfMatches(context.Background(), "synthetic-error", secret, SceneLogin)
		},
	} {
		ok, err := call()
		if ok || err == nil {
			t.Fatalf("storage failure accepted: %v %v", ok, err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "CROSSSLOT") {
			t.Fatal("raw storage error leaked")
		}
	}
}

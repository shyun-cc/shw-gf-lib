//go:build integration

package tests

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"
	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/verifycode"
)

// TestVerifyCodeAtomicRedis validates EVAL argument expansion and atomic state on real Redis.
// No vendor credentials are involved; REDIS_ADDR is required (no miniredis fallback).
func TestVerifyCodeAtomicRedis(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("REDIS_ADDR not configured; real Redis required")
	}
	setupRedis(t)
	ctx := context.Background()
	prefix := "gf-test-vc:{" + uuid.NewString() + "}"
	svc := &verifycode.VerifyCodeService{Prefix: prefix}
	const key, code, scene = "synthetic-user", "synthetic-challenge", verifycode.SceneLogin
	codeKey := fmt.Sprintf("%s:%s:%s", prefix, scene, key)
	failKey := fmt.Sprintf("%s:fail:%s:%s", prefix, scene, key)
	rateKey := fmt.Sprintf("%s:rl:%s:%s", prefix, scene, key)
	lockKey := fmt.Sprintf("%s:lock:%s:%s", prefix, scene, key)
	keys := []string{codeKey, failKey, rateKey, lockKey}
	t.Cleanup(func() {
		if _, err := g.Redis().Del(ctx, keys...); err != nil {
			t.Error("Redis cleanup failed")
		}
	})
	for _, pair := range [][2]string{{codeKey, code}, {failKey, "2"}, {rateKey, "1"}} {
		if err := g.Redis().SetEX(ctx, pair[0], pair[1], 60); err != nil {
			t.Fatal("seed failed")
		}
	}
	var successes, verified atomic.Int32
	d := events.New()
	svc.SetDispatcher(d)
	d.Subscribe(verifycode.EventVerified, 0, func(context.Context, events.Event) { verified.Add(1) })
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := svc.Verify(ctx, key, code, scene)
			if err != nil {
				t.Error("atomic verification failed")
			}
			if ok {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 || verified.Load() != 1 {
		t.Fatalf("successes=%d verified=%d", successes.Load(), verified.Load())
	}
	for _, k := range []string{codeKey, failKey} {
		if n, err := g.Redis().Exists(ctx, k); err != nil || n != 0 {
			t.Fatal("consumed state retained")
		}
	}
	if n, err := g.Redis().Exists(ctx, rateKey); err != nil || n != 1 {
		t.Fatal("rate state lost")
	}
	// Wrong-code count, lock rejection, and delayed delivery compensation use the same real adapter.
	if err := g.Redis().SetEX(ctx, codeKey, "new-challenge", 60); err != nil {
		t.Fatal("seed failed")
	}
	if ok, err := svc.Verify(ctx, key, "wrong", scene); ok || err != nil {
		t.Fatal("wrong challenge accepted")
	}
	if count, err := g.Redis().Get(ctx, failKey); err != nil || count.String() != "1" {
		t.Fatal("failure count mismatch")
	}
	if err := g.Redis().SetEX(ctx, lockKey, "1", 60); err != nil {
		t.Fatal("seed failed")
	}
	if ok, err := svc.Verify(ctx, key, "new-challenge", scene); ok || err != nil {
		t.Fatal("locked challenge accepted")
	}
	if ok, err := svc.InvalidateIfMatches(ctx, key, code, scene); ok || err != nil {
		t.Fatal("stale revoke accepted")
	}
	if stored, err := svc.GetStoredCode(ctx, key, scene); err != nil || stored != "new-challenge" {
		t.Fatal("new challenge lost")
	}
	if ok, err := svc.InvalidateIfMatches(ctx, key, "new-challenge", scene); !ok || err != nil {
		t.Fatal("matching revoke failed")
	}
	for _, k := range []string{failKey, rateKey, lockKey} {
		if n, err := g.Redis().Exists(ctx, k); err != nil || n != 1 {
			t.Fatal("abuse state lost")
		}
	}
	if verified.Load() != 1 {
		t.Fatal("failed/revoked challenge dispatched verified")
	}
}

package wxapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

func overwriteSafePasswordRecord(t *testing.T, store *RedisSafePasswordStore, record SafePasswordRecord) {
	t.Helper()
	key, err := store.key(record.AppID, record.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.Redis().Do(context.Background(), "SET", key, string(raw), "EX", 60); err != nil {
		t.Fatal("fixture cache write failed")
	}
}

func TestRedisSafePasswordStoreExactCompareAndIsolation(t *testing.T) {
	f := newSafePasswordFixture(t)
	ctx := context.Background()
	challenge := issueSafePassword(t, f)
	record, err := f.store.Load(ctx, f.config.AppID, challenge.Nonce)
	if err != nil || record == nil {
		t.Fatal("record load failed", err)
	}
	if err = f.store.Save(ctx, *record, time.Minute); err == nil {
		t.Fatal("nonce collision overwritten")
	}
	tampered := *record
	tampered.Binding.Purpose = "change-password"
	consumed, err := f.store.Consume(ctx, tampered)
	if err != nil || consumed {
		t.Fatal("mismatched full record consumed")
	}
	otherNamespace, _ := NewRedisSafePasswordStore("other:deployment")
	if found, err := otherNamespace.Load(ctx, f.config.AppID, challenge.Nonce); err != nil || found != nil {
		t.Fatal("namespace isolation failed")
	}
	if found, err := f.store.Load(ctx, "wx-another-app", challenge.Nonce); err != nil || found != nil {
		t.Fatal("appid isolation failed")
	}
	// Same nonce can be stored for another app without colliding, but consuming it
	// cannot remove the original app's record in a shared cache.
	another := *record
	another.AppID = "wx-another-app"
	if err = f.store.Save(ctx, another, time.Minute); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.store.Consume(ctx, another); err != nil || !ok {
		t.Fatal("other app consume failed")
	}
	if remaining, err := f.store.Load(ctx, record.AppID, record.Nonce); err != nil || remaining == nil {
		t.Fatal("other app deleted original record")
	}
	consumed, err = f.store.Consume(ctx, *record)
	if err != nil || !consumed {
		t.Fatal("exact expected record not consumed")
	}
	if again, err := f.store.Consume(ctx, *record); err != nil || again {
		t.Fatal("consume replay accepted")
	}
	key, _ := f.store.key(record.AppID, record.Nonce)
	if strings.Contains(key, record.AppID) || strings.Contains(key, record.Nonce) || strings.Contains(key, record.Binding.SessionID) {
		t.Fatal("private identity entered cache key")
	}
}

func TestRedisSafePasswordStoreExpirationAndMalformedRecord(t *testing.T) {
	f := newSafePasswordFixture(t)
	ctx := context.Background()
	challenge := issueSafePassword(t, f)
	record, err := f.store.Load(ctx, f.config.AppID, challenge.Nonce)
	if err != nil || record == nil {
		t.Fatal(err)
	}
	// Expire the actual adapter key by advancing the shared miniredis clock.
	key, _ := f.store.key(record.AppID, record.Nonce)
	cache := testredis.Start(t)
	cache.FastForward(2 * time.Minute)
	if absent, err := f.store.Load(ctx, record.AppID, record.Nonce); err != nil || absent != nil {
		t.Fatal("missing record not absent")
	}
	for _, bad := range []string{"", `{}`, `{"appid":"other","nonce":"bad"}`, `{"unexpected":"field"}`, strings.Repeat("x", 17<<10)} {
		if _, err = g.Redis().Do(ctx, "SET", key, bad, "EX", 60); err != nil {
			t.Fatal(err)
		}
		found, err := f.store.Load(ctx, record.AppID, record.Nonce)
		if err == nil || found != nil {
			t.Fatal("malformed cache record accepted")
		}
		assertSafePasswordError(t, err)
	}
	if _, err = NewRedisSafePasswordStore(""); err == nil {
		t.Fatal("empty namespace accepted")
	}
	if _, err = NewRedisSafePasswordStore("unsafe namespace"); err == nil {
		t.Fatal("unsafe namespace accepted")
	}
	oversized := *record
	oversized.Profile.CustomHash = strings.Repeat("x", 17<<10)
	if err = f.store.Save(ctx, oversized, time.Minute); err == nil {
		t.Fatal("oversized record persisted")
	}
	if err = f.store.Save(ctx, *record, 0); err == nil {
		t.Fatal("persistent no-TTL record accepted")
	}
}

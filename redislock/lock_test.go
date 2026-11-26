package redislock

import (
	"context"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestGetFirstSuccess 首次获取锁必须成功，且 Redis 中写入带前缀的 key（值为 owner）
func TestGetFirstSuccess(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()

	lock := Create("first-success", 10)
	if lock.Owner() == "" {
		t.Fatal("Create 后 Owner 不应为空")
	}
	if lock.Name() != "redis-lock:first-success" {
		t.Fatalf("Name 应含 redis-lock: 前缀，实际为 %s", lock.Name())
	}

	if !lock.Get(ctx) {
		t.Fatal("首次 Get 应成功")
	}
	// 直接检查 miniredis 中的最终状态：key 已写入
	if !mr.Exists(lock.Name()) {
		t.Fatalf("Get 成功后 Redis 中应存在 key %s", lock.Name())
	}
}

// TestGetMutualExclusion 同名锁的第二个实例 Get 必须互斥失败
func TestGetMutualExclusion(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	lockA := Create("mutual", 10)
	lockB := Create("mutual", 10) // 同名、不同 owner

	if !lockA.Get(ctx) {
		t.Fatal("首个实例 Get 应成功")
	}
	if lockB.Get(ctx) {
		t.Fatal("同名第二个实例 Get 应互斥失败")
	}
}

// TestReleaseAllowsOtherToGet 释放自己的锁后，他人应能立即获取
func TestReleaseAllowsOtherToGet(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()

	lockA := Create("release-then-get", 10)
	lockB := Create("release-then-get", 10)

	if !lockA.Get(ctx) {
		t.Fatal("lockA Get 应成功")
	}
	if !lockA.Release(ctx) {
		t.Fatal("持锁者 Release 应返回 true")
	}
	if mr.Exists(lockA.Name()) {
		t.Fatal("Release 后锁 key 应被删除")
	}
	if !lockB.Get(ctx) {
		t.Fatal("锁释放后他人 Get 应成功")
	}
}

// TestReleaseOwnerMismatch Release 必须比对 owner：他人 Release 返回 false 且锁原样保留
func TestReleaseOwnerMismatch(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()

	lockA := Create("owner-mismatch", 10)
	lockB := Create("owner-mismatch", 10)

	if !lockA.Get(ctx) {
		t.Fatal("lockA Get 应成功")
	}
	if lockB.Release(ctx) {
		t.Fatal("非持锁者 Release 应返回 false")
	}
	if !mr.Exists(lockA.Name()) {
		t.Fatal("非持锁者 Release 后锁不应被删除")
	}
	// 锁仍被 A 占用：第三个实例获取不到
	if Create("owner-mismatch", 10).Get(ctx) {
		t.Fatal("锁仍被 A 持有时，新实例 Get 应失败")
	}
	// 原 owner 视角依旧有效：A 仍能续期成功
	if !lockA.Extend(ctx, 20) {
		t.Fatal("锁未被误删时，持锁者 Extend 应成功")
	}
}

// TestExtendOnlyOwnLock Extend 仅续自己的锁：owner 不匹配返回 false 且不影响原 TTL
func TestExtendOnlyOwnLock(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()

	lockA := Create("extend-own", 5)
	lockB := Create("extend-own", 5)

	if !lockA.Get(ctx) {
		t.Fatal("lockA Get 应成功")
	}
	if lockB.Extend(ctx, 100) {
		t.Fatal("非持锁者 Extend 应返回 false")
	}
	// B 未续期成功：TTL 仍停留在原 5 秒附近
	if ttl := mr.TTL(lockA.Name()); ttl <= 0 || ttl > 5*time.Second {
		t.Fatalf("非持锁者 Extend 后 TTL 应保持约 5s，实际 %v", ttl)
	}
	// 持锁者本人续期成功：TTL 变为新值
	if !lockA.Extend(ctx, 100) {
		t.Fatal("持锁者 Extend 应成功")
	}
	if ttl := mr.TTL(lockA.Name()); ttl <= 5*time.Second {
		t.Fatalf("持锁者 Extend 后 TTL 应大于原 5s，实际 %v", ttl)
	}
	// 锁不存在时 Extend 也应返回 false
	if Create("extend-own-missing", 5).Extend(ctx, 10) {
		t.Fatal("锁不存在时 Extend 应返回 false")
	}
}

// TestForceReleaseDeletesAnyLock ForceRelease 无条件删除：不持锁的实例也能删掉他人锁
func TestForceReleaseDeletesAnyLock(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()

	lockA := Create("force", 10)
	lockB := Create("force", 10)

	if !lockA.Get(ctx) {
		t.Fatal("lockA Get 应成功")
	}
	// B 并不持有该锁，ForceRelease 仍应删除（无返回值，靠状态断言）
	lockB.ForceRelease(ctx)
	if mr.Exists(lockA.Name()) {
		t.Fatal("ForceRelease 后锁 key 应被无条件删除")
	}
	if lockA.Extend(ctx, 20) {
		t.Fatal("锁已被 ForceRelease 删除，原持锁者 Extend 应失败")
	}
	if !Create("force", 10).Get(ctx) {
		t.Fatal("ForceRelease 后新实例 Get 应成功")
	}
}

// TestGetZeroSecondsNoExpiry 零秒锁：SET NX 无 EX——无过期、非 owner 释放不动、owner 释放后消失
func TestGetZeroSecondsNoExpiry(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()

	lock := Create("no-expiry", 0)
	if !lock.Get(ctx) {
		t.Fatal("seconds=0 的 Get 应成功（无过期锁）")
	}

	// TTL 断言：miniredis 的 direct.TTL 对「无 TTL」返回 0（与真 Redis 的 -1 不同，库文档明示）；
	// 以正秒锁 TTL>0 作对照，区分两种形态
	if ttl := mr.TTL(lock.Name()); ttl != 0 {
		t.Fatalf("零秒锁应无过期（miniredis TTL=0），实际 %v", ttl)
	}
	ttlLock := Create("ttl-contrast", 10)
	if !ttlLock.Get(ctx) {
		t.Fatal("对照组正秒锁 Get 应成功")
	}
	if ttl := mr.TTL(ttlLock.Name()); ttl <= 0 || ttl > 10*time.Second {
		t.Fatalf("正秒锁 TTL 应在 (0,10s]，实际 %v", ttl)
	}

	// 非 owner 释放：key 仍在
	intruder := Create("no-expiry", 0)
	if intruder.Release(ctx) {
		t.Fatal("非 owner 的 Release 不应返回 true")
	}
	if !mr.Exists(lock.Name()) {
		t.Fatal("非 owner 释放后 key 不应消失")
	}

	// owner 释放：key 消失
	if !lock.Release(ctx) {
		t.Fatal("owner 的 Release 应成功")
	}
	if mr.Exists(lock.Name()) {
		t.Fatal("owner 释放后 key 应消失")
	}
}

// TestGetNegativeSecondsRejected 负秒数拒绝获取
func TestGetNegativeSecondsRejected(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()

	lock := Create("negative", -1)
	if lock.Get(ctx) {
		t.Fatal("seconds<0 应拒绝获取")
	}
	if mr.Exists(lock.Name()) {
		t.Fatal("拒绝获取时不应写入 key")
	}
}

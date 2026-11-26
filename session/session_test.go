package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// 约定的时间格式（与 session.go 中 time.Now().Format 保持一致）
const timeLayout = "2006-01-02 15:04:05"

// newTestService 构造测试用会话服务：Role=test、TTL=86400 秒、IDField=admin_id。
// 参数均由测试注入，对应键格式 "test:session:<uuid>" 与 "test:session:user:<id>"。
func newTestService() *SessionService {
	return New("test", 86400, "admin_id")
}

// mustCreate 创建会话并在失败时终止当前测试，返回 *SessionData。
func mustCreate(t *testing.T, svc *SessionService, ctx context.Context, userID string) *SessionData {
	t.Helper()
	sd, err := svc.Create(ctx, userID, map[string]interface{}{
		"name": fmt.Sprintf("用户%s", userID),
	})
	if err != nil {
		t.Fatalf("创建用户 %s 的会话失败: %v", userID, err)
	}
	if sd == nil || sd.UUID == "" {
		t.Fatalf("创建用户 %s 的会话应返回非空 UUID，实际: %+v", userID, sd)
	}
	return sd
}

// TestCreate 创建会话后：会话 Hash 已写入（传入字段 + 自动写入的
// IDField/login_at/last_active_at）、TTL 生效、用户会话索引（SAdd）存在、
// 返回的 SessionData 仅含机制层三字段（UUID/LoginAt/LastActiveAt），
// 业务字段（name/username/role）只经 data 参数落盘 Hash，不进返回值。
func TestCreate(t *testing.T) {
	mr := testredis.Start(t)
	svc := newTestService()
	ctx := context.Background()

	sd, err := svc.Create(ctx, "42", map[string]interface{}{
		"name":     "张三",
		"username": "zhangsan",
		"role":     1,
	})
	if err != nil {
		t.Fatalf("Create 应成功，实际返回错误: %v", err)
	}
	if sd == nil || sd.UUID == "" {
		t.Fatalf("返回的 SessionData.UUID 应非空，实际: %+v", sd)
	}

	// 契约：返回值仅含机制层三字段，业务字段（name/username/role）
	// 由 data 参数传入并落盘 Hash，库不枚举其形状、不进返回值
	if *sd != (SessionData{UUID: sd.UUID, LoginAt: sd.LoginAt, LastActiveAt: sd.LastActiveAt}) {
		t.Errorf("Create 返回值应仅含 UUID/LoginAt/LastActiveAt，实际: %+v", *sd)
	}

	// 返回值中的时间字段非空且格式合法
	for _, ts := range []string{sd.LoginAt, sd.LastActiveAt} {
		if ts == "" {
			t.Fatalf("返回的时间字段不应为空: %+v", sd)
		}
		if _, err := time.Parse(timeLayout, ts); err != nil {
			t.Fatalf("时间字段 %q 应符合格式 %q: %v", ts, timeLayout, err)
		}
	}

	// 会话 Hash：传入字段与自动补充字段均已写入
	sessionKey := "test:session:" + sd.UUID
	if !mr.Exists(sessionKey) {
		t.Fatalf("会话键 %q 应存在", sessionKey)
	}
	wantFields := map[string]string{
		"admin_id":       "42",
		"name":           "张三",
		"username":       "zhangsan",
		"role":           "1",
		"login_at":       sd.LoginAt,
		"last_active_at": sd.LastActiveAt,
	}
	for field, want := range wantFields {
		if got := mr.HGet(sessionKey, field); got != want {
			t.Errorf("Hash 字段 %s = %q, 期望 %q", field, got, want)
		}
	}

	// 用户会话索引：已 SAdd 当前会话 UUID
	userKey := "test:session:user:42"
	if !mr.Exists(userKey) {
		t.Fatalf("用户会话索引键 %q 应存在", userKey)
	}
	members, err := mr.SMembers(userKey)
	if err != nil {
		t.Fatalf("读取索引成员失败: %v", err)
	}
	if len(members) != 1 || members[0] != sd.UUID {
		t.Errorf("索引成员应为 [%s]，实际 %v", sd.UUID, members)
	}

	// TTL 生效：剩余存活时间落在 (0, 86400s] 内
	if ttl := mr.TTL(sessionKey); ttl <= 0 || ttl > 86400*time.Second {
		t.Errorf("会话键 TTL 应在 (0, 86400s] 内，实际 %v", ttl)
	}
}

// TestCreateAutoIDField Create 自动写入 IDField 的两种场景（userID 用
// uuidv7 样式文本，与既有测试的 int64 文本 "42"/"43" 共同覆盖两类主键文本）：
//   - data 未携带 IDField 键：自动以参数值补写
//   - data 携带同名键：被参数值覆盖（覆盖语义是刻意的，保证 Hash 与索引一致）
func TestCreateAutoIDField(t *testing.T) {
	mr := testredis.Start(t)
	svc := newTestService()
	ctx := context.Background()
	userID := "0192c8f0-a1b2-7ccc-8ddd-0e1f2a3b4c5d"

	// 场景一：data 不带 admin_id 键，Create 自动写入参数值
	sd1, err := svc.Create(ctx, userID, map[string]interface{}{
		"name": "甲",
	})
	if err != nil {
		t.Fatalf("data 未携带 IDField 时 Create 应成功: %v", err)
	}
	if got := mr.HGet("test:session:"+sd1.UUID, "admin_id"); got != userID {
		t.Errorf("data 未携带 IDField 时应自动写入 %q，实际 %q", userID, got)
	}

	// 场景二：data 携带同名键，被参数值覆盖
	sd2, err := svc.Create(ctx, userID, map[string]interface{}{
		"admin_id": "wrong",
		"name":     "乙",
	})
	if err != nil {
		t.Fatalf("data 携带同名键时 Create 应成功: %v", err)
	}
	if got := mr.HGet("test:session:"+sd2.UUID, "admin_id"); got != userID {
		t.Errorf("data 同名键应被参数值覆盖为 %q，实际 %q", userID, got)
	}
}

// TestValidateNotExists 不存在的 UUID：返回 (nil, nil)，不报错。
func TestValidateNotExists(t *testing.T) {
	testredis.Start(t)
	svc := newTestService()

	data, err := svc.Validate(context.Background(), "not-exists-uuid")
	if err != nil {
		t.Fatalf("不存在的 UUID 不应返回错误，实际: %v", err)
	}
	if data != nil {
		t.Fatalf("不存在的 UUID 应返回 (nil, nil)，实际 data: %v", data)
	}
}

// TestValidate 有效会话：返回值含传入字段与 uuid，且刷新 last_active_at 与 TTL。
// 刷新验证不依赖真实 sleep：先把 last_active_at 篡改为旧值，再用 miniredis
// 虚拟时钟 FastForward 消耗部分 TTL，Validate 后两者均应恢复。
func TestValidate(t *testing.T) {
	mr := testredis.Start(t)
	svc := newTestService()
	ctx := context.Background()

	sd, err := svc.Create(ctx, "42", map[string]interface{}{
		"name":     "张三",
		"username": "zhangsan",
	})
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	sessionKey := "test:session:" + sd.UUID

	// 预置旧 last_active_at 并消耗 3600s TTL（86400s -> 82800s）
	stale := "2000-01-01 00:00:00"
	if _, err := g.Redis().HSet(ctx, sessionKey, map[string]interface{}{"last_active_at": stale}); err != nil {
		t.Fatalf("预置旧 last_active_at 失败: %v", err)
	}
	mr.FastForward(3600 * time.Second)
	ttlBefore := mr.TTL(sessionKey)

	data, err := svc.Validate(ctx, sd.UUID)
	if err != nil {
		t.Fatalf("有效会话 Validate 应成功，实际: %v", err)
	}
	if data == nil {
		t.Fatal("有效会话 Validate 不应返回 nil")
	}

	// 返回值包含传入字段与 uuid
	for k, want := range map[string]string{
		"uuid":     sd.UUID,
		"admin_id": "42",
		"name":     "张三",
		"username": "zhangsan",
	} {
		if data[k] != want {
			t.Errorf("Validate 返回字段 %s = %q, 期望 %q", k, data[k], want)
		}
	}

	// last_active_at 已刷新：不等于旧值、格式合法、且与 Redis 中落盘值一致
	if data["last_active_at"] == stale || data["last_active_at"] == "" {
		t.Fatalf("last_active_at 应被刷新，实际 %q", data["last_active_at"])
	}
	if _, err := time.Parse(timeLayout, data["last_active_at"]); err != nil {
		t.Fatalf("刷新后的 last_active_at 格式非法: %v", err)
	}
	if got := mr.HGet(sessionKey, "last_active_at"); got != data["last_active_at"] {
		t.Errorf("Redis 中 last_active_at = %q, 应与返回值 %q 一致", got, data["last_active_at"])
	}

	// TTL 已重置回接近完整时长
	ttlAfter := mr.TTL(sessionKey)
	if ttlAfter <= ttlBefore {
		t.Errorf("Validate 后 TTL 应回到接近 86400s，刷新前 %v，刷新后 %v", ttlBefore, ttlAfter)
	}
	if ttlAfter > 86400*time.Second {
		t.Errorf("TTL 不应超过 86400s，实际 %v", ttlAfter)
	}
}

// TestDestroy 销毁会话：删除 session 键，并从用户索引移除该 UUID；
// 同一用户的其余会话不受影响。
func TestDestroy(t *testing.T) {
	mr := testredis.Start(t)
	svc := newTestService()
	ctx := context.Background()

	// 同一用户两个会话
	sd1 := mustCreate(t, svc, ctx, "42")
	sd2 := mustCreate(t, svc, ctx, "42")

	if err := svc.Destroy(ctx, sd1.UUID); err != nil {
		t.Fatalf("Destroy 失败: %v", err)
	}

	// session 键已删除，Validate 返回 (nil, nil)
	key1 := "test:session:" + sd1.UUID
	if mr.Exists(key1) {
		t.Errorf("Destroy 后会话键 %q 应被删除", key1)
	}
	if data, err := svc.Validate(ctx, sd1.UUID); err != nil || data != nil {
		t.Errorf("Destroy 后 Validate 应返回 (nil, nil)，实际 data=%v err=%v", data, err)
	}

	// 用户索引只剩 sd2 的 UUID
	userKey := "test:session:user:42"
	members, err := mr.SMembers(userKey)
	if err != nil {
		t.Fatalf("读取索引成员失败: %v", err)
	}
	if len(members) != 1 || members[0] != sd2.UUID {
		t.Errorf("Destroy 后索引成员应为 [%s]，实际 %v", sd2.UUID, members)
	}

	// sd2 仍有效
	if data, err := svc.Validate(ctx, sd2.UUID); err != nil || data == nil {
		t.Errorf("未销毁的会话 sd2 应仍有效，实际 data=%v err=%v", data, err)
	}
}

// TestDestroyByUserID 销毁指定用户的全部会话：各 session 键被删、
// 索引集合被删除，且其他用户的会话不受影响。
func TestDestroyByUserID(t *testing.T) {
	mr := testredis.Start(t)
	svc := newTestService()
	ctx := context.Background()

	// 用户 42 两个会话，用户 43 一个会话
	sd1 := mustCreate(t, svc, ctx, "42")
	sd2 := mustCreate(t, svc, ctx, "42")
	sd3 := mustCreate(t, svc, ctx, "43")

	if err := svc.DestroyByUserID(ctx, "42"); err != nil {
		t.Fatalf("DestroyByUserID 失败: %v", err)
	}

	// 用户 42 的所有会话键已删除、Validate 均返回 nil
	for _, sd := range []*SessionData{sd1, sd2} {
		if mr.Exists("test:session:" + sd.UUID) {
			t.Errorf("会话键 %q 应被删除", "test:session:"+sd.UUID)
		}
		if data, err := svc.Validate(ctx, sd.UUID); err != nil || data != nil {
			t.Errorf("已销毁会话 %s 的 Validate 应返回 (nil, nil)，实际 data=%v err=%v", sd.UUID, data, err)
		}
	}

	// 索引键整体被删除
	if mr.Exists("test:session:user:42") {
		t.Error("用户索引键 test:session:user:42 应被删除")
	}

	// 用户 43 的会话不受影响
	if data, err := svc.Validate(ctx, sd3.UUID); err != nil || data == nil {
		t.Errorf("用户 43 的会话不应受影响，实际 data=%v err=%v", data, err)
	}
}

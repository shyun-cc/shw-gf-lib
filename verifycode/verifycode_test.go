// verifycode 单元测试（change: add-package-tests，task 3.2）。
//
// 依赖 Redis 的路径全部跑在 internal/testredis 提供的进程内 miniredis 上；
// 时间相关场景（频率限制间隔、锁定时长）用 miniredis.FastForward 快进逻辑时钟，
// 不做真实 sleep。全局 config 是包级变量，需要改配置的用例一律经
// setConfigForTest 注入并在测试结束还原，避免泄漏到同包其他用例。
package verifycode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// setConfigForTest 临时覆盖全局配置（非零字段生效），测试结束后还原原配置。
// 改 config 的用例必须经此入口，防止影响同包其他测试。
func setConfigForTest(t *testing.T, cfg Config) {
	t.Helper()
	old := config
	t.Cleanup(func() { config = old })
	SetConfig(cfg)
}

// wrongCode 基于正确码构造一个必然不同的错误码（首位数字 +1 对 10 取模），
// 避免随机生成的验证码恰好撞上断言用的"错误码"导致用例不稳定。
func wrongCode(code string) string {
	b := []byte(code)
	b[0] = byte('0' + (b[0]-'0'+1)%10)
	return string(b)
}

// TestGenerateStoresFixedLengthCode 场景 1：
// Generate 产出 CodeLen（默认 6）位纯数字码，且 GetStoredCode 可取回同一值。
func TestGenerateStoresFixedLengthCode(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()
	svc := New()
	const key, scene = "10800000001", SceneLogin

	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("同 key 首次 Generate 不应报错: %v", err)
	}
	if len(code) != config.CodeLen {
		t.Fatalf("验证码长度 = %d, 期望 %d（code=%q）", len(code), config.CodeLen, code)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Fatalf("验证码应全为数字, 实际 %q", code)
		}
	}

	stored, err := svc.GetStoredCode(ctx, key, scene)
	if err != nil {
		t.Fatalf("GetStoredCode 不应报错: %v", err)
	}
	if stored != code {
		t.Fatalf("存储的验证码 = %q, 期望与 Generate 返回一致 %q", stored, code)
	}
}

// TestGenerateRateLimit 场景 2：
// 同 key+scene 在 RateInterval（默认 60s）内第二次 Generate 返回 ErrRateLimited；
// 不同 key 互不影响；快进超过间隔后限制解除可再次生成。
func TestGenerateRateLimit(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()
	svc := New()
	const key, scene = "10800000002", SceneLogin

	if _, err := svc.Generate(ctx, key, scene); err != nil {
		t.Fatalf("同 key 首次 Generate 不应报错: %v", err)
	}

	// 频率间隔内第二次：应命中频率限制
	if _, err := svc.Generate(ctx, key, scene); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("间隔内第二次 Generate 应返回 ErrRateLimited, 实际 err = %v", err)
	}

	// 不同 key 的频率限制互不影响
	if _, err := svc.Generate(ctx, "10800000003", scene); err != nil {
		t.Fatalf("不同 key 不应受频率限制影响: %v", err)
	}

	// 快进超过 RateInterval 后限制键过期，可再次生成
	mr.FastForward(time.Duration(config.RateInterval+1) * time.Second)
	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("超过频率间隔后 Generate 不应报错: %v", err)
	}
	stored, err := svc.GetStoredCode(ctx, key, scene)
	if err != nil {
		t.Fatalf("GetStoredCode 不应报错: %v", err)
	}
	if stored != code {
		t.Fatalf("解限后重新生成, 存储码 %q 应等于返回码 %q", stored, code)
	}
}

// TestVerifyCorrectAndWrong 场景 3：
// 正确码 Verify 返回 true；错误码返回 false 且失败计数（failKey）增长，
// 随后正确码仍可通过。
func TestVerifyCorrectAndWrong(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()
	svc := New()
	const key, scene = "user-verify-1", SceneGeneric

	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("Generate 不应报错: %v", err)
	}

	// 错误码：返回 (false, nil)，失败计数增长为 1
	ok, err := svc.Verify(ctx, key, wrongCode(code), scene)
	if err != nil || ok {
		t.Fatalf("错误码 Verify 应返回 (false, nil), 实际 (%v, %v)", ok, err)
	}
	failCount, err := mr.Get(svc.failKey(scene, key))
	if err != nil {
		t.Fatalf("读取失败计数不应报错: %v", err)
	}
	if failCount != "1" {
		t.Fatalf("错误一次后失败计数 = %q, 期望 \"1\"", failCount)
	}

	// 正确码：返回 (true, nil)
	ok, err = svc.Verify(ctx, key, code, scene)
	if err != nil || !ok {
		t.Fatalf("正确码 Verify 应返回 (true, nil), 实际 (%v, %v)", ok, err)
	}
}

// TestVerifyLockAfterMaxAttempts 场景 4：
// 连续错误达到 MaxAttempts 次后写入 lockKey 并清理失败计数；
// 锁定期间即使正确码 Verify 也直接返回 false（lockKey 存在路径）；
// 快进超过 LockDuration 后锁定解除，正确码恢复通过。
func TestVerifyLockAfterMaxAttempts(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()
	// 自定义较小的 MaxAttempts/LockDuration 便于用 FastForward 验证锁定与解锁；
	// 保持 TTL(默认 300s) 远大于 LockDuration(10s)，确保解锁后验证码仍有效
	setConfigForTest(t, Config{MaxAttempts: 3, LockDuration: 10})
	svc := New()
	const key, scene = "user-bruteforce", SceneLogin

	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("Generate 不应报错: %v", err)
	}

	// 连续错 MaxAttempts 次，触发锁定
	wrong := wrongCode(code)
	for i := 1; i <= config.MaxAttempts; i++ {
		ok, err := svc.Verify(ctx, key, wrong, scene)
		if err != nil || ok {
			t.Fatalf("第 %d 次错误码 Verify 应返回 (false, nil), 实际 (%v, %v)", i, ok, err)
		}
	}
	if !mr.Exists(svc.lockKey(scene, key)) {
		t.Fatal("达到 MaxAttempts 后应写入锁定 key")
	}
	if mr.Exists(svc.failKey(scene, key)) {
		t.Fatal("触发锁定后失败计数应被删除")
	}

	// 锁定期间即使正确码也直接 false
	ok, err := svc.Verify(ctx, key, code, scene)
	if err != nil || ok {
		t.Fatalf("锁定期间正确码 Verify 应返回 (false, nil), 实际 (%v, %v)", ok, err)
	}

	// 快进超过 LockDuration 后锁定键过期，正确码恢复通过
	mr.FastForward(time.Duration(config.LockDuration+1) * time.Second)
	ok, err = svc.Verify(ctx, key, code, scene)
	if err != nil || !ok {
		t.Fatalf("锁定过期后正确码 Verify 应返回 (true, nil), 实际 (%v, %v)", ok, err)
	}
}

// TestVerifyConsumesAndRejectsReplay 场景 5：
// Verify 成功原子删除验证码与失败计数，之后同一码不可重放。
func TestVerifyConsumesAndRejectsReplay(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()
	svc := New()
	const key, scene = "user-consume", SceneGeneric

	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("Generate 不应报错: %v", err)
	}

	// 先错一次产生失败计数，验证 Verify 成功同时清理计数
	if ok, err := svc.Verify(ctx, key, wrongCode(code), scene); err != nil || ok {
		t.Fatalf("错误码 Verify 应返回 (false, nil), 实际 (%v, %v)", ok, err)
	}

	if ok, err := svc.Verify(ctx, key, code, scene); err != nil || !ok {
		t.Fatalf("首次正确码应消费成功: %v %v", ok, err)
	}

	// 首次成功后验证码失效：同一码再次 Verify 也 false
	if ok, err := svc.Verify(ctx, key, code, scene); err != nil || ok {
		t.Fatalf("首次成功后正确码 Verify 应返回 (false, nil), 实际 (%v, %v)", ok, err)
	}
	if stored, _ := svc.GetStoredCode(ctx, key, scene); stored != "" {
		t.Fatalf("Verify 成功后存储码应为空, 实际 %q", stored)
	}
	if mr.Exists(svc.failKey(scene, key)) {
		t.Fatal("Verify 成功应清理失败计数 key")
	}
}

// TestGenerateAlnum36Charset 36 进制码型：每位均属 0-9a-z（小写），多次生成字符集合法
func TestGenerateAlnum36Charset(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()
	setConfigForTest(t, Config{CodeLen: 8, CodeType: CodeTypeAlnum36})

	svc := New()
	for round := 0; round < 20; round++ {
		code, err := svc.Generate(ctx, "10800000000", SceneLogin)
		if err != nil {
			// 限频只对同 key 生效；换 key 避免干扰
			code, err = svc.Generate(ctx, fmt.Sprintf("1380000%04d", round), SceneLogin)
			if err != nil {
				t.Fatalf("第 %d 轮 Generate 失败: %v", round, err)
			}
		}
		if len(code) != 8 {
			t.Fatalf("码长应为 8，实际 %d（%s）", len(code), code)
		}
		for _, ch := range code {
			if !strings.ContainsRune(dictAlnum36, ch) {
				t.Fatalf("码 %s 含非法字符 %q（应属 0-9a-z 小写）", code, ch)
			}
		}
	}
}

// TestGenerateNumericDefaultCharset 默认（数字）码型回归：每位均属 0-9
func TestGenerateNumericDefaultCharset(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	svc := New()
	for round := 0; round < 20; round++ {
		code, err := svc.Generate(ctx, fmt.Sprintf("1390000%04d", round), SceneLogin)
		if err != nil {
			t.Fatalf("第 %d 轮 Generate 失败: %v", round, err)
		}
		for _, ch := range code {
			if ch < '0' || ch > '9' {
				t.Fatalf("数字码型下码 %s 含非数字字符 %q", code, ch)
			}
		}
	}
}

// TestCheckRateLimitStates 限频独立查询三态：Generate 后被限、窗口过期后未限；
// 查询不写码、不重置标记（限频 key 剩余 TTL 单调递减）
func TestCheckRateLimitStates(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()
	setConfigForTest(t, Config{RateInterval: 60})

	svc := New()
	key := "10700000000"

	// 查询先于任何 Generate：未限
	limited, err := svc.CheckRateLimit(ctx, key, SceneLogin)
	if err != nil {
		t.Fatalf("CheckRateLimit 失败: %v", err)
	}
	if limited {
		t.Fatal("未 Generate 过应返回未限")
	}

	// 对照 key：全程不 Generate，断言查询不留痕
	controlKey := "10600000000"

	if _, err := svc.Generate(ctx, key, SceneLogin); err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}

	limited, err = svc.CheckRateLimit(ctx, key, SceneLogin)
	if err != nil || !limited {
		t.Fatalf("Generate 后应返回被限: limited=%v err=%v", limited, err)
	}

	// 查询不重置标记：限频 key 的 TTL 应仍按原窗口递减（未到期前再次查询仍被限）
	mr.FastForward(30 * time.Second)
	limited2, err := svc.CheckRateLimit(ctx, key, SceneLogin)
	if err != nil || !limited2 {
		t.Fatalf("窗口内（30s<60s）查询仍应被限: limited=%v err=%v", limited2, err)
	}

	// 快进越过窗口：未限
	mr.FastForward(31 * time.Second)
	limited3, err := svc.CheckRateLimit(ctx, key, SceneLogin)
	if err != nil || limited3 {
		t.Fatalf("窗口过期后应返回未限: limited=%v err=%v", limited3, err)
	}

	// 对照 key 全程未限且未产生任何 key
	if limitedControl, err := svc.CheckRateLimit(ctx, controlKey, SceneLogin); err != nil || limitedControl {
		t.Fatalf("对照 key 应未限: %v %v", limitedControl, err)
	}
	for _, k := range []string{svc.codeKey(SceneLogin, controlKey), svc.rateLimitKey(SceneLogin, controlKey)} {
		if mr.Exists(k) {
			t.Fatalf("对照 key %s 不应被写入", k)
		}
	}
}

// ---------------------------------------------------------------------------
// 事件断言用例组（change: verifycode-events，task 2.1）。
//
// 对照 .changes/verifycode-events/specs/verifycode-events/spec.md 全部 scenario：
// 三个事件（Generated / Verified / RateLimited）的恰好一次、零派发与载荷完整性，
// SetDispatcher 独立分发器隔离，以及 handler panic 隔离。形态沿用 sms 包
// TestSendEvents：mutex + 切片收集 payload + 恰好一次断言；每个用例独立
// testredis.Start + events.New()（派发同步，断言发生在 Generate/Verify 返回后），
// 不依赖执行顺序。
// ---------------------------------------------------------------------------

// TestEventGeneratedDispatchedOnce 对照 spec「产码事件 - Generate 成功派发恰好一次且载荷完整」：
// 注入独立分发器并订阅 EventGenerated 后 Generate 成功，应恰好收到一次事件，
// 载荷 Key/Scene 与入参一致、Code 与 Generate 返回值一致。
func TestEventGeneratedDispatchedOnce(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()
	svc := New()
	d := events.New()
	svc.SetDispatcher(d)

	var mu sync.Mutex
	var payloads []GeneratedPayload
	d.Subscribe(EventGenerated, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(GeneratedPayload); ok {
			mu.Lock()
			payloads = append(payloads, p)
			mu.Unlock()
		}
	})

	const key, scene = "10800000011", SceneLogin
	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("Generate 不应报错: %v", err)
	}

	if len(payloads) != 1 {
		t.Fatalf("EventGenerated 应恰好一次, 实际 %d 次", len(payloads))
	}
	if got := payloads[0]; got.Key != key || got.Scene != scene || got.Code != code {
		t.Fatalf("产码载荷不符: %+v（期望 Key=%s Scene=%s Code=%s）", got, key, scene, code)
	}
}

// TestEventGeneratedZeroSubscribersNoop 对照 spec「产码事件 - 零订阅时行为不变」
// （含 Requirement 中「零订阅时派发为 no-op，返回值与错误行为与无事件时完全一致」）：
// 注入零订阅的空分发器后 Generate，应正常返回验证码、无错误，存储路径行为不变。
func TestEventGeneratedZeroSubscribersNoop(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()
	svc := New()
	svc.SetDispatcher(events.New()) // 空分发器：零订阅，派发为 no-op

	const key, scene = "10800000012", SceneLogin
	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("零订阅时 Generate 不应报错: %v", err)
	}
	if len(code) != config.CodeLen {
		t.Fatalf("零订阅时验证码长度 = %d, 期望 %d", len(code), config.CodeLen)
	}
	stored, err := svc.GetStoredCode(ctx, key, scene)
	if err != nil {
		t.Fatalf("GetStoredCode 不应报错: %v", err)
	}
	if stored != code {
		t.Fatalf("零订阅时存储码 %q 应等于返回码 %q（行为与无事件时一致）", stored, code)
	}
}

// TestEventRateLimitedDispatchedBeforeError 对照 spec「限频拒绝事件 - 限频拒绝时派发」：
// 同 key+scene 窗口内第二次 Generate 返回 ErrRateLimited，此前应恰好派发一次
// EventRateLimited（载荷 Key/Scene 与入参一致）；同时 EventGenerated 只收到一次
// （首次成功那次，被拒的第二次不产码）。
func TestEventRateLimitedDispatchedBeforeError(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()
	svc := New()
	d := events.New()
	svc.SetDispatcher(d)

	var mu sync.Mutex
	var rateLimited []RateLimitedPayload
	var generated []GeneratedPayload
	d.Subscribe(EventRateLimited, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(RateLimitedPayload); ok {
			mu.Lock()
			rateLimited = append(rateLimited, p)
			mu.Unlock()
		}
	})
	d.Subscribe(EventGenerated, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(GeneratedPayload); ok {
			mu.Lock()
			generated = append(generated, p)
			mu.Unlock()
		}
	})

	const key, scene = "10800000013", SceneLogin
	if _, err := svc.Generate(ctx, key, scene); err != nil {
		t.Fatalf("首次 Generate 不应报错: %v", err)
	}
	if _, err := svc.Generate(ctx, key, scene); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("窗口内第二次 Generate 应返回 ErrRateLimited, 实际 %v", err)
	}

	if len(rateLimited) != 1 {
		t.Fatalf("EventRateLimited 应恰好一次, 实际 %d 次", len(rateLimited))
	}
	if got := rateLimited[0]; got.Key != key || got.Scene != scene {
		t.Fatalf("限频载荷不符: %+v（期望 Key=%s Scene=%s）", got, key, scene)
	}
	if len(generated) != 1 {
		t.Fatalf("EventGenerated 应只收到首次成功一次, 实际 %d 次", len(generated))
	}
}

// TestEventCheckRateLimitZeroDispatch 对照 spec「限频拒绝事件 - CheckRateLimit 查询零派发」：
// CheckRateLimit 是只读状态查询口，被限态与未限态均不得派发任何事件。
// 被限态由独立种子实例构造：种子注入空分发器，其产码事件既不进被测分发器 d，
// 也不污染全局 Default()（后者进程级共享且 Subscribe 无法退订）。
func TestEventCheckRateLimitZeroDispatch(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	seed := New()
	seed.SetDispatcher(events.New()) // 空分发器：构造数据的事件不进任何被测通道
	const limitedKey, freeKey, scene = "10800000014", "10800000015", SceneLogin
	if _, err := seed.Generate(ctx, limitedKey, scene); err != nil {
		t.Fatalf("构造被限态的 Generate 失败: %v", err)
	}

	svc := New()
	d := events.New()
	svc.SetDispatcher(d)
	var mu sync.Mutex
	var generated []GeneratedPayload
	var verified []VerifiedPayload
	var rateLimited []RateLimitedPayload
	d.Subscribe(EventGenerated, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(GeneratedPayload); ok {
			mu.Lock()
			generated = append(generated, p)
			mu.Unlock()
		}
	})
	d.Subscribe(EventVerified, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(VerifiedPayload); ok {
			mu.Lock()
			verified = append(verified, p)
			mu.Unlock()
		}
	})
	d.Subscribe(EventRateLimited, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(RateLimitedPayload); ok {
			mu.Lock()
			rateLimited = append(rateLimited, p)
			mu.Unlock()
		}
	})

	// 被限态查询
	limited, err := svc.CheckRateLimit(ctx, limitedKey, scene)
	if err != nil {
		t.Fatalf("被限态 CheckRateLimit 失败: %v", err)
	}
	if !limited {
		t.Fatal("构造的被限 key 应返回被限")
	}
	// 未限态查询（从未 Generate 过的 key）
	limited, err = svc.CheckRateLimit(ctx, freeKey, scene)
	if err != nil {
		t.Fatalf("未限态 CheckRateLimit 失败: %v", err)
	}
	if limited {
		t.Fatal("从未 Generate 的 key 应返回未限")
	}

	if len(generated) != 0 || len(verified) != 0 || len(rateLimited) != 0 {
		t.Fatalf("CheckRateLimit 两态查询均不应派发事件: generated=%d verified=%d rateLimited=%d",
			len(generated), len(verified), len(rateLimited))
	}
}

// TestEventVerifiedDispatchedOnce 对照 spec「验证通过事件 - 正确码派发恰好一次」：
// Generate 后以正确码 Verify 通过，应恰好派发一次 EventVerified（载荷 Key/Scene
// 与入参一致）；同一分发器上 EventGenerated 应收到一次（Generate 那次）。
func TestEventVerifiedDispatchedOnce(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()
	svc := New()
	d := events.New()
	svc.SetDispatcher(d)

	var mu sync.Mutex
	var verified []VerifiedPayload
	var generated []GeneratedPayload
	d.Subscribe(EventVerified, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(VerifiedPayload); ok {
			mu.Lock()
			verified = append(verified, p)
			mu.Unlock()
		}
	})
	d.Subscribe(EventGenerated, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(GeneratedPayload); ok {
			mu.Lock()
			generated = append(generated, p)
			mu.Unlock()
		}
	})

	const key, scene = "user-event-verify", SceneGeneric
	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("Generate 不应报错: %v", err)
	}
	ok, err := svc.Verify(ctx, key, code, scene)
	if err != nil || !ok {
		t.Fatalf("正确码 Verify 应返回 (true, nil), 实际 (%v, %v)", ok, err)
	}

	if len(verified) != 1 {
		t.Fatalf("EventVerified 应恰好一次, 实际 %d 次", len(verified))
	}
	if got := verified[0]; got.Key != key || got.Scene != scene {
		t.Fatalf("验证通过载荷不符: %+v（期望 Key=%s Scene=%s）", got, key, scene)
	}
	if len(generated) != 1 {
		t.Fatalf("EventGenerated 应收到 Generate 那一次, 实际 %d 次", len(generated))
	}
}

// TestEventWrongCodeZeroVerifiedDispatch 对照 spec「验证通过事件 - 错误码零派发」：
// MaxAttempts=3 下连续错满 3 次（含触发锁定的最后一次），EventVerified 零到达；
// 锁定生效后正确码 Verify 走锁定路径，同样零派发。
func TestEventWrongCodeZeroVerifiedDispatch(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()
	setConfigForTest(t, Config{MaxAttempts: 3})
	svc := New()
	d := events.New()
	svc.SetDispatcher(d)

	var mu sync.Mutex
	var verified []VerifiedPayload
	d.Subscribe(EventVerified, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(VerifiedPayload); ok {
			mu.Lock()
			verified = append(verified, p)
			mu.Unlock()
		}
	})

	const key, scene = "user-event-wrong", SceneLogin
	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("Generate 不应报错: %v", err)
	}

	wrong := wrongCode(code)
	for i := 1; i <= config.MaxAttempts; i++ {
		if ok, err := svc.Verify(ctx, key, wrong, scene); err != nil || ok {
			t.Fatalf("第 %d 次错误码 Verify 应返回 (false, nil), 实际 (%v, %v)", i, ok, err)
		}
	}
	if !mr.Exists(svc.lockKey(scene, key)) {
		t.Fatal("错满 MaxAttempts 后应已锁定（证明最后一次错误走了触发锁定的路径）")
	}

	// 锁定期间正确码也直接 false，锁定路径同样不得派发
	if ok, err := svc.Verify(ctx, key, code, scene); err != nil || ok {
		t.Fatalf("锁定期间正确码 Verify 应返回 (false, nil), 实际 (%v, %v)", ok, err)
	}

	if len(verified) != 0 {
		t.Fatalf("错误码（含触发锁定的最后一次）不应派发 EventVerified, 实际 %d 次", len(verified))
	}
}

// TestEventDispatcherIsolation 对照 spec「Dispatcher 注入语义 - 独立分发器隔离」：
// 实例 A 注入分发器 dA、实例 B 不注入（走全局 Default()），各自 Generate 成功后
// dA 只收到 A 的事件一次、全局只收到 B 的事件一次，互不串扰（注入实例的事件
// 不泄漏到全局）。注意：全局 Default() 进程级共享且 Subscribe 无法退订，可能被
// 同包其他用例的派发污染，故只做自身 key 的过滤断言（恰好收到各自 key 一次、
// 不含对方 key），不做全局零到达断言。
func TestEventDispatcherIsolation(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	dA := events.New()
	var mu sync.Mutex
	var aGot, globalGot []GeneratedPayload
	dA.Subscribe(EventGenerated, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(GeneratedPayload); ok {
			mu.Lock()
			aGot = append(aGot, p)
			mu.Unlock()
		}
	})
	events.Subscribe(EventGenerated, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(GeneratedPayload); ok {
			mu.Lock()
			globalGot = append(globalGot, p)
			mu.Unlock()
		}
	})

	svcA := New()
	svcA.SetDispatcher(dA)
	svcB := New() // 不注入：事件走全局 Default()

	if _, err := svcA.Generate(ctx, "key-a", SceneLogin); err != nil {
		t.Fatalf("实例 A Generate 不应报错: %v", err)
	}
	if _, err := svcB.Generate(ctx, "key-b", SceneLogin); err != nil {
		t.Fatalf("实例 B Generate 不应报错: %v", err)
	}

	if len(aGot) != 1 || aGot[0].Key != "key-a" {
		t.Fatalf("dA 应只收到实例 A 的 key-a 一次, 实际 %+v", aGot)
	}
	bCount := 0
	for _, p := range globalGot {
		if p.Key == "key-a" {
			t.Fatalf("实例 A 的事件不应泄漏到全局分发器: %+v", p)
		}
		if p.Key == "key-b" {
			bCount++
		}
	}
	if bCount != 1 {
		t.Fatalf("全局应恰好收到实例 B 的 key-b 一次, 实际 %d 次", bCount)
	}
}

// TestEventHandlerPanicIsolated 对照 spec「派发失败隔离 - handler panic 不影响主流程」：
// 订阅 EventGenerated 的 handler 内 panic（priority 大值先执行，panic handler 先跑），
// Generate 仍正常返回验证码、无错误（events 包逐 handler recover），且同事件
// 后续收集 handler 照常执行。
func TestEventHandlerPanicIsolated(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()
	svc := New()
	d := events.New()
	svc.SetDispatcher(d)

	var mu sync.Mutex
	var payloads []GeneratedPayload
	d.Subscribe(EventGenerated, 1, func(_ context.Context, _ events.Event) {
		panic("boom")
	})
	d.Subscribe(EventGenerated, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(GeneratedPayload); ok {
			mu.Lock()
			payloads = append(payloads, p)
			mu.Unlock()
		}
	})

	const key, scene = "10800000016", SceneLogin
	code, err := svc.Generate(ctx, key, scene)
	if err != nil {
		t.Fatalf("handler panic 不应影响 Generate: %v", err)
	}
	if len(code) != config.CodeLen {
		t.Fatalf("handler panic 后验证码长度 = %d, 期望 %d", len(code), config.CodeLen)
	}

	if len(payloads) != 1 {
		t.Fatalf("panic handler 之后同事件后续 handler 应照常执行, 实际收到 %d 次", len(payloads))
	}
	if got := payloads[0]; got.Key != key || got.Scene != scene || got.Code != code {
		t.Fatalf("产码载荷不符: %+v（期望 Key=%s Scene=%s Code=%s）", got, key, scene, code)
	}
}

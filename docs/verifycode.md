# verifycode — 验证码（Redis 存储 + 频率限制 + 防爆破锁定）

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `verifycode/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

verifycode 是验证码生成与校验（对应 PHP VerifyCode），管「码的机制」全链路：

- **生成**：安全随机（crypto/rand）按码型产码，Redis 存储带 TTL；
- **频率限制**：同 key + scene 在 `RateInterval` 内只能发一次（Redis SET NX EX 实现）；
- **防爆破锁定**：验证失败计数，达到 `MaxAttempts` 后锁定该 key 一段时间，锁定期间正确码也拒绝；
- **原子消费**：`Verify` 在一次 Redis 脚本内检查锁定、比对并删码 + 清失败计数，同一存储挑战并发至多一次成功。从 v0.15.0 起 `Verify` 成功即消费，不提供非消费开关。

**码怎么送到用户**（短信 / 邮件 / 语音）不在本组件——短信发送用 [sms](sms.md)，本组件只产码与校验，两者在 controller 层衔接（见第 2 节示例）。五类场景常量（登录 / 注册 / 重置密码 / 换绑手机 / 通用）内置，同一手机号在不同场景互不干扰。

产码 / 限频拒绝 / 验证通过三个动作会经 [events](events.md) 派发事件（`verifycode.Generated` / `verifycode.RateLimited` / `verifycode.Verified`），送达通道可由监听器按场景路由（短信 / 邮件 / 语音）——显式编排与事件驱动两种串联形态并存（见第 4 节事件契约、第 5 节 FAQ）。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节），gf Redis 配置就绪（`g.Redis()` 可用）。

```go
package boot

import (
	"github.com/shyun-cc/shw-gf-lib/verifycode"
)

// InitVerifyCode boot 层初始化（可选：不调用则用第 3 节默认值）
func InitVerifyCode() {
	verifycode.SetConfig(verifycode.Config{
		Prefix:       "myapp", // Redis key 前缀（多项目共用 Redis 时隔离命名空间）
		CodeLen:      6,
		TTL:          300,
		RateInterval: 60,
		MaxAttempts:  5,
		LockDuration: 3600,
		FailCountTTL: 3600,
		// CodeType 不设即默认数字码（CodeTypeNumeric）
	})
}
```

发送与校验（controller 层，衔接 [sms](sms.md) 发送）：

```go
package controller

import (
	"errors"

	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/response"
	"github.com/shyun-cc/shw-gf-lib/sms"
	"github.com/shyun-cc/shw-gf-lib/verifycode"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
)

var vcSvc = verifycode.New()

// SendLoginCode 发送登录验证码：Generate 产码（自带频率限制），经 sms 发送
func SendLoginCode(r *ghttp.Request) {
	phone := r.Get("phone").String()

	// 生成：60 秒内同手机号同场景第二次调用返回 ErrRateLimited
	code, err := vcSvc.Generate(r.Context(), phone, verifycode.SceneLogin)
	if err != nil {
		if errors.Is(err, verifycode.ErrRateLimited) {
			// code 99909：验证码发送频率受限（exception 段位常量）
			response.WriteException(r, exception.NewApplication(exception.CodeVerifyCodeRateLimited, "发送过于频繁，请稍后再试"))
			return
		}
		response.WriteException(r, exception.NewInfrastructure(50001, "验证码生成失败"))
		return
	}

	// 发送通道由项目选择：此处示意经 sms 组件发登录场景短信
	if _, err := smsManager.Send(r.Context(), "aliyun", sms.Message{
		Phone: phone,
		Scene: sms.SceneLoginCode,
		Params: map[string]any{"code": code}, // 项目自身模板约定
	}); err != nil {
		// 仅撤销本次码；迟到的发送失败不会删除值不同的新码，不回滚限频。
		if _, revokeErr := vcSvc.InvalidateIfMatches(r.Context(), phone, code, verifycode.SceneLogin); revokeErr != nil {
			g.Log().Warning(r.Context(), "验证码撤销失败") // 不记录码或原始存储错误
		}
		response.WriteException(r, exception.NewInfrastructure(50008, "短信发送失败"))
		return
	}
	response.Success(r, g.Map{})
}

// LoginByCode 登录校验：原子验证并消费，成功后才创建会话
func LoginByCode(r *ghttp.Request) {
	phone := r.Get("phone").String()
	code := r.Get("code").String()

	ok, err := vcSvc.Verify(r.Context(), phone, code, verifycode.SceneLogin)
	if err != nil {
		response.WriteException(r, exception.NewInfrastructure(50001, "验证码校验失败"))
		return
	}
	if !ok {
		// 错误码 / 已过期 / 已锁定统一 false；连续错 5 次后锁定 1 小时（默认值）
		response.WriteException(r, exception.NewApplication(exception.CodeVerifyCodeExpired, "验证码错误或已过期"))
		return
	}

	// ...执行登录（创建会话等，项目自身逻辑，见 docs/session.md）...
	response.Success(r, g.Map{})
}
```

> 上例中 `smsManager` 为项目侧构造的 `sms.Manager`（注册 Provider 后使用，见 [docs/sms.md](sms.md)）；简化演示省略构造。

## 3. Config 参数语义

`verifycode.SetConfig(cfg Config)` 八个字段（boot 层初始化时调用一次；零值字段跳过更新——`Prefix == ""`、数值 `<= 0` 不生效）：

| 字段 | 类型 | 默认 | 语义 |
| --- | --- | --- | --- |
| `Prefix` | `string` | `vc` | Redis key 前缀，所有键以 `<prefix>:` / `<prefix>:rl:` / `<prefix>:fail:` / `<prefix>:lock:` 起头 |
| `CodeLen` | `int` | `6` | 验证码位数 |
| `TTL` | `int` | `300` | 验证码有效期（秒），过期作废 |
| `RateInterval` | `int` | `60` | 同 key + scene 两次发送的最小间隔（秒），窗口内再发返回 `ErrRateLimited` |
| `MaxAttempts` | `int` | `5` | 验证失败次数上限，达到后写锁定键并清失败计数 |
| `LockDuration` | `int` | `3600` | 防爆破锁定时长（秒），锁定期间 Verify 直接 false |
| `FailCountTTL` | `int` | `3600` | 失败计数有效期（秒）——错误尝试的计数窗口 |
| `CodeType` | `int` | `CodeTypeNumeric`（0） | 码型：`CodeTypeNumeric = 0`（每位 0-9，默认）或 `CodeTypeAlnum36 = 1`（每位 0-9a-z 小写） |

五个场景常量（`Scene` 类型）：`SceneLogin`（login）、`SceneRegister`（register）、`SceneResetPassword`（reset_password）、`SceneChangePhone`（change_phone）、`SceneGeneric`（generic）——全部键布局都带 scene 段，同 key 不同场景完全隔离。

## 4. 契约

**生成（Generate）**：先抢频率键（`<prefix>:rl:<scene>:<key>`，SET NX EX RateInterval）——抢不到返回 `ErrRateLimited`（错误文案「发送过于频繁，请稍后再试」）；抢到则产码并以 TTL 写入码键（`<prefix>:<scene>:<key>`），返回明文验证码。**同 key + scene 在 RateInterval 内至多成功一次**；不同 key、不同 scene 互不影响。

**原子校验消费（Verify）**：`func (s *VerifyCodeService) Verify(ctx context.Context, key, code string, scene Scene) (bool, error)`。单次 EVAL 检查锁定键与当前码；锁定、码空或过期返回 `(false, nil)`，不计失败；不匹配时按 MaxAttempts、FailCountTTL 与 LockDuration 原子更新失败计数/锁定；匹配时原子删除码键和失败计数，随后派发一次 Verified，再返回 `(true, nil)`。限频、锁定键不因成功被清除。同一存储挑战的并发调用至多一个成功；重新 Generate 是新的挑战。

**条件撤销（InvalidateIfMatches）**：`func (s *VerifyCodeService) InvalidateIfMatches(ctx context.Context, key, expected string, scene Scene) (bool, error)`。单键 EVAL 仅删除当前值等于 expected 的非空码键；匹配并删除返回 true，不匹配/缺失/过期返回 false。不触碰失败计数、限频、锁定，也不派发事件。用于发送失败补偿，迟到的旧发送失败不会误删值不同的新码；值相同的重发无法区分，需要区分时由项目以独立 key 标识挑战。

Verify / InvalidateIfMatches 的存储故障（含无 EVAL 权限或 Cluster 跨槽）均返回 `(false, error)`，固定错误文案分别为「verifycode: 原子校验消费失败」「verifycode: 条件撤销失败」；不透传或记录可能含挑战值的原始命令错误。调用方遇错不得签发业务身份。网络响应丢失时脚本可能已执行，错误不代表状态未改变；不要自动重放未知结果的业务操作。

**只读查询（CheckRateLimit）**：Generate 后的限频窗口内返回 `(true, nil)`，窗口过期返回 `(false, nil)`；不写码、不重置、不延长限频标记（「发送按钮倒计时」刷新页后恢复状态用）。

**产码随机源**：crypto/rand 安全随机（每位独立在码型字典内均匀取值）——不依赖 math/rand，可预测性风险不引入。随机源故障的兜底是字典首字符（不 fail 停服），概率形态见第 6 节。

**部署兼容性**：Verify / InvalidateIfMatches 使用 Redis/Valkey 支持的 EVAL 基本命令，不依赖 Redis Functions 或 GETDEL。单机及 Sentinel 的当前主节点可直接使用。`Verify` 的 lock/code/fail 三键在 Redis Cluster 中必须同 hash slot；可在项目初始化时为 Prefix 配置 hash tag（如 `{myapp-vc}`），四类键自动保留该 tag。没有同槽布局时返回错误，不退回分步校验。现有键格式保持不变，不自动迁移；修改 Prefix 会使存量验证码与防滥用状态失联，已有项目应规划旧窗口过期及命名空间切换。`InvalidateIfMatches` 只访问一个键，无跨槽问题。需授予 EVAL 及脚本所调用命令的权限。

**Redis 键布局**（scene 隔离的四类键）：

| 键 | 格式 | 结构 |
| --- | --- | --- |
| 码键 | `<prefix>:<scene>:<key>` | String（码明文），TTL = TTL |
| 频率键 | `<prefix>:rl:<scene>:<key>` | String（"1"），TTL = RateInterval |
| 失败计数 | `<prefix>:fail:<scene>:<key>` | String（INCR 计数），TTL = FailCountTTL |
| 锁定键 | `<prefix>:lock:<scene>:<key>` | String（"1"），TTL = LockDuration |

**事件契约**：产码 / 限频拒绝 / 验证通过三个节点各派发一个事件（常量 `EventGenerated` / `EventRateLimited` / `EventVerified`）——

- **`verifycode.Generated`**：Generate 存码成功后派发，载荷 `GeneratedPayload{Key, Scene, Code}`（Code 为明文验证码，边界见下条）；
- **`verifycode.RateLimited`**：Generate 抢不到限频键、返回 `ErrRateLimited` 前派发，载荷 `RateLimitedPayload{Key, Scene}`；
- **`verifycode.Verified`**：Verify 原子消费成功后、返回 true 前派发，载荷 `VerifiedPayload{Key, Scene}`。

三个事件在各自触发路径上每次调用恰好派发一次；Verify 比对不符、码键为空、锁定或存储错误路径不派发；InvalidateIfMatches 不派发；`CheckRateLimit` 是只读状态查询口，不派发任何事件。事件默认经全局默认分发器派发，`SetDispatcher(*events.Dispatcher)` 给服务实例注入独立分发器（不设则走 `events.Default()`）；handler 同步执行、单个 handler panic 被事件包隔离不影响主流程（派发语义见 [docs/events.md](events.md)）。订阅示例（boot 层装配，送达类监听器形态）：

```go
events.Subscribe(verifycode.EventGenerated, 0, func(ctx context.Context, e events.Event) {
	p := e.Payload.(verifycode.GeneratedPayload)
	// p.Key / p.Scene / p.Code：Code 为明文验证码，只应进发送通道
	// ...按 Scene 路由送达通道（短信 / 邮件 / 语音），项目自身逻辑...
})
```

**明文码事件边界**：`GeneratedPayload.Code` 是明文验证码，只应进发送通道（短信模板变量 / 邮件正文），监听方不得将其写日志或透传前端——与 `GetStoredCode` 同款纪律（见第 5 节）。

## 5. FAQ

**Q：Verify 返回 false 有几种可能？怎么区分提示文案？**

三种业务态（锁定 / 码空 / 比对不符）统一 `(false, nil)`，组件不区分——防爆破设计上刻意不给攻击者「码错了还是被锁了」的反馈边界。产品侧要区分提示，可先 `CheckRateLimit` 判限频，锁定态按「操作过于频繁，请稍后再试」话术处理；也可以通过 `GetStoredCode`（测试用途，生产不建议）观察。

**Q：验证通过后码还在 Redis 里，同一码能用第二次吗？**

不能。`Verify` 返回 true 时已原子删除码与失败计数，同一存储挑战再次校验返回 false。多步流程需要保留的是业务证明，迁移要求见第 7 节。

**Q：频率限制、失败锁定分别针对什么攻击？**

频率限制（Generate 侧）：短信轰炸——同手机号 60 秒只能触发一次发送。失败锁定（Verify 侧）：暴力猜码——连续错 5 次锁 1 小时，锁定期间连正确码都拒绝。两者配合 `CheckRateLimit` 的只读查询（前端按钮倒计时）构成完整的防滥用链。

**Q：失败计数会不会「攒着」：今天错 2 次、明天错 2 次、后天错 1 次被锁？**

会，但受 `FailCountTTL`（默认 3600 秒）约束——失败计数自第一次失败起 1 小时后整体清零，跨天的错误不会累计。窗口内连续失败会累计，`Verify` 成功时原子清除失败计数，达到阈值时清计数并创建锁定窗口。

**Q：数字码和 36 进制码怎么选？**

默认数字码（6 位，100 万空间），配合 `MaxAttempts=5` 锁定已够多数场景。36 进制小写码（`0-9a-z`）把同位数空间扩大到 36^n（6 位约 21 亿），适合「位数不想加长、又要更大猜码空间」的场景（如语音播报不便、字符输入无歧义的 App 端）。注意 36 进制含易混淆字符（0/o、1/l），面向用户手输的场景慎用。

**Q：Generate 成功但短信发送失败，用户 60 秒内还能重发吗？**

不能——限频键在 Generate 时已写入，发送通道（sms / 邮件）的成败不影响它。通道故障导致的「码生成了但用户没收到」需等 RateInterval 过后重发；对通道稳定性差的环境，可把 RateInterval 调小或由项目侧对发送失败做补偿（如后台对冲重发），本组件不回滚限频。发送失败可用 InvalidateIfMatches 撤销本次码，但不能据此提前重发。

**Q：多项目共用一个 Redis 会串验证码吗？**

不会，前提是各项目 `SetConfig` 配不同 `Prefix`（默认 `vc` 相同才可能撞）——四类键都带前缀段，前缀即命名空间。

**Q：Generate 返回的明文码直接给前端？**

绝不能。明文码只应进发送通道（短信模板变量 / 邮件正文），接口响应里不回码（上例 Generate 后只返回成功标志）。`GetStoredCode` 是测试观察口，生产代码禁止用它把码透给前端。

**Q：产码后怎么把码送到用户？**

两种形态：controller 显式编排（Generate 返回值进发送通道，见第 2 节示例），或订阅 `verifycode.Generated` 由监听器送达（见第 4 节事件契约）。送达职责同时交给两条路径时用户会收到两条码（一测即现）；怎么分工是业务方的组装决策。

**Q：监听器里做网络调用（如发短信）会拖慢 Generate 吗？**

会。事件 handler 同步执行，送达类监听器的网络耗时并进 Generate 的响应时长；需要异步时由监听方自行处理（起 goroutine 或走 [task](task.md) 组件异步任务）。

## 6. 维护者注记

**机制四件套的键位设计**：四类键（码 / 频率 / 失败 / 锁定）都以 `<prefix>:<scene>:<key>` 为主干，机制段插中间（rl / fail / lock）或空——scene 进主干保证场景隔离免费获得，机制段前缀保证同场景四键互不冲突。改键布局属破坏性变更（Redis 里存量键全部失联），须走 change 流程并评估迁移。

**错误码段位**：本组件消费 exception 三段常量——`CodeVerifyCodeRateLimited`（99909）、`CodeVerifyCodeExpired`（99910）、`CodeVerifyCodeMaxAttempts`（99911），见 [docs/exception.md](exception.md)。组件自身只导出 `ErrRateLimited`（sentinel error，`errors.Is` 判定）；过期与锁定不单独产错（统一 Verify false 形态），段位常量留给调用方组装响应时引用。

**依赖关系**：本组件派发事件经 [events](events.md) 包，verifycode→events 为新增内部依赖；events 是无业务语义的基础包（handler 同步执行、panic 隔离等派发语义见其文档），依赖方向保持单向。

**统一消费入口（#159）**：v0.15.0 将 `Verify` 统一为原子校验消费；移除分叉入口与无条件清理，不提供参数开关。发送失败补偿继续使用 `InvalidateIfMatches`。

**测试形态**（`verifycode/verifycode_test.go`，跑在 internal/testredis 的 miniredis 上，时间场景用 FastForward 快进逻辑时钟，不真 sleep）：默认 6 位数字码且可取回、限频（窗口内二次 ErrRateLimited、异 key 不影响、快进解限）、正确 / 错误码与失败计数增长、达上限锁定且正确码也拒、快进过 LockDuration 解锁恢复、Verify 成功双清且再次校验被拒绝、36 进制字符集（20 轮全属 0-9a-z）、数字码回归、CheckRateLimit 三态只读（对照 key 零写入、窗口内不重置）。事件断言用例组跑在注入 `events.New()` 独立分发器上，对照 change verifycode-events spec 的全部 scenario——恰好一次断言、CheckRateLimit 零派发、独立分发器与全局隔离、handler panic 隔离。

**新增原子回归**（`verifycode/atomic_test.go`，miniredis）：32 并发仅一次成功及 Verified、事件发生在删码后、失败窗口不延长/阈值锁定/解锁、过期和空码拒绝、场景隔离、旧值撤销保留新码与防滥用状态、存储故障脱敏且零 Verified。

**真实 Redis 集成**（`tests/verifycode_atomic_test.go`，`integration` 标签）：`TestVerifyCodeAtomicRedis` 要求 REDIS_ADDR，否则 Skip；经实际 GoFrame adapter 验证 EVAL 参数展开、32 并发一次成功、错误计数、锁定拒绝及条件撤销保留防滥用状态。可在独立的集成测试模块中手动运行。

**随机源故障兜底的权衡**：`rand.Int` 失败时该位取字典首字符（'0'），不向上抛错——验证码可用性优先于停服；crypto/rand 在主流平台故障概率趋零，兜底只是防御式收尾。若安全审计不接受该兜底，改 panic 或错误返回属行为变更，走 change 流程。

## 7. v0.15.0 破坏性迁移

- `Verify(ctx, key, code, scene) (bool, error)` 签名不变，成功行为改为原子消费。已有「只验证、稍后再验证同一码」调用必须调整。
- `VerifyAndConsume` 与无条件 `Consume` 已移除。前者调用改为 `Verify`；原 `Verify` 后的 `Consume` 调用删除。发送失败清理改用 `InvalidateIfMatches` 并处理错误，不能用它绕过锁定或限频。
- 多因子或换绑流程在首次成功 `Verify` 后，由项目创建短期、不可伪造且绑定当前用户/会话、用途、流程实例和旧手机号的业务证明；后续步骤验证并原子消费该证明，不能再次校验已消费的旧码。证明有效期、撤销及业务事务由项目管理，不能用一个未绑定流程的布尔标志代替。
- **换绑手机号接入**：原「验证旧手机号验证码 → 发送新手机号验证码 → 最终再次验证旧码」流程在第二次验旧码处必然失败。消费方须把首次旧码校验结果转成上述流程绑定证明，选择新号码后将目标号码绑定同一流程，最终校验新手机号验证码并消费证明完成换绑。
- 原 Verify 的所有部署现在都需要 EVAL 权限；Redis Cluster 的 lock/code/fail 三键必须同槽，见第 4 节。升级前核对命名空间；不自动迁移存量键或降级为分步校验。

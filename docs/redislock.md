# redislock — Redis 分布式锁（SET NX + owner 安全释放）

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `redislock/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

redislock 是基于 Redis 的分布式锁（对应 PHP RedisLock），管「多实例进程同一时刻只有一个在干活」这一层互斥：获取走 `SET NX`（存在即失败），释放走 Lua 脚本比对 owner——**只有持锁者能删掉自己的锁**，避免 A 超时后 B 持锁、A 回来把 B 的锁误删。

它只做单把锁的获取 / 释放 / 续期 / 强删，不提供可重入、等待队列、watchdog 自动续期——需要「抢不到就排队等」的语义由调用方自行轮询 `Get` 实现。库内它也是 [seqnum](seqnum.md) 恢复流程、worker 进程互斥、wecom/wxapp token 刷新的底层依赖。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节），gf Redis 配置就绪（`g.Redis()` 可用）。

```go
package job

import (
	"context"
	"time"

	"github.com/shyun-cc/shw-gf-lib/redislock"
)

// ReportJob 多实例部署下每天只跑一次的报表任务
func ReportJob(ctx context.Context) {
	// 锁名全局唯一（Redis key 自动加 redis-lock: 前缀）；
	// seconds=300：带过期，持锁进程崩溃后 5 分钟自动复活
	lock := redislock.Create("report-daily", 300)

	// 抢锁：SET NX EX，抢不到说明别的实例在跑，直接退出
	if !lock.Get(ctx) {
		return
	}
	// 用 defer 兜底释放：正常路径主动归还锁，别等 TTL 自然过期
	defer lock.Release(ctx)

	// ...执行任务主体（项目自身逻辑，示意耗时操作）...
	time.Sleep(time.Second)

	// 任务比预期长时续期（仅能续自己的锁，见第 4 节契约）
	if !lock.Extend(ctx, 300) {
		// 续期失败（锁已过期或被他人持有）：应尽快结束本轮，避免无锁裸奔
		return
	}
	// ...继续执行...
}
```

手工释放锁（不限时任务）的形态：`Create(name, 0)` 获取后锁**无过期**（TTL=-1），必须由持锁者 `Release`（或运维 `ForceRelease`）才消失——适合「任务时长不可预估、宁可死锁告警也不让任务并发」的场景，使用纪律见第 4 节。

## 3. Create 参数语义

`redislock.Create(name string, seconds int) *Lock`：

| 参数 | 语义 |
| --- | --- |
| `name` | 锁名。实际 Redis key 为 `redis-lock:` + name，全局唯一由调用方保证；同名锁实例互斥 |
| `seconds` | 过期秒数，三种语义见下表 |

`seconds` 三态（`Get` 的获取行为）：

| 取值 | 获取命令 | 过期语义 |
| --- | --- | --- |
| `> 0` | `SET NX EX <seconds>` | 带过期，持锁者崩溃后最长 seconds 秒自动释放（防死锁） |
| `= 0` | `SET NX` | **无过期**（TTL=-1），持有至持锁者主动释放——持锁进程崩溃则锁永存，须运维 `ForceRelease` 清理 |
| `< 0` | 拒绝获取 | 记错误日志「redislock: 非法 seconds=%d（须 ≥0），拒绝获取锁 %s」并返回 `false`，不触碰 Redis |

`Create` 只构造不获取——每个实例生成独立 owner（UUID），`Owner()` / `Name()` 可读出（`Name()` 含 `redis-lock:` 前缀），`String()` 输出 `Lock{name=..., owner=..., seconds=...}` 形态便于日志排查。

## 4. 契约

**获取**：`Get(ctx) bool`——成功返回 `true`；锁被占（NX 失败）、Redis 故障、seconds 非法一律 `false`（不区分原因，调用方按「没抢到」处理）。同一个 `Lock` 实例可重复 `Get`（自锁重入会失败——owner 相同也过不了 NX）。

**安全释放**：`Release(ctx) bool`——Lua 脚本原子执行「GET 比对 owner 相同才 DEL」：本实例持有返回 `true`；非持锁者（owner 不匹配或锁不存在）返回 `false` 且**不动锁**。这是与裸 `DEL` 的本质区别：不会误删其他持锁者的锁。

**续期**：`Extend(ctx, seconds int) bool`——仅续自己的锁：先 GET 比对 owner，匹配才 `SET XX EX <seconds>` 重置 TTL；owner 不匹配、锁不存在、Redis 故障均返回 `false`。注意实现是「GET 比对 + SET XX EX」两步，非原子脚本——比对通过到 SET 落地之间锁理论上可能刚好过期被他人抢走，此时 SET XX 因键不存在而失败（返回 `false`），安全性由 XX 语义兜底，但调用方必须处理 `Extend` 返回 `false` 的分支（不能假设续期必然成功）。

**强制释放**：`ForceRelease(ctx)`——无条件 DEL，不校验 owner。只用于运维清障（如零秒锁的持锁进程已死）；业务代码里用它等于放弃安全释放契约。

**锁值即 owner**：Redis 中锁的 value 是持有实例的 owner UUID——排查时 `GET redis-lock:<name>` 看到的值可与 `Lock.Owner()` / 日志对上，定位持锁者。

## 5. FAQ

**Q：seconds 该填多少？零秒锁（无过期）什么时候用？**

默认带过期（正秒），TTL 略大于任务最坏耗时，崩溃自愈优先。零秒锁用于「任务时长完全不可预估 / 任务并发比短时死锁更危险」的场景（如某些数据订正作业）——代价是持锁进程崩溃后锁永存，必须人工 `ForceRelease`（或 `redis-cli DEL redis-lock:<name>`）清理，用前想清楚运维路径。

**Q：抢锁失败（Get 返回 false）怎么区分「被占」和「Redis 挂了」？**

区分不了，`Get` 一律 `false`。当前语义是「没抢到就别干活」，Redis 故障时多实例全部静默退出——若业务要求「Redis 故障时降级单实例执行」，需要在调用方包一层对 Redis 的健康探测，本包不代劳。

**Q：锁过期了但任务还没跑完，会发生什么？**

锁自动消失，另一实例可 `Get` 成功，出现**两实例并发执行**。TTL 只是死锁兜底，不是任务并发边界——任务可能超时就 `Extend` 续期，且续期失败必须收尾退出（见第 2 节示例）。

**Q：非持锁者调用 Release 会把锁删掉吗？**

不会。Lua 脚本比对 owner 不匹配直接返回 0，锁原样保留——这是本组件存在的核心理由。

**Q：同一个 Lock 实例 Get 两次能「重入」吗？**

不能。第二次 `SET NX` 因键已存在失败，`Get` 返回 `false`。本包不支持可重入语义。

**Q：为什么 Release 用 Lua 脚本而不是先 GET 再 DEL？**

GET 和 DEL 是两次 Redis 往返，中间锁可能易主（GET 时还是自己的、DEL 时已是别人的），裸 DEL 依旧误删。Lua 脚本在 Redis 端原子执行「比对相同才删」，窗口不存在。

## 6. 维护者注记

**测试形态**（`redislock/lock_test.go`，跑在 internal/testredis 的进程内 miniredis 上，无外部依赖）：首次获取成功且 key 带前缀写入、同名锁互斥、释放后他人立即可取、非持锁者 Release 不动锁（且原持锁者仍可续期）、非持锁者 Extend 不影响原 TTL、ForceRelease 无条件删除、零秒锁 TTL 为 -1 且 owner 释放路径正确、负秒拒绝且不写 key。

**Extend 两步非原子是有意保留的现状**：比对（GET）与续期（SET XX EX）之间无原子性。极端时序下（比对通过 → 锁恰好过期 → 他人抢到 → 本例 SET XX 落地）会把锁的 value 写回本实例 owner 并重置 TTL——真实持锁者随后的 Release 因 owner 不匹配失效，双跑窗口重新打开。窗口极窄（一次 Redis 往返内），现按已知限制记录；若未来证实造成事故，改法是收敛为单条 Lua 脚本（get 比对 + expire，不覆写 value），属行为兼容的加固，走正常 change 流程。调用方层面的纪律：任务主体耗时接近 TTL 时勤续期，`Extend` 返回 `false` 立即收尾退出。

**owner 用 google/uuid**：每实例每锁一个 UUID，`Create` 时生成。锁值与实例生命周期绑定，跨进程重启不复用——重启后旧 owner 丢失，未释放的正秒锁只能等 TTL 过期，这是 SET NX EX 形态的固有代价。

**库内消费方**：seqnum 恢复互斥（`seqnum-recover:` 前缀锁，见 [docs/seqnum.md](seqnum.md)）、worker 进程互斥、wecom/wxapp 的 token 刷新互斥。新增消费方注意锁名前缀隔离，避免与业务锁名撞车（本包统一加 `redis-lock:` 前缀，锁名内部再自带用途前缀是现惯例）。

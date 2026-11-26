# worker — 后台定时任务框架

> 面向项目侧接入者（第 1–6 节）与库维护者（加读第 8 节）。事实来源是 `worker/` 源码，本文的代码示例、签名、环境变量与错误行为均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

后台定时任务框架（对应 PHP hyperf-lib 的 @Process 常驻进程体系），六个角色：

- **`Worker` 接口**：`Name() string` + `Run(ctx) error`，一切后台任务的最小抽象——`Name()` 同时是部署编成的 key（见第 3 节）；
- **`WorkerManager`**：注册多个 Worker 并发生命周期管理（SIGINT/SIGTERM 优雅退出、panic 隔离不殃及同伴），并派发生命周期事件；
- **`BaseWorker`**：裸循环底座 Worker——无间隔死循环 + 每轮独立 traceId + panic 捕获落日志后继续下一轮 + 轮次事件，阻塞型 handle（如 MQ 消费在 handle 里等消息）的底座；
- **`PeriodicWorker`**：定时循环执行的 Worker，可选 `WithRedisLock` 分布式锁防多实例重复执行；
- **`ProcWorker`**：三层进程保护的周期任务（自毁上限 / 锁异常分层释放 / 周期抖动错峰，对应 PHP AbstractLoopProcess / AbstractPeriodicProcess）；
- **`RunEnabled`**：部署编成统一注册入口——经环境变量 `ENABLE_WORKERS` 白名单过滤后启动，是生产部署的唯一启动路径。

依赖方向：worker → redislock + tracex + exception + events（每轮独立 traceId，日志可检索；五事件经 events 派发）。

## 2. 快速接入

### 全量注册（startWorker 一行式）

项目侧定义 `startWorker(ctx)` 把全部 worker（BaseWorker / PeriodicWorker / ProcWorker 混编）经 `worker.RunEnabled` 传入，main 的 worker 角色分支调用：

```go
package main

import (
	"context"
	"os"
	"time"

	"github.com/shyun-cc/shw-gf-lib/worker"
)

// startWorker 全量注册：项目全部 worker 在此一处声明，实际启动哪些由
// 环境变量 ENABLE_WORKERS 白名单决定（见第 3 节）
func startWorker(ctx context.Context) error {
	return worker.RunEnabled(ctx,
		worker.NewPeriodicWorker(
			"sync-orders",
			5*time.Minute,
			func(ctx context.Context) error { return syncOrders(ctx) }, // 项目业务
			worker.WithRedisLock("lock:sync-orders", 300*time.Second),
		),
		worker.NewProcWorker(
			"attachment-archive",
			10*time.Minute,
			func(ctx context.Context) error { return archiveAttachments(ctx) }, // 项目业务
			worker.WithProcLock("lock:attachment-archive", 600),
		),
	)
}

func main() {
	// ... 项目初始化 ...

	// worker 角色分支调用 startWorker（角色名由项目自定，经容器 args 传入）；
	// 阻塞至 SIGINT/SIGTERM 或 ctx 取消
	if len(os.Args) > 1 && os.Args[1] == "worker" {
		if err := startWorker(context.Background()); err != nil {
			panic(err)
		}
		return
	}

	// 其他角色：HTTP 服务等
}
```

`ENABLE_WORKERS` 决定白名单内哪些真正启动：`ENABLE_WORKERS=sync-orders,attachment-archive` 启动全部两个；只配 `sync-orders` 则只启动第一个；未设置或为空则零启动（Warning 后返回，见第 3 节）。

### BaseWorker（裸循环底座）

```go
worker.NewBaseWorker(
	"mq-consumer",                                                    // 名字用 kebab-case，是 ENABLE_WORKERS 的 key
	func(ctx context.Context) error { return consumeMessages(ctx) }, // 阻塞型 handle：在 handle 里等消息
)
```

`Run` 是**无间隔死循环**（轮间无任何等待），逐轮执行 handle；每轮独立 traceId（tracex.New），handle panic 捕获落「Worker %s 执行 panic」错误日志后继续下一轮（不 re-panic），handle 返回 error 落「Worker %s 执行失败」后同样继续；每轮派发 RoundStarted / RoundCompleted 轮次事件（见第 4 节）。

**适用边界（busy-loop）**：节奏控制靠 handle 自身阻塞——handle 是阻塞型（MQ 消费等在 handle 里等消息、每轮自然让出）时适配此形态；非阻塞轮询型 handle 会 busy-loop 空转（轮间无任何等待、CPU 打满），非阻塞轮询任务用 PeriodicWorker（ticker 间隔保证轮间让出）。

**与 PeriodicWorker / ProcWorker 的层次关系**：PeriodicWorker / ProcWorker 均内嵌 BaseWorker 复用其单轮保护壳（RunOnce），在其上叠加策略：

```
单轮保护壳（每轮独立 traceId + panic 处理 + 轮次事件 RoundStarted/RoundCompleted）
├─ BaseWorker      裸循环：无间隔死循环直接执行——panic 落日志继续下一轮
├─ PeriodicWorker  + ticker 轮询间隔 + 可选分布式锁——panic 落日志继续下一轮（同 BaseWorker）
└─ ProcWorker      + 三层进程保护（自毁上限/锁异常分层释放/周期抖动）——panic 强制释放锁后
                    re-panic 上抛，worker 停止交部署编排拉起（见第 6 节）
```

单轮保护壳由 BaseWorker.RunOnce 统一承担（traceId/recover/轮次事件三处埋点收敛一处）；ProcWorker 经 panicRound 钩子把 panic 改为强制释放锁后上抛（区别于默认的落日志轮转）。

### PeriodicWorker（轻保护：仅分布式锁）

```go
worker.NewPeriodicWorker(
	"sync-orders",        // 名字用 kebab-case，是 ENABLE_WORKERS 的 key
	5*time.Minute,        // 周期
	func(ctx context.Context) error { return syncOrders(ctx) },
	worker.WithRedisLock("lock:sync-orders", 300*time.Second), // 可选：多实例防重复执行
)
```

### ProcWorker（三层进程保护）

```go
worker.NewProcWorker(
	"order-sync",         // 单名，kebab-case
	5*time.Minute,        // Interval：相邻两轮起点间隔下界
	func(ctx context.Context) error { return syncOrders(ctx) },
	worker.WithProcLock("lock:order-sync", 300), // 可选：分布式锁（TTL 秒）
)
```

ProcWorker 无自身开关：注册到 WorkerManager 后即随生命周期运行，是否被启动由 `ENABLE_WORKERS` 白名单统一控制（白名单外的 worker 根本不会被注册，见第 3 节）。

## 3. 部署编成（RunEnabled + ENABLE_WORKERS）

`RunEnabled(ctx context.Context, workers ...Worker) error` 是部署编成唯一启动路径：解析 `ENABLE_WORKERS`（逗号分隔白名单）→ 重名校验 → 白名单校验 → 按注册顺序过滤 → 逐个 Register 到 WorkerManager → Start（复用其优雅退出与 panic 隔离）。用法即第 2 节 startWorker 一行式。

### ENABLE_WORKERS 契约（五条）

1. **未设或为空 = 零启动（fail-closed）**：`ENABLE_WORKERS` 未设置、空串或纯空白项时，打 Warning「ENABLE_WORKERS 未设置或为空，未启动任何 worker；如需启动请在环境变量配置白名单，如 ENABLE_WORKERS=order-sync,attachment-archive」后返回 nil，零 worker 启动——「忘了配」的失败模式是无害的零执行；
2. **未知名拒绝启动**：白名单含注册列表之外的名时返回 error「ENABLE_WORKERS 含未知 worker 名: \<名\>（可用: \<全部可用名\>）」拒绝启动，未知名与可用名均逐字列出（可用名按注册顺序逗号连接），零启动；
3. **无通配符**：白名单精确匹配、区分大小写，`all` 不是保留字——启动哪些必须显式逐名列出；
4. **重名拒绝**：注册列表名字重复（全量检查，无论是否在白名单内）返回 error「重复注册 worker 名: \<名\>」；重名检查先于白名单检查——代码 bug 先于配置 bug 暴露；
5. **启动 Info 日志**：「启动 n/m 个 worker: 名单」（n 为实际启动数、m 为注册总数，名单按注册顺序逗号连接）。

### 命名约定（kebab-case）

worker 名用 kebab-case（如 `order-sync`、`attachment-archive`）。`Name()` 是部署编成的 key：运维在 `ENABLE_WORKERS` 里配置的名字与代码完全一致才生效（区分大小写精确匹配）——大小写不一致会在启动时按未知名拒绝，错误信息同时含 env 原名与注册名，便于对出哪里不一致。

### 部署形态

K8s worker Deployment 经 env 注入 `ENABLE_WORKERS`；worker 与 api 等角色共用同一镜像，角色经容器 args 切换、不动 command（详细部署约定见部署侧 skill，本文不展开）。

## 4. 事件

worker 链路五事件经 events 组件派发：生命周期三事件由 WorkerManager 埋点，轮次两事件由 BaseWorker / PeriodicWorker / ProcWorker 埋点。事件名与载荷是对外契约。

| 常量 | 事件名 | 载荷 | 触发时机 |
| --- | --- | --- | --- |
| `EventWorkerStarted` | `worker.Started` | `WorkerStartedEvent{Names []string}` | WorkerManager 启动完成（全部 worker goroutine 已拉起）恰好派发一次；Names 为实际启动名单（按注册顺序） |
| `EventRoundStarted` | `worker.RoundStarted` | `RoundEvent{Name, Duration, Err}` | 单个 worker 一轮执行开始（锁获取成功后起算）；Duration / Err 为零值 |
| `EventRoundCompleted` | `worker.RoundCompleted` | `RoundEvent{Name, Duration, Err}` | 单轮执行结束；Duration 为库内测量时长，Err 为 handle 返回值 |
| `EventWorkerPanicked` | `worker.Panicked` | `WorkerPanickedEvent{Name, Recovered}` | worker 的 Run 内 panic（任何来源）被 WorkerManager recover；Recovered 为捕获的 panic 值 |
| `EventWorkerStopped` | `worker.Stopped` | `WorkerStoppedEvent{Name, Err}` | 单个 worker 退出；Err 为 Run 返回值，nil 表示 Run 未返回错误（正常退出，或 panic 退出时 Run 无返回值——判定异常退出看 Panicked 是否先发） |

载荷字段（`worker/events.go`）：`RoundEvent{Name string; Duration time.Duration; Err error}`、`WorkerPanickedEvent{Name string; Recovered any}`。

订阅（全局默认分发器）：

```go
events.Subscribe(worker.EventRoundCompleted, 0, func(ctx context.Context, e events.Event) {
	ev := e.Payload.(worker.RoundEvent)
	// ev.Name / ev.Duration / ev.Err
})
```

### Dispatcher 注入

生命周期事件经 `WorkerManager.SetDispatcher(d *events.Dispatcher)` 注入；轮次事件经 PeriodicWorker / ProcWorker 的 `Dispatcher` 字段或 BaseWorker 的 `WithBaseDispatcher(d)` 选项注入。不注入均降级 `events.Default()`（进程级默认分发器，与 sms 组件同一先例）：

```go
d := events.New() // 独立分发器（测试或事件域隔离用）
d.Subscribe(worker.EventWorkerStopped, 0, func(ctx context.Context, e events.Event) { /* ... */ })

m := worker.NewWorkerManager()
m.SetDispatcher(d) // 生命周期事件（Started/Panicked/Stopped）走 d

w := worker.NewPeriodicWorker("sync-orders", 5*time.Minute, handle)
w.Dispatcher = d // 轮次事件（RoundStarted/RoundCompleted）同样可注入
```

### 轮次时长与 watchdog

**时长以 Duration 字段为准**：事件是同步派发的——`RoundStarted` 的某个 handler 若执行慢，会推迟 `RoundCompleted` 的派发时刻，用两个事件的 `DispatchedAt` 相减会高估时长。时长的唯一权威是 `RoundEvent.Duration`（库内测量：handle 前起算，不含轮间等待）。同理，事件 handler 在派发方 goroutine 内同步执行，慢 handler 会拖慢 worker 的轮次节奏——handler 应快速返回，重活自起 goroutine 处理。

watchdog 参考形态（订阅 Started 记 map、Completed 删除、后台扫描悬挂轮即卡死告警；悬挂判定阈值是项目决策，库不内置）：

```go
// 参考形态：轮次 watchdog
var mu sync.Mutex
running := make(map[string]time.Time)

d.Subscribe(worker.EventRoundStarted, 0, func(ctx context.Context, e events.Event) {
	name := e.Payload.(worker.RoundEvent).Name
	mu.Lock()
	running[name] = time.Now()
	mu.Unlock()
})
d.Subscribe(worker.EventRoundCompleted, 0, func(ctx context.Context, e events.Event) {
	name := e.Payload.(worker.RoundEvent).Name
	mu.Lock()
	delete(running, name)
	mu.Unlock()
})
// 后台 goroutine 周期扫描：running 中超过阈值未移除的名即疑似卡死，告警
```

### 派发边界

- **锁跳过的轮零事件**：`RoundStarted` 在锁获取成功后才派发，锁竞争跳过的轮不产生任何轮次事件（watchdog 不会误报）；
- **handle panic 的轮有 Started 无 Completed**：panic 直接走 defer 链（ProcWorker 先强制释放锁），该轮无 Completed。此后各基础类路径不同——ProcWorker 的 panic re-panic 传播出 Run，归生命周期事件管（Panicked + Stopped，worker 停止）；BaseWorker 与 PeriodicWorker 的 panic 被 runOnce 自身 recover 记日志、worker 继续跑（下一轮 RoundStarted 会刷新 watchdog 时间戳，悬挂自愈，不触发 Panicked）。

## 5. ProcWorker 参数语义

`NewProcWorker(name string, interval time.Duration, handle func(ctx context.Context) error, opts ...ProcOption)` 构造（name 即 `Name()`，kebab-case），其余参数经字段或选项注入：

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `Interval` | time.Duration | 周期（相邻两轮起点间隔下界）；构造参数，导出字段 |
| `MaxLoops` | int | >0 启用循环上限自毁：执行轮数达限以 `CodeProcessIterationMax` 段位码错误退出 |
| `LimitMemoryMB` | int | >0 启用内存上限自毁：每轮前后读数达限以 `CodeProcessMemoryLimit` 退出 |
| `MemFunc` | func() int | 内存读数注入，默认 runtime.MemStats.Alloc（MB）；测试注入假读数 |
| `StartupDelay` | int | 秒；-1 随机 [1,5] 错峰启动、0 不延迟（默认）、>0 定值延迟 |
| `LockKey` / `LockTTL` | string / int | WithProcLock 设置；非空启用 redislock 分布式锁（TTL 秒） |
| `Clock` / `Rand` / `Sleep` | 函数 | 时间/随机/睡眠注入，默认 time.Now / rand.Intn / time.Sleep；测试不真睡 |
| `Dispatcher` | *events.Dispatcher | 轮次事件分发器注入；nil 走 events.Default()（见第 4 节） |

## 6. 契约（三层进程保护）

1. **自毁上限**：MaxLoops / LimitMemoryMB 达限即退出并返回携带段位码的错误（不静默、不无限循环）；由部署编排（k8s 一类）负责拉起重置内存态；
2. **锁异常分层释放**：handle 正常完成走 owner 安全释放；handle panic 走强制释放（不等 TTL）后 re-panic——锁不因异常悬挂到 TTL 尽头；
3. **周期抖动错峰**：相邻两轮起点间隔 ≥ interval（抖动只延长不缩短，0..interval/10 随机）；多实例同配置部署不会同秒齐发。

每轮执行独立 traceId（tracex.New，BaseWorker 与 PeriodicWorker、ProcWorker 相同），日志链路按轮次可检索。panic 语义：BaseWorker 与 PeriodicWorker 单轮 panic 被 recover 记日志、下一轮照常、worker 不退出；ProcWorker handle panic 强制释放锁后 re-panic 传播出 Run，由 WorkerManager recover（派发 `worker.Panicked`）后该 worker 停止——自毁的另一条触发路径，同样交由部署编排拉起。

## 7. FAQ

**Q：BaseWorker、PeriodicWorker 与 ProcWorker 选哪个？**

先看 handle 形态再选：阻塞型 handle（在 handle 里等消息/等连接，每轮自然让出）配 BaseWorker 的无间隔死循环；非阻塞轮询型 handle 配 PeriodicWorker（ticker 间隔保证轮间让出，直接放 BaseWorker 会 busy-loop 占满 CPU）；ProcWorker 与 PeriodicWorker 同为周期形态，差异只剩保护等级——PeriodicWorker 只有分布式锁，ProcWorker 在锁之上有自毁上限 + 锁异常分层释放 + 周期抖动错峰三层保护。基础类选择是编码时按 handle 形态与保护等级做的决策，不是部署层的事——启动哪些 worker 由 `ENABLE_WORKERS` 统一控制，三类一视同仁。

**Q：handle 返回 error 会怎样？**

记「Worker %s 执行失败」错误日志（三类基础类经 BaseWorker 单轮保护壳统一此文案），下一轮照常执行——周期任务的失败语义是「本轮作废」，重试策略由业务自己决定（需要重试语义的任务用 `task` 组件）。

**Q：handle panic 会怎样？**

BaseWorker 与 PeriodicWorker：单轮 panic 被 recover 记「Worker %s 执行 panic」，下一轮照常、worker 不退出。ProcWorker：panic 同样先记「Worker %s 执行 panic」，随后强制释放锁并 re-panic 传播出 Run，WorkerManager recover（派发 `worker.Panicked`）后该 worker 停止，由部署编排拉起。

**Q：WorkerManager.Start 阻塞吗？**

阻塞：起完全部 Worker goroutine 后等待，收到 SIGINT/SIGTERM 经 context 取消让各 Worker 退出，全部停止后返回。放在 main 末尾即可。

**Q：没获取到锁的那次执行，日志在哪？**

PeriodicWorker 记 Debug 级「Worker %s 未获取到锁，跳过本次执行」；ProcWorker 静默跳过本轮。锁竞争是分布式部署的常态而非异常，故不刷 Error；锁跳过的轮不派发任何轮次事件（见第 4 节）。

## 8. 维护者注记

**多实例重复执行**：同一周期任务部署在多个实例时，使用 WithRedisLock 或 WithProcLock 避免重复执行。

**时间与随机全注入**：ProcWorker 的 Clock/Rand/Sleep 与 MemFunc 全部可替换，单测以假时间源与假随机断言抖动区间、启动延迟三态与自毁时机，不真睡（被测路径的 Sleep 注入为 no-op、Clock 假时钟快进；测试对异步信号的等待为带超时上限的毫秒级轮询）。

**改动规矩**：ENABLE_WORKERS 五条契约（第 3 节）与 kebab-case 命名约定是对外部署契约；五事件的事件名与载荷（第 4 节）是对外 API 契约，事件名或载荷字段改名即 breaking；段位码（CodeProcessIterationMax / CodeProcessMemoryLimit）是对外可观察错误。三者改动须走 change 流程并同步本文档。

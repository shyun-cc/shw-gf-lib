# events — 事件分发器（多 handler / 优先级 / panic 隔离）

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `events/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

events 是进程内事件分发器（PSR-14 语义的 Go 化），管「一件事发生了，关心它的各方各自处理」的解耦：**发布方**只管喊一声「短信发送失败」，**监听方**（记日志、发告警、更新统计）各自订阅，两边互不认识。

核心解耦契约：库内组件作为发布方只 import events 派发事件，**不 import 任何监听方**；业务 handler 由项目在 boot 层（启动装配阶段）订阅注册。例如 [sms](sms.md) 发送失败时派发 `sms.Failed`，要不要告警、告警发到哪，是项目订阅时才决定的事——库不感知。

同一事件可注册任意多个 handler，按优先级有序同步执行；单个 handler panic 不影响其余 handler。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节）。本组件无外部依赖（不依赖 Redis / DB）。订阅放 boot 层（程序启动时执行一次的装配代码），发布点在业务或库内组件：

```go
package boot

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/sms"
)

// RegisterEventHandlers boot 层装配：订阅库内组件与项目自身的事件。
// 包级 Subscribe/Dispatch 走进程级默认分发器（events.Default() 单例）
func RegisterEventHandlers() {
	// 短信发送失败：记告警日志（优先级 100，大值先执行）
	events.Subscribe(sms.EventFailed, 100, func(ctx context.Context, e events.Event) {
		if p, ok := e.Payload.(sms.SendEventPayload); ok {
			// p.Phone / p.Scene / p.Result 为失败现场（Result.Success == false）
			// ...项目自身的告警动作（示意：写日志/通知值班群）...
			_ = p
		}
	})

	// 同一事件可再挂 handler：失败计入监控统计（优先级 10，在上面的 handler 之后执行）
	events.Subscribe(sms.EventFailed, 10, func(ctx context.Context, e events.Event) {
		// ...统计上报（示意）...
	})
}
```

项目自身业务事件的发布与订阅（自定义事件名按「域.动作」命名，如 `order.paid`）：

```go
package service

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/events"
)

// OrderPaid 支付完成后发布事件：关心方（发货、积分、消息）各自订阅，发布方零依赖
func OrderPaid(ctx context.Context, orderID int64) {
	events.Dispatch(ctx, "order.paid", map[string]interface{}{"order_id": orderID})
}
```

## 3. 订阅与派发 API 语义

**事件对象**：

```go
type Event struct {
	Name         string    // 事件名，区分事件
	Payload      any       // 载荷，类型由发布方与订阅方约定
	DispatchedAt time.Time // 派发时刻
}

type Handler func(ctx context.Context, e Event)
```

**两套入口，一个语义**：

| 入口 | 形态 | 适用 |
| --- | --- | --- |
| 包级 `Subscribe(name string, priority int, handler Handler)` / `Dispatch(ctx context.Context, name string, payload any)` | 进程级默认分发器（`Default()` 单例） | 业务全局装配——boot 层订阅、业务点发布，绝大多数场景用这套 |
| `New()` 创建独立 `*Dispatcher`，方法 `Subscribe` / `Dispatch` 签名同上 | 独立实例，订阅表互不干扰 | 测试隔离、事件域隔离（库内 [sms](sms.md) 的 `SetDispatcher` 注入独立实例就是此用途） |

**priority 参数**：整数，**大值先执行**；同优先级按注册顺序（稳定排序）。执行顺序在订阅时即排定，与派发时机无关。

**Dispatch 同步语义**：派发是同步调用——`Dispatch` 返回时该事件全部 handler 已执行完毕，发布方代码里在 Dispatch 之后的逻辑可以依赖 handler 副作用已完成。无订阅者的事件派发直接返回，不报错。

## 4. 契约

**优先级有序**：派发一次，该事件全部 handler 各执行恰好一次，顺序为 priority 降序（100 → 10 → 1），同优先级按注册先后。

**panic 隔离**：单个 handler 执行期间 panic 不会中断同事件后续 handler，也不会向发布方传播（`Dispatch` 无返回值，发布方无从感知 handler 失败）；panic 事实记入错误日志，格式 `[EVENTS] handler panic 事件=%s: %v`。因此：handler 内的业务错误要么自己消化（记日志），要么显式做成异步补偿任务——不能指望 panic 给发布方报错。

**载荷透传**：`Payload` 为 `any` 原样透传，分发器不做类型约束与拷贝；订阅方用类型断言取（见第 2 节示例）。约定发布方用固定 payload 结构导出（如 `sms.SendEventPayload`），订阅方断言失败分支要有兜底。

**并发安全**：订阅与派发可并发（订阅表 RWMutex 保护，派发遍历注册切片快照）；但派发中注册的新 handler 不参与本次派发。

**handler 幂等纪律**：`Dispatch` 不保证恰好一次交付以外的任何重试语义——一次派发就是一次执行。handler 若触发下游动作（发消息、扣积分），自身考虑幂等以应对业务层的重放调用。

## 5. FAQ

**Q：为什么订阅要放在 boot 层，不能在业务代码里随用随订？**

订阅是装配不是逻辑——放业务点会导致重复注册（每次调用多挂一个 handler）与执行顺序不可预期。boot 层一次性装配，订阅关系在进程生命周期内稳定可见，这正是「发布方零依赖监听方」的另一半：监听关系集中在一处 review。

**Q：handler 里能再 Dispatch 别的事件吗？**

可以（同步嵌套派发，注意别成环——A 派 B、B 派 A 会栈溢出）。需要「事件链」时优先考虑是否真的需要事件，还是直接函数调用更清晰。

**Q：handler 执行慢会阻塞发布方吗？**

会。同步派发下 `Dispatch` 等 handler 跑完才返回——慢 handler 直接拖慢发布点。耗时动作（外部 HTTP 调用、大批量写）应在 handler 里投递给项目的异步机制（如 [task](task.md) 表驱动任务），handler 本身只做轻量分发。

**Q：priority 该怎么分配？**

约定俗成：百位量级给「告警/审计」类必须最先跑的 handler，十位给业务处理，个位给统计类收尾。数值本身无魔法，团队内保持量级惯例即可；同优先级靠注册顺序，尽量别依赖它表达语义（要顺序就用不同 priority）。

**Q：怎么在单测里验证「我的 handler 被触发了」？**

用 `events.New()` 建独立分发器（不污染全局单例），订阅后 Dispatch，断言 handler 内记录的状态；或在订阅 handler 里写 chan，测试端带超时等待。库内包测试（events_test.go）即前一种形态。

**Q：库内组件现在派发哪些事件？**

[sms](sms.md)：`sms.Sent` / `sms.Failed`（载荷 `SendEventPayload`）；[verifycode](verifycode.md)：`verifycode.Generated` / `verifycode.Verified` / `verifycode.RateLimited`（产码 / 验证通过 / 限频拒绝三节点，载荷 `GeneratedPayload` / `VerifiedPayload` / `RateLimitedPayload`）；[worker](worker.md)：`worker.Started` / `worker.RoundStarted` / `worker.RoundCompleted` / `worker.Panicked` / `worker.Stopped`（manager 启动 / 轮次起止 / panic / 退出五个观察点，载荷 `WorkerStartedEvent` / `RoundEvent` / `WorkerPanickedEvent` / `WorkerStoppedEvent`）；[wecom](wecom.md)：回调事件族 26 个常量（通讯录变更族、外部联系人三族、审批 / 日程 / 会议 / 客服 / 获客等回调）+ 消息兜底 `wecom.msg.<MsgType>`（经 `HandleCallback` 的 dispatcher 参数派发，传 nil 则不派发）。新组件接入事件按「组件名.动作」命名并导出常量。

## 6. 维护者注记

**PSR-14 语义 Go 化的取舍**：PHP PSR-14 有 Event / Listener / Provider / Dispatcher 四角色，本包收敛为 Event / Handler / Dispatcher 三者——Go 无容器自动装配，订阅关系显式注册在项目 boot 层即是等价物。`Default()` 单例对齐 PHP 容器单例语义，`New()` 独立实例覆盖测试隔离需求。

**发布方零依赖监听方的结构保证**：库内组件包只 import events（如 sms、wecom），依赖方向单一——`go list -deps` 可验证组件包依赖清单不含其他业务包（既有白名单 worker→redislock、middleware→response+session 除外）。给组件加「发事件」能力时禁止顺手 import 调用方，那是把解耦倒着写回去。

**同步派发是刻意的**：异步队列（goroutine / channel / 持久化）会引入「handler 还没跑完 Dispatch 就返回了」的时序复杂度与丢失语义，超出进程内分发器的职责。需要异步、可靠、可重试的事件消费，用 [task](task.md) 表驱动任务承载，事件 handler 只做投递。

**panic 只记日志不外抛**：`run` 内 recover 后以 `g.Log().Errorf` 记 `[EVENTS] handler panic 事件=%s: %v`——发布方的业务路径不该被无关订阅方拖垮，这是隔离契约的代价面：handler 的失败对发布方不可见，监控要看日志。

**测试形态**（`events/events_test.go`，纯逻辑无外部依赖）：三优先级顺序执行、同优先级注册顺序、panic 隔离（最高优先级 panic 不影响后续两个）、无订阅者安全派发、载荷透传、事件名定向（只触发目标事件的 handler）、Event 三字段完整。

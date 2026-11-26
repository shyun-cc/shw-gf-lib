# tracex — traceId 上下文载体（otel SpanContext）

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `tracex/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

tracex 是 traceId 的上下文载体：以 OpenTelemetry SpanContext 承载 traceId 存进 `context.Context`，与 GF 的 glog / gtrace 原生取值机制**同源**——glog 打日志时就是从 ctx 的 SpanContext 取 TraceID 输出。选这个载体意味着：同一 ctx 里，日志行的 traceId、[response](response.md) 出口的 traceId 字段、任何第三方 otel 组件读到的 traceId，天然是同一个值，无需任何桥接。

它只提供三个函数（纯 context 操作，不引入 TracerProvider、不开网络上报）：

- HTTP 请求场景的入口注入由 [middleware](middleware.md) 的 Trace 中间件完成（内部就是 `Ensure`）；
- [response](response.md) 全部 JSON 出口的 traceId 字段经 `Ensure` 同源取值；
- 非 HTTP 场景（worker / 命令行任务）由项目直接调用 `Ensure` / `New` 获得独立 traceId。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节）。

**HTTP 场景：多数项目唯一要做的动作是挂 Trace 中间件**（应绑在中间件链最前，网关已注入则沿用不覆盖），此后响应 traceId 与请求期间全部日志同值——接线见 [docs/middleware.md](middleware.md)。

**非 HTTP 场景（worker / 命令）直接操作 ctx**：

```go
package jobs

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/tracex"

	"github.com/gogf/gf/v2/frame/g"
)

// RunOnce 单次任务：惰性取 traceId——首次生成，同 ctx 后续取用同值
func RunOnce(ctx context.Context) {
	ctx, tid := tracex.Ensure(ctx)
	g.Log().Infof(ctx, "任务开始, traceId=%s", tid)
	// 后续所有经此 ctx 的日志自动携带同一 traceId
}

// RunLoop 周期任务：每轮独立 trace 用 New 强制新值（覆盖上轮残留）
func RunLoop(ctx context.Context) {
	for {
		roundCtx := tracex.New(ctx)
		g.Log().Infof(roundCtx, "本轮处理开始")
		// ... 本轮全部日志同一 traceId，与上一轮互不相同
	}
}
```

只读场景（如把 traceId 写进对下游的请求头）用 `FromCtx`：

```go
tid := tracex.FromCtx(r.Context()) // 无注入时返回空串，不生成、不污染
```

## 3. 三 API 语义

| 函数 | 签名 | 语义 |
| --- | --- | --- |
| `FromCtx` | `func FromCtx(ctx context.Context) string` | 纯读：ctx 已有有效 traceId 则返回其 32 位小写 hex 串；无（或 ctx 为 nil）返回空串。绝不生成、绝不改写 ctx |
| `Ensure` | `func Ensure(ctx context.Context) (context.Context, string)` | 惰性保证：已有有效 traceId 则**原样沿用并返回原 ctx**（上游/网关注入场景不覆盖）；否则合成注入新 SpanContext 并返回**新 ctx**（原 ctx 不被污染） |
| `New` | `func New(ctx context.Context) context.Context` | 强制生成新 traceId 注入 ctx，**覆盖已有值**——周期任务每轮独立 trace 用 |

选择口诀：**请求链路 Ensure（能用上游的不自己造）、每轮隔离 New、只看不写 FromCtx**。

## 4. 契约

- **同源贯穿**：同一请求生命周期内，中间件入口 `Ensure` 注入后，response 全部 JSON 出口与请求期间 glog 日志行取到同一个 traceId；两个不同请求的 traceId 互不相同；
- **上游优先**：请求 ctx 已存在合法 traceId（网关/上游注入）时 `Ensure` 沿用原值且返回原 ctx，不重建、不覆盖；
- **惰性且稳定**：`Ensure` 对无 traceId 的 ctx 首次调用生成新值，同 ctx 后续调用得到同值；
- **形态约定**：traceId 为 32 位小写十六进制串（otel TraceID 标准形态），SpanContext 带 `FlagsSampled`；
- **失败边界**：熵源（crypto/rand）读取失败时直接 panic（「tracex: crypto/rand 不可用: ...」）——系统级故障不降级、不允许带着空 traceId 静默继续。

## 5. FAQ

**Q：HTTP 项目需要直接调用 tracex 吗？**

一般不需要。挂 `middleware.Trace` + 规范路由后整条链路（日志、响应、异常日志）已同源；只有要在 handler 里显式拿 traceId 传给下游时才用 `FromCtx(r.Context())`。

**Q：`Ensure` 和 `New` 怎么选？**

`Ensure` 是「保证有、有则沿用」，适合请求链路与单次任务；`New` 是「强制换新」，唯一正当场景是周期任务每轮独立 trace。在请求链路里误用 `New` 会切断与上游的 trace 关联。

**Q：为什么不基于 TracerProvider / OpenTelemetry SDK？**

traceId 只用于日志关联与报障索引，不上报追踪后端。SpanContext 载体已满足「与 glog 同源」，引入 Provider 纯增依赖。未来若接全链路追踪，把入口注入换成真 Provider 即可，取值方（glog、response、tracex.FromCtx）零改动。

**Q：traceId 是什么格式？能自己造一个塞进去吗？**

32 位小写 hex（otel TraceID 形态）。不要绕过本包直接拼 ctx——glog 只认 otel SpanContext 里的合法 TraceID，非法形态等于没有 traceId。

**Q：上游网关传来的 traceId 怎么接？**

要求上游以 otel 标准注入请求 ctx（HTTP header 传播协议），`middleware.Trace` / `Ensure` 自动沿用；不要自己解析 header 再转塞。

## 6. 维护者注记

**选型**：合成 SpanContext（`trace.NewSpanContext` + `ContextWithSpanContext`）而非自造 map key 存 ctx，是整个设计的关键一步——glog 从 ctx 取 TraceID 的机制是 otel 原生的，同源是「免费」获得的，任何自造载体都要桥接。

**依赖纪律**：本包直接依赖仅 `go.opentelemetry.io/otel/trace`，是 GF 既有间接依赖转直接，无新增模块；保持这个边界，不引入 otel SDK / exporter。

**panic 而非降级**：crypto/rand 读失败意味着系统熵池故障，静默降级会产生「看似正常但无 trace 关联」的日志，排查代价远高于快速失败。panic 文案前缀 `tracex: ` 便于定位来源。

**测试锁住的行为**（`tracex/tracex_test.go`，纯逻辑）：Ensure 生成 32 位 hex 且同 ctx 稳定、原 ctx 不被污染；上游已注入时 Ensure 沿用并返回原 ctx；New 两次互不相同且覆盖已有值；FromCtx 无注入返回空串。

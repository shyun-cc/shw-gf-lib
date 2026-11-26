# logging — 全系统 JSON 单行日志格式

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `logging/` 源码，本文的代码示例、签名与输出格式均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

logging 把 glog 的日志输出收敛为**单行 JSON**（字段契约见第 3 节），供日志采集侧（ES / Loki 等）按字段检索，替代文本日志的人肉 grep。格式是本组件的全部职责——日志写到哪（文件路径）、记什么级别、怎么轮转，仍归项目自身的 logger 配置（见第 4 节）。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节）。两行接线：boot 层 `logging.Setup()` 一次，入口注入 traceId。

```go
package boot

import (
	"github.com/shyun-cc/shw-gf-lib/logging"
)

// InitLogging boot 层初始化：幂等，重复调用无副作用
func InitLogging() {
	logging.Setup()
}
```

traceId 注入入口（决定 `id` 字段何时有值）：

| 入口 | 接线 | 效果 |
| --- | --- | --- |
| HTTP 请求 | `s.Use(middleware.Trace)` | 同一请求内日志与响应 traceId 同源（机制见 [docs/middleware.md](middleware.md)） |
| TCP 连接 | `middleware.RecoverConnFunc(name, handler)` | 连接级 traceId + panic 守卫（见下方 TCP 形态） |
| 后台任务 | worker 每轮自动独立 traceId | 轮次级链路可检索（见 [docs/worker.md](worker.md)） |

HTTP 形态：

```go
package main

import (
	"github.com/shyun-cc/shw-gf-lib/middleware"

	"github.com/gogf/gf/v2/frame/g"
)

func main() {
	s := g.Server()
	s.Use(middleware.Trace) // 链最前注入 traceId（挂载形态与顺序说明见 docs/middleware.md）
	// ... 路由绑定（项目业务）...
	s.Run()
}
```

TCP 形态——`RecoverConnFunc` 包装 gtcp 连接 handler（`gtcp.NewServer` 第二参数的裸函数形态），同时是 TCP 入口的 panic 守卫：连接处理链上任一 panic 被捕获，落一条 `[TCP PANIC] conn=<name>: <panic 值>` 形态的错误日志（带 traceId 与堆栈）后正常返回——单连接异常只终结该连接，进程不崩，后续连接继续服务：

```go
package main

import (
	"github.com/shyun-cc/shw-gf-lib/middleware"

	"github.com/gogf/gf/v2/net/gtcp"
)

func main() {
	srv := gtcp.NewServer(":9000", middleware.RecoverConnFunc("order-tcp", func(conn *gtcp.Conn) {
		// 连接处理（项目业务）：阻塞收发、处理、返回；
		// 此链路上的 panic 由守卫捕获落日志，不会打崩进程
		_ = conn
	}))
	srv.Run()
}
```

接线完成后，业务日志的 kv 用法（kv 对落 `ct` 字段）：

```go
g.Log().Info(ctx, "登录成功", "phone", "<手机号码>", "scene", "login")
// {"id":"<traceId>","lv":"INFO","dt":"2026-08-24 10:00:00.123456","mg":"登录成功","ct":{"phone":"<手机号码>","scene":"login"}}
```

## 3. 字段契约

每条日志输出单行 JSON，字段以固定序排列：

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `id` | string | 链路标识 traceId（ctx 内 OpenTelemetry SpanContext 经 glog 透传）；无则省略，不伪造 |
| `lv` | string | 日志级别，glog 原生 4 字符缩写原样透传（DEBU / INFO / NOTI / WARN / ERRO / CRIT / PANI / FATA） |
| `dt` | string | 日志时间，微秒精度（`2006-01-02 15:04:05.000000`） |
| `mg` | string | 消息正文；调用参数 Values 为奇数个时首个转字符串以空格并入（对齐 glog 内置 HandlerStructure 的兜底语义） |
| `ct` | object | 结构化上下文：`Info(ctx, "msg", "k1", v1, "k2", v2)` 形态的 kv 对落这里；恒输出，无配对时为 `{}` |
| `st` | string | 堆栈文本，glog 自动采集堆栈的场景（错误级日志等）非空才输出 |

`ct` 的 value 在组装时逐个经 json.Marshal 探测：chan / func / NaN / 循环引用等不可序列化的毒值降级为 `fmt.Sprintf("%v")` 字符串——任何输入下单条日志不 panic、不丢整条。

## 4. 格式与目的地正交

`logging.Setup()` 只决定**格式**：未显式 `SetHandlers` 的日志实例全部走 JSON，含 `g.Log()` 全局单例、ghttp server 自建实例（access log / error log / HTTP panic 恢复日志）、命名实例 `g.Log("x")`。**写到哪**不受影响：日志文件路径、级别过滤、轮转仍走项目配置文件的 logger 节点。glog 的 `Handlers` 字段带 `json:"-"`（配置文件配不了）——格式经代码内 `Setup()` 生效，无法被配置覆盖。

## 5. 边界

**显式 `SetHandlers` 的实例脱离兜底**：业务对某实例显式 `SetHandlers` 后，该实例的格式由其自配的 handlers 链决定，不再走本组件的 JSON handler；其余未显式配置的实例不受影响。

**`Setup()` 幂等**：内部 sync.Once，boot 层与测试重复调用均无副作用。

**panic 的兜底范围（Go 语言边界）**：recover 只捕获本 goroutine 及其同步调用链。入口 goroutine 已由各入口形态兜住——HTTP handler（ghttp 内建 recover）、TCP 连接（`RecoverConnFunc`）、worker 轮（BaseWorker / PeriodicWorker 的轮级 recover）；库内自起子 goroutine 的 panic 独立传播，是 Go runtime 层行为，外层无语言手段兜底，进程退出——编排层重启（如 K8s）是最后防线。

## 6. 维护者注记

**hyperf-lib 对照**：字段名沿用其超缩写——id / lv / dt / mg / ct 与 hyperf-lib 日志字段一一对应；其 ch / sq / mu / ci 未采纳（PHP / Swoole 特有语义，Go 侧无对应物）。

**不重蹈 glog 内置 HandlerJson 覆辙**：glog 自带的 JSON handler 在 marshal 失败时直接 panic（一条毒值日志能打崩进程）；本组件的 jsonHandler 对 ct 逐值探测净化，兜底路径丢弃 ct 重序列化（纯 string 字段必然成功），最终 Marshal 必然成功（见包 doc「序列化防御」）。

**改动规矩**：字段契约（id / lv / dt / mg / ct / st 的名字、语义、JSON 序）是对外契约（日志采集侧按字段建索引），改动即 breaking、走 change 流程并同步本文档。

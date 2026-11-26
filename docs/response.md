# response — 统一 JSON 响应体

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `response/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

response 是全库统一的 JSON 出口（对应 PHP hyperf-lib 的 Res）。所有 HTTP 响应落成同一四字段结构：

```json
{"traceId": "...", "code": 0, "message": "success", "data": {...}}
```

它解决三件事：

- **出口统一**：成功 / 业务错误 / 异常分层 / HTTP 状态码语义响应，全部同构四字段体；
- **异常分层落地**：`WriteException` 按 [exception](exception.md) 的类型分流 HTTP 状态码（Domain→422 / Application→200 / Infrastructure→500）；
- **traceId 同源**：每个出口的 `traceId` 字段与请求期间日志同值（取值机制见 [docs/tracex.md](tracex.md)），前端报障时拿响应里的 traceId 可直接对日志。

日常接线中本组件通常经 [middleware](middleware.md) 的 Response 中间件自动接管，项目侧手写调用主要出现在旧式函数路由。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节）。

**形态一（推荐）：规范路由 + Response 中间件自动接管**。controller 返回 `(res, err)`，响应封装由中间件完成（完整接线见 [docs/middleware.md](middleware.md)）：

```go
s := g.Server()

group := s.Group("/")
group.Middleware(middleware.Response)
group.GET("/detail", func(ctx context.Context, req *DetailReq) (res *DetailRes, err error) {
	// 成功：err==nil → {"traceId":"...","code":0,"message":"success","data":{...}}
	return &DetailRes{Id: 7, Title: "订单"}, nil
})
group.GET("/pay", func(ctx context.Context, req *PayReq) (res *PayRes, err error) {
	// 业务异常：Application 层 → HTTP 200 + code 40001
	return nil, exception.NewApplication(40001, "余额不足")
})
```

**形态二：旧式函数路由手写出口**：

```go
s.BindHandler("/legacy", func(r *ghttp.Request) {
	response.Success(r, g.Map{"way": "old"})
})
```

**boot 层（main 启动时）按项目约定设置校验失败的 code**（默认 400）：

```go
response.SetValidationErrorCode(400)
```

## 3. 出口函数与参数语义

手动出口函数一览（`r *ghttp.Request` 均为第一参）：

| 函数 | HTTP | code | message | data |
| --- | --- | --- | --- | --- |
| `Success(r, data)` | 200 | 0 | `"success"` | 透传 |
| `SuccessWithMessage(r, data, message)` | 200 | 0 | 自定义 | 透传 |
| `Error(r, message, code)` | 200 | 自定义 | 自定义 | `{}` |
| `ErrorWithData(r, message, code, data)` | 200 | 自定义 | 自定义 | 透传 |
| `WriteSuccess(r, res)` | 200 | 0 | 见下 | 见下 |
| `WriteException(r, err)` | 见第 4 节分流表 | | | |
| `Unauthorized(r, message)` | 401 | 401 | 自定义 | `{}` |
| `Forbidden(r, message)` | 403 | 403 | 自定义 | `{}` |
| `ServerError(r, message)` | 500 | 99999 | 自定义 | `{}` |

`WriteSuccess` 供规范路由统一响应中间件调用，两个可选接口让项目 Res DTO 定制输出（不实现则全默认）：

- `MessageCarrier`（`GetMessage() string`）：非空返回值替换默认 `"success"`；
- `RawDataCarrier`（`GetRawData() interface{}`）：返回值替换整个 `data`（`data` 为 `nil` 时输出 `{}`）。

## 4. 契约

**WriteException 分层分流表**（入口判定顺序：AppError → 校验错误 → 兜底）：

| 错误形态 | HTTP | code | data | 日志级别 |
| --- | --- | --- | --- | --- |
| `*exception.AppError` Type=domain | 422 | `NormalizedCode()` | 原样（`Data` 为 nil 时输出 `null`） | Warning `[DOMAIN]` |
| `*exception.AppError` Type=infrastructure | 500 | `NormalizedCode()` | `{}`（内部细节不透出） | Error `[INFRA]` |
| `*exception.AppError` 其余（application） | 200 | `NormalizedCode()` | `Data` 为 nil 时 `{}`，否则透传 | Info `[APP]` |
| gvalid 校验失败（`gcode.CodeValidationFailed`） | 200 | `SetValidationErrorCode` 设置值 | `{}` | Info `[VALIDATION]` |
| 未捕获普通 error | 500 | 99999（`exception.FallbackErrorCode`） | `{}`，message 固定「服务器内部错误」 | Error `[UNCAUGHT]` |

**traceId 始终在场**：全部出口（含上表全部分支）的 `traceId` 经 `tracex.Ensure` 从请求 ctx 同源取值——挂了 Trace 中间件则与请求期间日志同值，未挂时惰性生成稳定值，保证响应永远携带 traceId（机制见 [docs/tracex.md](tracex.md)）。

**ID 字段的出口形态**：Go 侧 `uuid.UUID` 序列化为字符串，天然无 JS 2^53 精度问题，出口无需任何特殊处理；本包历史上为雪花 ID 提供的 `SnowflakeID` 类型已移除（v0.4.0 BREAKING）。

## 5. FAQ

**Q：业务错误为什么 HTTP 200，不返回 4xx/5xx？**

约定：HTTP 状态码表达传输层语义，业务成败由 `code` 表达。Application 层异常（余额不足、登录过期）是「请求成功送达、业务答复为否」，前端按 code 分支处理；只有 Domain（422）/ Infrastructure（500）两类才动 HTTP 状态码。全表见第 4 节。

**Q：`Error` 和抛 AppError 有什么区别？**

`Error`/`ErrorWithData` 是直接手写出口（旧式函数路由用）；规范路由下应返回 `exception.NewApplication(...)` 由中间件分流，两者最终落成的响应体同构。

**Q：响应里 message 想按 DTO 定制怎么做？**

Res 结构体实现 `MessageCarrier`（`GetMessage() string`）；同理 `RawDataCarrier` 定制整个 data 输出。仅在经 `WriteSuccess` 的规范路由链路生效。

**Q：traceId 有时和日志对不上？**

检查路由是否挂了 `middleware.Trace`（见 [docs/middleware.md](middleware.md)）。没挂时 response 惰性生成的 traceId 也能保证响应携带，但与请求期间日志不同值；挂上后同请求的响应与全部日志同值。

## 6. 维护者注记

**WriteException 的状态码映射**：历史上 Domain 异常落 200 导致前端把规则违规当成功处理。三层映射（422/200/500）与日志级别（Warning/Error/Info）一一对应，改动任一映射属对外契约变更，走 change 流程并同步本文档。

**99999 字面量禁止出现在本包**：`ServerError` 与未捕获兜底均引用 `exception.FallbackErrorCode`（见 [docs/exception.md](exception.md)），常量唯一来源在 exception 包。

**Domain 与 Application 对 nil Data 的输出差异是有意的**：Domain 分支 `data` 原样透传（nil → JSON `null`），供前端定位违规字段；Application 分支 nil 补 `{}` 保持成功/失败形态对称；Infrastructure 恒 `{}` 不泄内部细节。

**测试形态**：response 函数内部经 `WriteJsonExit`/`WriteStatus` + `r.Exit()` 退出（ghttp 以 panic 实现），必须经真实 HTTP server 路径调用——单测以 ghttp 本地随机端口 server + 标准库客户端跑全部分支（成功三态、WriteException 六分支、401/403/500、SetValidationErrorCode 参数化），不能在测试里直接调用这些函数。

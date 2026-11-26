# exception — 分层异常与库内错误码体系

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `exception/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

exception 是全库统一的业务异常载体（对应 PHP hyperf-lib 的 DomainException / ApplicationException / InfrastructureException 三层）。它只做两件事：

- **分层**：`AppError` 携带异常类型（domain / application / infrastructure），把「这个错误属于哪一层」编码进异常本身；
- **错误码**：维护库内十六段错误码常量（99901–99916）与兜底码 99999，库内组件抛异常时直接引用，不自造码值。

它**不负责**把异常变成 HTTP 响应——分层到 HTTP 状态码的映射由 [response](response.md) 组件的 `WriteException` 执行（Domain→422 / Application→200 / Infrastructure→500），通常经 [middleware](middleware.md) 的 Response 中间件自动接管，项目侧 controller 只管返回错误。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节）。controller 里按错误所属层选择构造器，直接 `return`：

```go
package controller

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/exception"

	"github.com/gogf/gf/v2/frame/g"
)

// OrderCancelReq / OrderCancelRes 项目侧 api 包定义的请求/响应结构（示意）
type OrderCancelReq struct{}
type OrderCancelRes struct{}

// OrderCancel 取消订单：规范路由 handler 形态，返回 (res, err)。
// 订单数据来自项目自身的查询，此处仅示意判定分支。
func OrderCancel(ctx context.Context, req *OrderCancelReq) (res *OrderCancelRes, err error) {
	order := loadOrder() // 项目自身的数据来源

	// 领域规则违规：订单状态不允许取消 → 前端收到 HTTP 422
	if order.Status != "pending" {
		return nil, exception.NewDomain(42202, "订单状态不允许取消")
	}

	// 应用层业务失败：余额不足 → HTTP 200，前端按 code 分支提示
	if order.Balance < order.Amount {
		return nil, exception.NewApplication(40002, "余额不足")
	}

	// 带附加数据：前端可高亮出错字段
	if !order.OwnerVerified {
		return nil, exception.NewWithData(40004, "请先实名认证", g.Map{"verified": false})
	}

	return &OrderCancelRes{}, nil
}
```

基础设施故障（数据库 / Redis / 第三方服务不可用）用第三层：

```go
// redis 连接失败 → HTTP 500
return nil, exception.NewInfrastructure(50001, "redis 连接失败")
```

项目侧不需要手写任何响应封装——路由挂上 `middleware.Response` 后（接线见 [docs/middleware.md](middleware.md)），返回的 `*AppError` 会被自动按下表落成响应：

| 构造器 | 异常类型 | HTTP 状态码 | 前端感知 |
| --- | --- | --- | --- |
| `NewDomain(code, message)` | `domain` | 422 | 表单/业务规则错误，message 直接可展示 |
| `NewApplication(code, message)` | `application` | 200 | 业务失败，按 code 分支处理 |
| `NewInfrastructure(code, message)` | `infrastructure` | 500 | 服务异常，message 落日志、响应体同形 |

## 3. 构造器与错误码语义

`AppError` 结构（四个字段全部导出）：

```go
type AppError struct {
	Code    int
	Message string
	Data    interface{}
	Type    string // domain / application / infrastructure
}
```

构造器族一览（均为 `func(code int, message string) *AppError`，`NewWithData` 多一个 `data interface{}`）：

| 构造器 | 语义 |
| --- | --- |
| `NewDomain` | 领域异常（HTTP 422） |
| `NewApplication` | 应用异常（HTTP 200，前端通过 code 区分） |
| `NewInfrastructure` | 基础设施异常（HTTP 500） |
| `New` | 创建业务异常（默认应用异常，HTTP 200）。向后兼容已有代码——与 `NewApplication` 行为完全等价 |
| `NewWithData` | 在应用异常基础上携带 `Data` 附加数据（如出错字段定位），响应体 `data` 字段原样透传 |

两个方法：

- `Error() string`：输出 `"[code] message"` 格式（如 `[1001] 库存不足`）；
- `NormalizedCode() int`：规范化错误码——`Code != 0` 返回原值（含负数原样返回），`Code == 0` 兜底返回 `FallbackErrorCode`（99999）。

## 4. 契约

**三层 → HTTP 状态码映射**（由 [response](response.md) 的 `WriteException` 执行）：Domain→422、Application→200、Infrastructure→500。异常在本包构造、在 response 分流，两包职责分离。

**库内十六段错误码（99901–99916，段位语义对齐 hyperf-lib LibErrorCode）**——库内组件抛异常的 code 取自对应段位常量，项目侧引用而不要自造 999xx 码值：

| 常量 | 值 | 语义（所属组件） |
| --- | --- | --- |
| `CodeSeqNumGenFailed` | 99901 | 序列号生成失败（seqnum） |
| `CodeFileUploadFailed` | 99902 | 文件上传失败（storage/attachment） |
| `CodeAliyunServiceError` | 99903 | 阿里云服务错误（aliyun） |
| `CodeProcessLockError` | 99904 | 进程锁错误（worker） |
| `CodeProcessSignalError` | 99905 | 进程信号错误（worker） |
| `CodeProcessIterationMax` | 99906 | 进程循环次数上限（worker） |
| `CodeProcessMemoryLimit` | 99907 | 进程内存超限（worker） |
| `CodeSmsServiceError` | 99908 | 短信发送服务异常（sms） |
| `CodeVerifyCodeRateLimited` | 99909 | 验证码发送频率受限（verifycode） |
| `CodeVerifyCodeExpired` | 99910 | 验证码已过期（verifycode） |
| `CodeVerifyCodeMaxAttempts` | 99911 | 验证码验证次数超限（verifycode） |
| `CodeWecomServiceError` | 99912 | 企微/微信服务错误（wecom/wxapp） |
| `CodeAttachmentStoreError` | 99913 | 附件存储错误（attachment） |
| `CodeExpressServiceError` | 99914 | 快递服务错误（express） |
| `CodeKd100ServiceError` | 99915 | Kd100 服务错误（express/kd100） |
| `CodeRegionServiceError` | 99916 | 行政区划服务错误（region） |

**兜底码**：`FallbackErrorCode = 99999`，两个用途——`NormalizedCode` 对零码 `AppError` 的兜底，与 response 对未捕获普通 error 的兜底响应 code。**项目自身业务码自选段位**（如 40001/42202/50001），避开 99901–99916 与 99999 即可。

## 5. FAQ

**Q：Domain 为什么映射 HTTP 422 而不是 400？**

422（Unprocessable Entity）语义是「格式合法、语义违规」——请求结构没问题，但违反业务规则（库存不足、状态不允许取消）。400 留给参数校验失败，gf 的 gvalid 校验错误走 [response](response.md) 的 `SetValidationErrorCode` 通道，不经过本包。

**Q：code 忘了设（传 0）会怎样？**

`NormalizedCode()` 把 0 兜底为 99999，前端不会收到莫名的 `code: 0` 成功形态错误体。`Error()` 字符串仍输出原始的 `[0] message`（日志里可辨认）。

**Q：`New` 和 `NewApplication` 用哪个？**

完全等价（`New` 是收编前代码的向后兼容入口）。新代码建议用 `NewApplication` 显式表意。

**Q：AppError 怎么变成 HTTP 响应的？**

不需要手动转换。规范路由挂 `middleware.Response`（见 [docs/middleware.md](middleware.md)）后，handler 返回的 `*AppError` 由中间件调 `response.WriteException` 按层分流；旧式函数路由可手写 `response.WriteException(r, err)`（见 [docs/response.md](response.md)）。

**Q：上层代码怎么判断错误是不是某个 AppError？**

`AppError` 是具体类型，用标准库 `errors.As` 解链后读字段：

```go
var appErr *exception.AppError
if errors.As(err, &appErr) {
	fmt.Println(appErr.NormalizedCode(), appErr.Message, appErr.Type)
}
```

**Q：错误信息想带结构化数据给前端，用什么？**

`NewWithData(code, message, data)`，`data` 会出现在响应体 `data` 字段（Application 层）或随 Domain 层原样透传；Infrastructure 层不透传 data（内部细节不给前端）。

## 6. 维护者注记

**分层对应 PHP 原型**：domain / application / infrastructure 三常量逐字对齐 hyperf-lib 的 DomainException / ApplicationException / InfrastructureException；十六段常量对齐其 LibErrorCode 段位表。上游段位调整时本包须同步评审。

**99999 字面量唯一来源**：`FallbackErrorCode` 只在 exception 定义一处；response 的 `ServerError` 与未捕获兜底均引用该常量，包内不得出现 99999 字面量（存量由测试锁住）。

**99901–99916 是库内保留段**：后续新增组件需要抛异常时，先在十六段表内找归属（多数段位一对多组件共享语义）；确需新段位时扩展常量表并同步本文档第 4 节表格。既有组件逐步把裸码值迁到段位常量，不强制一次性回改。

**测试覆盖**（`exception/exception_test.go`，纯逻辑无外部依赖）：三个分层构造器写入正确 Type 并透传 Code/Message；`New` 向后兼容等价于 `NewApplication`；`NormalizedCode` 非 0 原样（含负数）、0 兜底 99999；`Error()` 的 `"[code] message"` 格式；`NewWithData` 携带 Data；十六段常量值 99901–99916 逐段断言与段数恰为 16。

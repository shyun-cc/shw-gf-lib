# express — 快递物流抽象（Provider 接口 + kd100 实现）

> 面向项目侧接入者（第 1–3 节）与库维护者（加读第 4 节）。事实来源是 `express/` 源码，本文的代码示例、接口方法与签名规则均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

快递物流抽象：**`Provider` 9 方法接口**（对应 PHP ExpressProviderInterface）+ 子包 `express/kd100`（快递100 开放平台自主复刻客户端，零厂商 SDK）。业务参数一律 `map[string]any` 透传——快递平台业务字段（快递公司编码表、收件人字段名）是平台决策，库不做校验。

接口分两域：

- **追踪域 3 方法**：`Track`（实时轨迹，com+num）/ `Subscribe`（订阅推送）/ `HandleTrackingCallback`（推送验签）；
- **下单域 6 方法**：`CreateOrder` / `CancelOrder` / `ModifyOrder` / `GetOrderDetail` / `QueryPrice` / `HandleOrderCallback`。

## 2. 快速接入

```go
package main

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/express"
	"github.com/shyun-cc/shw-gf-lib/express/kd100"
)

func main() {
	var p express.Provider = kd100.New("<KEY>", "<SECRET>", "<CUSTOMER>")
	ctx := context.Background()

	// 实时查轨迹（com=快递公司编码如 zhongtong，num=运单号）
	trace, err := p.Track(ctx, "zhongtong", "75312345678900")

	// 寄件下单（param 字段见快递100 文档：kuaidicom/sender/recMan 等）
	order, err := p.CreateOrder(ctx, map[string]any{
		"kuaidicom": "zhongtong",
		"recMan":    map[string]any{"name": "收件人", "mobile": "<手机号码>", "printAddr": "收件地址"},
		"sendMan":   map[string]any{"name": "寄件人", "mobile": "<手机号码>", "printAddr": "寄件地址"},
		// …其余业务字段透传
	})

	// 物流推送回调验签（param=回调原始 param 字段 JSON 串，sign=回调签名）
	err = p.HandleTrackingCallback(paramJSON, sign)
}
```

## 3. kd100 签名与行为契约

**双签名规则**：

| 域 | 签名 | 表单字段 |
| --- | --- | --- |
| 查询/订阅类 | `sign = UPPER(MD5(paramJSON + key + customer))` | customer / sign / param |
| 下单类 | `sign = UPPER(MD5(paramJSON + t + key + secret))`，t 为秒级时间戳 | key / sign / t / param |

请求形态：表单 POST（application/x-www-form-urlencoded）到 `https://poll.kuaidi100.com`。

**重试语义**：HTTP 异常或响应非合法 JSON 重试至多 3 次（含首次）、间隔 2s（可注入）；**合法 JSON 但业务 `result=false` 不重试**、原样返回由上层判断——业务失败重试无益。

**回调验签**：恒时比较（crypto/subtle），防时序侧信道；追踪域与下单域各自 Handle 方法（下单类验签带 t 参数参与）。

## 4. FAQ

**Q：怎么接其他快递平台（快递鸟等）？**

项目实现 `express.Provider` 9 方法接口即可替换——业务层只依赖 `express.Provider`，平台差异收敛在实现内。

**Q：Subscribe 订阅后推送打到哪？**

param 里的 callback_url 是项目提供的 HTTP 端点；收到推送后调 `HandleTrackingCallback`（验签）再自行解析 param JSON（data 字段是轨迹明细）。

**Q：result=false 时返回什么？**

正常返回（map 里有 result/status/message 等平台字段、err 为 nil）——库不把业务失败翻译成 error，由上层按平台语义判断（不同平台失败字段不统一）。

**Q：Track 的 resultv2 参数是什么？**

kd100 实现内部固定带 `"resultv2": "4"`（轨迹完整度 v4 格式），调用方无须传。

## 5. 维护者注记

**编译期接口锁定**：`var _ express.Provider = (*kd100.Client)(nil)`——kd100 实现漂移（方法签名改坏）编译期即爆，不靠 review。

**透传边界**：map[string]any 透传是刻意取舍——各平台字段差异大且演进快（新增增值服务字段），强类型化会让库跟着平台文档跑；校验责任在平台侧（错误响应会带明确 message）。

**测试**：包内固定向量单测（双签名 UPPER(MD5) 对齐 PHP 向量、表单字段全集、重试仅对 HTTP/JSON 异常、回调恒时比较）+ httptest。

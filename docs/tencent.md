# tencent — 腾讯地图 WebService 客户端

> 面向项目侧接入者（第 1–3 节）与库维护者（加读第 4 节）。事实来源是 `tencent/` 源码，本文的代码示例、签名算法与错误行为均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

腾讯地图 WebService API 自主复刻客户端，零厂商 SDK。覆盖两类用法：

- **直接用**：`Get(ctx, path, query)` 调任意 WebService 接口（geocoder / place / district…），自动补 key 与 SK 签名；
- **经 region 组件**：行政区划采集（docs/region.md）内部用本客户端拉数。

SK 签名**两步分离**（与「参数整体 urlencode 再签名」的错误做法不同，这是腾讯实测语义）：

1. 签名用**原始未编码值**——参数按名升序、`k=v` 原始值拼 querystring，`签名串 = path?querystring + SK`，md5 转小写；
2. HTTP 请求时参数**正常 urlencode 编码**发送，sig 追加到 query。

## 2. 快速接入

```go
package main

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/tencent"
)

func main() {
	c := tencent.New("<KEY>", "<SK>") // SK 为空则不签名（兼容无 SK 的 key）

	payload, err := c.Get(context.Background(), "/ws/geocoder/v1", map[string]string{
		"address": "北京市海淀区中关村大街1号",
	})
	if err != nil {
		return // err 已含重试后仍失败的完整上下文
	}
	// payload 是完整响应 map（status/message/result 原始字段）
	result := payload["result"].(map[string]any)
	location := result["location"].(map[string]any) // 经纬度
	_ = location
}
```

成功判定：`status == 0`（数值全等）；status ≠ 0、HTTP 异常或响应非 JSON 均视为失败——**自动重试至多 3 次**（间隔 2s，可注入），全部失败返回末次错误。

## 3. 参数语义

| 字段 | 语义 |
| --- | --- |
| `Key` / `SK` | WebService key 与签名 SK；SK 空 → 只补 key 不签名 |
| `HTTPClient` | `*http.Client` 注入 |
| `BaseURL` | 覆盖默认 `https://apis.map.qq.com`（测试用） |
| `Sleep` | 重试间隔注入（默认 time.Sleep；测试注入假 sleep） |

`Get` 的 query 参数自动补 `key`（调用方已传则不覆盖）；SK 非空时自动算 `sig` 追加。

## 4. FAQ

**Q：为什么签名用原始值、发送才编码？**

腾讯平台的实测语义：服务端拿原始值重算签名，若用编码后的值签名会对不上。两步分离是官方文档语义的忠实实现，不是实现瑕疵。

**Q：重试会重试哪些失败？**

status ≠ 0（限频/参数错）、HTTP 网络异常、响应非 JSON——三类都重试。注意 status≠0 的参数错误重试 3 次也救不回来但无害（多耗 2 次配额），平台限频（status=120）反而靠重试间隔自愈。

**Q：错误信息长什么样？**

三层：单次失败「HTTP 请求异常: …」「响应非合法 JSON: …」「腾讯 API 返回错误 status=%v message=%v」；重试耗尽包装为「腾讯地图 API 请求失败（\<path\> 尝试 3 次后仍失败）: \<末次错误\>」。

## 5. 维护者注记

**为什么独立成公开包**：region 采集要用，但地图能力（逆地理编码等）其他场景也可能用——不做 region 的私有子包，平铺为独立组件（与 aliyun 同级同形态）。

**FormatStatus 调试辅助**：status 字段字符串形态（float64 → int 字符串），排错时打印用。

**测试**：包内 httptest 单测（签名两步分离断言：签名串用原始值、请求 query 用编码值；status 判定；重试计数与注入 sleep）。

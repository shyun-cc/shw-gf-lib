# aliyun — 阿里云 OpenAPI 自主复刻客户端

> 面向项目侧接入者（第 1–4 节）与库维护者（加读第 5 节）。事实来源是 `aliyun/` 源码，本文的代码示例、签名、算法与错误行为均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

阿里云 OpenAPI 自主复刻客户端（ACS3-HMAC-SHA256 V3 签名），**零厂商 SDK 依赖**——签名与 hyperf-lib PHP 侧逐字段对齐。包内现成两条业务链：

- **Sms**（dysmsapi.aliyuncs.com，RPC 风格）：POST 业务参数走 URL query、body 空、成功判定宽容（Code 空或 "OK"）；
- **Sts**（sts.aliyuncs.com，ROA 风格）：POST 参数走 JSON body、成功判定严格（响应不得带 Code 字段）。

其他阿里云 API：用 `Client.Do` 自组 Request（Method/Host/Action/Version/Query/Body），签名算法对任意 V3 接口通用。

## 2. 快速接入

```go
package main

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/aliyun"
)

func main() {
	client := aliyun.NewClient(aliyun.Credentials{
		AccessKeyID:     "<AK>",
		AccessKeySecret: "<SK>",
		RegionID:        "cn-hangzhou", // 可空，Sts 链缺省 cn-hangzhou
	})

	ctx := context.Background()

	// 短信（一般经 sms 组件的 NewAliyunProvider 间接使用，见 docs/sms.md；直用亦可）
	res, err := aliyun.NewSms(client).SendSms(ctx,
		"<手机号码>", "签名", "SMS_123456789",
		map[string]any{"code": "123456"}, // 模板变量；无变量传 nil
	)
	// res.Success / res.MessageID(BizId) / res.ErrorCode / res.ErrorMessage

	// 发送明细查询：发送后拿 MessageID(BizId) 查终态（最小闭环）
	qres, err := aliyun.NewSms(client).QuerySendDetails(ctx,
		"<手机号码>", // 单手机号（厂商仅支持单号查询）
		"20260901",    // 发送日期，请求格式 yyyyMMdd
		res.MessageID, // BizId；空串 = 不限流水
		10, 1,         // pageSize / currentPage，原样透传不校验
	)
	// qres.Success / qres.TotalCount / qres.Details / qres.ErrorCode / qres.ErrorMessage
	// 明细终态：d.SendStatus 与 aliyun.SmsStatusWaiting / SmsStatusFailed / SmsStatusSuccess 比对

	// STS 临时凭证
	creds, err := aliyun.NewSts(client).AssumeRole(ctx,
		"acs:ram::1234567890:role/app",   // roleArn
		"app-session",                    // roleSessionName
		nil,                              // policy 内联策略（可空 = 继承角色全部权限）
		3600,                             // expireSeconds ≤0 时取 3600
	)
	// creds.Credentials 四字段：AccessKeyID / AccessKeySecret / SecurityToken / Expiration
}
```

## 3. 参数语义

`Credentials` 与 `Client` 可注入项：

| 字段 | 语义 |
| --- | --- |
| `Credentials.SecurityToken` | 可空；非空时注入 `x-acs-security-token` 头（STS 凭证再调用场景） |
| `Client.HTTPClient` | `*http.Client` 注入（代理 / 超时定制） |
| `Client.BaseURL` | 整包覆盖请求基址（默认 `https://{host}`；httptest 取证用） |
| `Client.Now` / `Client.Nonce` | 时间源 / nonce 源注入（默认 time.Now UTC 化 / 32 位 hex 随机） |

`Request` 结构：`Method` / `Host` / `Path`（空视为 /）/ `Action`（x-acs-action）/ `Version` / `Query`（url.Values）/ `Body`（JSON []byte，nil 即空 body）。

`QuerySendDetails` 入参语义：

| 参数 | 语义 |
| --- | --- |
| `phone` | 手机号；厂商仅支持单手机号查询 |
| `sendDate` | 发送日期，请求格式 `yyyyMMdd`（如 20260901）——与响应明细 `SendDate` 的 `yyyy-MM-dd HH:mm:ss` 不对称，格式错会被厂商拒绝或静默查空 |
| `bizID` | SendSms 返回的 MessageID（BizId），空串表示不限流水；厂商仅支持单 BizId |
| `pageSize` / `currentPage` | 原样透传（int 转 string），不做范围校验与默认值填补，越界由厂商拒绝并经 `ErrorCode` 带回 |

## 4. 契约

**ACS3 签名**（`Client.Do` 内部完成，对外不可见）：

```
CanonicalRequest = METHOD \n CanonicalURI \n CanonicalQueryString \n
                   CanonicalHeaders \n (空行) \n SignedHeaders \n HashedPayload
StringToSign     = "ACS3-HMAC-SHA256\n" + hex(sha256(CanonicalRequest))
Signature        = hex(hmacSha256(AccessKeySecret, StringToSign))
Authorization    = ACS3-HMAC-SHA256 Credential={AK},SignedHeaders={hdrs},Signature={sig}
```

参与签名的头：`host` / `content-type`（有 body 时）/ 全部 `x-acs-*`，小写字典序。query 编码对齐 PHP rawurlencode（空格 `%20` 非 `+`、豁免 `~`）。

**成功判定**：`Do` 返回 `Response{Success, StatusCode, RequestID, Body, ErrorCode, ErrorMessage}`——HTTP 2xx 且 Code 空或 "OK" 即 Success（RPC 宽容语义）；Sts 链在自身层再收紧（Code 非空即败）。

**错误包装**：HTTP 层错误带中文前缀——「阿里云API请求失败: 」「读取响应失败: 」「构造请求失败: 」，均 `%w` 包装可解链。业务失败不报 error，看 `Response.ErrorCode/ErrorMessage`。

**QuerySendDetails 契约**：与 SendSms 同链同构——ACS3 签名、POST 业务参数走 URL query、body 空、宽容成功判定复用。错误心智与 SendSms 一致：Go error 仅网络/本地失败；厂商拒绝不返回 error，而是 `Success=false` 的 Result（`ErrorCode/ErrorMessage` 带回）。结果 `QuerySendResult{Success, TotalCount, Details, RequestID, ErrorCode, ErrorMessage}`；空态返回空切片（`Details` 恒非 nil，len=0 可直接 range）。`TotalCount` 为**提交维度计数（非计费维度）**，双形态解析——厂商文档标注 string、而 Client 的 json 解码把 number 解为 float64，string 走 strconv.Atoi、number 走 float64，其余形态（含缺失/解析失败）容错为零值（TotalCount 与 SendStatus 共用该解析）。

**QuerySendDetails 厂商约束**：仅支持单手机号、单 BizId、查询最近 30 天、PageSize 1~50、单账号 QPS 5000/秒——越界由厂商拒绝并经 `ErrorCode` 带回；本包 pageSize/currentPage 原样透传（int 转 string），不做范围校验与默认值填补。

**明细字段与终态**：`QuerySendDetail{Phone, SendDate, ReceiveDate, SendStatus, ErrCode, TemplateCode, Content}`——`SendDate` 为厂商响应格式 `yyyy-MM-dd HH:mm:ss`（与请求参数 sendDate 的 `yyyyMMdd` 不对称，格式错会被厂商拒绝或静默查空）；`ReceiveDate` 未回执为空串；`ErrCode` 为运营商状态码，成功为 DELIVERED；`TemplateCode` 测试模板发送时厂商不返回。`SendStatus` 取值（对齐厂商枚举）：

| 常量 | 值 | 语义 |
| --- | --- | --- |
| `SmsStatusWaiting` | 1 | 等待回执 |
| `SmsStatusFailed` | 2 | 发送失败 |
| `SmsStatusSuccess` | 3 | 发送成功 |

## 5. FAQ

**Q：为什么不用阿里云官方 SDK？**

库定策：厂商 SDK 依赖面大（一堆传递依赖）且抽象不可控。本包签名算法与 PHP 侧逐字段对齐、经固定向量取证（SetFixedDateNonce 注入固定 date/nonce 独立重算签名比对），跨语言行为一致。

**Q：`SetFixedDateNonce` 生产能用吗？**

不能。它是签名向量取证专用（固定时间/nonce 才能重算比对），生产调用会造成 nonce 重放风险。

**Q：发短信为什么推荐走 sms 组件？**

sms 组件（docs/sms.md）在 aliyun 之上补了场景→模板映射、多供应商切换、发送事件——单发一条短信直用 `aliyun.NewSms` 也完全可以。

**Q：其他阿里云 API（OSS 除外）怎么调？**

`client.Do(ctx, aliyun.Request{...})` 自组：填 Host（API 域名）、Action、Version、Query 或 Body。V3 签名通用；OSS 用 storage 组件（docs/storage.md）不走本包。

## 6. 维护者注记

**双链差异是 PHP 行为对齐**：Sms 宽容判定（Code=OK）与 Sts 严格判定（无 Code）来自 hyperf-lib 两个 Service 的实测语义，迁移期不可「统一」——改判定语义会改变上游容错行为。

**urlEsc 三步替换**：Go `url.QueryEscape` 空格编 `+`、转义 `~`，与 PHP rawurlencode 两处不一致——`+`→`%20`、`%7E`→`~` 两步替换对齐，漏任一步签名即错。

**测试**：包内固定向量单测（签名重算比对 + httptest 响应解析，SendSms 与 QuerySendDetails 均覆盖）；无 integration 集成（阿里云无测试环境）；真链路契约验证在 tests/ 冒烟套件（smoke 标签，凭证缺失自动 Skip：`TestSmokeSmsQueryDetails` 零计费查询、`TestSmokeSmsRealSend` 真发送 + BizId 查询闭环）。

## 模板详情查询

`func (s *Sms) GetSmsTemplate(ctx context.Context, templateCode string) (*SmsTemplateResult, error)` 复用 ACS3 签名，POST 参数在 query、body 为空。输入 TemplateCode 原样发送。

SmsTemplateResult 包含 Success、RequestID、ErrorCode、ErrorMessage、TemplateName、TemplateCode、TemplateContent、TemplateType、TemplateStatus、RelatedSignName、VariableAttribute 和 AuditInfo（RejectInfo、AuditDate）。类型/审核状态保持厂商字符串，包含未知新状态；VariableAttribute 保留原始 JSON 字符串，由消费者显式解析。Success 仅表示查询成功，不表示审核通过。厂商拒绝返回 Success=false；网络/解码失败返回 Go error。本方法不映射项目状态、不写数据库。

[官方 GetSmsTemplate 文档](https://help.aliyun.com/zh/sms/developer-reference/api-dysmsapi-2017-05-25-getsmstemplate)（2026-10-07 核对）。协议测试：aliyun/sms_template_test.go，使用本地 HTTP 替身，不访问厂商。

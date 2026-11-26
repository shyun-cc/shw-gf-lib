# sms — 短信发送抽象

> 面向项目侧接入者（第 1–4 节）与库维护者（加读第 5 节）。事实来源是 `sms/` 源码，本文的代码示例、签名、事件名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

短信发送抽象（对应 PHP Sms）：**Scene 场景 + Provider 接口 + Manager 多供应商按名切换 + 发送事件**。库内置阿里云 Provider（经 aliyun 组件）与日志 Provider（开发/测试）；其他平台由项目实现 `Provider` 接口。

依赖方向：sms → events（发送事件）+ aliyun（内置 Provider）。事件派发解耦审计/统计/告警——发送方只管发，监听方自己 `events.Subscribe`。

## 2. 快速接入

```go
package boot

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/aliyun"
	"github.com/shyun-cc/shw-gf-lib/sms"
)

// 单供应商（简单场景）
var svc *sms.Service

func init() {
	client := aliyun.NewClient(aliyun.Credentials{
		AccessKeyID:     "<AK>",
		AccessKeySecret: "<SK>",
	})
	svc = sms.NewWithProvider(sms.NewAliyunProvider(sms.AliyunConfig{
		Client:   client,
		SignName: "公司签名",
		Templates: map[string]string{
			"login_code":         "SMS_10001", // key = Scene 字符串值
			"reset_password_code": "SMS_10002",
		},
	}))
}

func SendLoginCode(ctx context.Context, phone, code string) error {
	res, err := svc.Send(ctx, sms.Message{
		Phone:  phone,
		Scene:  sms.SceneLoginCode,
		Params: map[string]any{"code": code},
	})
	if err != nil || !res.Success {
		return err // 事件 sms.Failed 已派发，这里只管返回
	}
	return nil
}
```

多供应商（按名切换、灰度/多通道）：

```go
m := sms.NewManager()
m.Register(sms.NewAliyunProvider(aliCfg))        // Name() = "aliyun"
// m.Register(myProvider)                         // 项目自实现，Name() = 自己的标识

res, err := m.Send(ctx, "aliyun", msg)           // 按名发送
p, err := m.Provider("aliyun")                   // 按名取出（未注册报错）
```

监听发送事件（审计/统计）：

```go
events.Subscribe(sms.EventSent, 0, func(ctx context.Context, e events.Event) {
	p := e.Payload.(sms.SendEventPayload)
	// p.Phone / p.Scene / p.Result
})
events.Subscribe(sms.EventFailed, 0, func(ctx context.Context, e events.Event) { /* 告警 */ })
```

## 3. 参数语义

| 字段 | 位置 | 语义 |
| --- | --- | --- |
| `Scene` | Message | 场景枚举（`login_code` / `reset_password_code` / `notify` / `generic`）或自定义 string；阿里云 Provider 用它查模板表 |
| `SignName` | Message | 消息级签名覆盖，空串用 Provider 默认签名 |
| `Params` | Message | 模板变量 map（JSON 序列化进 TemplateParam） |
| `Templates` | AliyunConfig | 场景名 → 模板 Code 映射；未命中场景发送报「未配置场景模板: \<scene\>」 |
| `dispatcher` | Service/Manager | 事件分发器注入（SetDispatcher）；不设走 events.Default() 全局 |

## 4. 契约

**Provider 接口**：`Name() string`（Manager 注册键与 Result.Provider）+ `Send(ctx, Message) (*Result, error)`。Result 六字段：Success / MessageID / Provider / ErrorMessage / RequestID / ErrorCode。阿里云 Provider 原样透传 RequestID 和响应 ErrorCode（成功时也可能为 OK），其他 Provider 未提供时为空。

**事件契约**：每次 Send 恰好派发一个事件——成功（含 err==nil 但 Success=false 的「业务失败」）按 Success 分流 `sms.Sent` / `sms.Failed`；err != nil（Provider 报错）必派 `sms.Failed`。载荷 `SendEventPayload{Phone, Scene, Result}`。

**默认日志驱动**：`sms.New()` 构造的服务用 logProvider（Name="log"），发送只打日志 `[SMS LOG] phone=... scene=... code=...` 恒 Success——开发/测试环境零成本跑通链路。

**Manager 注册语义**：按 Name() 存储，重名覆盖；`Provider(name)` 未注册返回「未注册的短信 Provider: \<name\>（已注册: [...]）」。

## 5. FAQ

**Q：场景 Scene 用内置枚举还是自定义？**

Scene 底层是 string，自定义场景直接 `sms.Scene("order_shipped")`——阿里云 Provider 只要求 Templates 表里有对应键。

**Q：发送失败重试怎么做？**

组件不内置重试（短信失败多数是模板/签名/欠费类，重试无益）。需要重试语义在上层做（如 task 组件异步任务 + 退避）。阿里云客户端层 HTTP 异常的重试也不在本组件——直发一次。

**Q：怎么接腾讯云短信？**

项目实现 `sms.Provider` 接口（Name + Send），Manager.Register 注册后按名发送。参考 aliyun Provider 的实现形态（场景查表 → 平台调用 → Result 映射）。

**Q：怎么查短信发送状态/终态？**

发送抽象层（Provider 接口）不含查询能力——发送状态查询是厂商特有能力：阿里云走 aliyun 包 `aliyun.NewSms(client).QuerySendDetails(...)`，见 docs/aliyun.md。

**Q：事件监听方 panic 会影响发送吗？**

不会。events 组件的 handler panic 隔离（recover + 日志，不中断后续），发送方无感。

## 6. 维护者注记

**Provider 接口 v0.2.0 升级**：`Send(ctx, msg) error` 改为 `Name() string` + `Send(ctx, msg) (*Result, error)`——旧单方法接口无 Result 承载、Manager 无法按名注册。骨架期变更无生产使用方。

**事件与解耦边界**：发送事件经 events 派发而非回调函数——监听方数量与形态（审计入库 / metrics / 告警）是项目决策，库只保证事件载荷完整（Result 原样带出）。

**测试**：包内单测（logProvider 链路、Manager 注册/未注册错误、事件恰好一次断言用 fake dispatcher）；aliyun Provider 的平台交互在 aliyun 包测试覆盖。

## 模板变量映射

`Placeholders(content string) []string` 提取 `${name}`、去重并排序；name 以 ASCII 字母开头，其余为字母、数字或下划线。

`ResolveParams(content string, mappings map[string]string, allowed []string, values map[string]any) (map[string]any, error)` 将上下文变量映射为厂商参数。mappings 必须恰好覆盖占位符；映射目标必须已声明且存在于 values，额外未声明的上下文变量会报错。声明但模板未使用的变量允许存在。函数不修改输入、不执行表达式、不验证厂商变量值格式，错误不包含变量值。业务场景与白名单由项目决定，JSON 元数据存储也留在项目。

新增字段兼容带字段名的 Result 字面量；使用无字段名结构体字面量的消费者须改用字段名。

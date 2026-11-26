// Package wecom 企业微信服务端 API 自主复刻（零厂商 SDK），API 域覆盖以微信官方文档全集为基准
// （见仓库 roadmap 与 docs/wecom.md）。
//
// 工程模式：每个域方法 = 一行 Endpoint 表项 + 一行薄委托（invoke 统一分发，自动注入 access_token）；
// 异常两层——BizError（errcode ≠ 0，errors.As 可捕获原始码值）与 InfraError（HTTP/解码/凭证链路失败，
// 可 Unwrap），二者类型互斥供告警分流。
//
// 会话存档（msgaudit）明确不做：官方仅提供 C 语言 SDK + 私有二进制协议，Go 侧需 cgo 绑定或自研
// 协议解析，工程形态与表驱动薄委托完全不合。触发条件：有真实项目需要时另立专项 change 评估。
package wecom

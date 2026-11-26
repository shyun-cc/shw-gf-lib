// Package wxpay 微信支付商户侧服务端 API 自主复刻（零厂商 SDK），V2+V3 双轨并存：
//
//   - V3 轨：JSON 报文 + SHA256-RSA 请求签名 + 平台证书/微信支付公钥验签 + 回调 AES-256-GCM 解密；
//   - V2 轨：XML 报文 + API 密钥 MD5/HMAC-SHA256 签名 + p12 商户证书双向认证。
//
// 两轨各持独立的表驱动分发层（V3 Endpoint 表项 + 统一 invoke；V2 V2Endpoint 表项走 doV2），编解码与签名栈各自实现。
// 域覆盖以微信官方文档全集为基准（支付/转账/支付分/营销/交易支持/安全工具产品族 + 仅 V2 提供的
// 端点），随 wechat-ecosystem roadmap 各域 change 逐端点清点铺设。
//
// 一切证书与密钥（商户私钥、证书序列号、API 密钥、平台证书）SHALL 经构造参数注入——库不做项目决策，
// 包内不出现硬编码密钥或证书路径。EasyWeChat 工厂不迁移：Go 侧定案全自主复刻、零厂商 SDK。
package wxpay

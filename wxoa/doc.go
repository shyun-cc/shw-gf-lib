// Package wxoa 微信公众号（服务号）服务端 API 自主复刻（零厂商 SDK）。
//
// 组件定位：以微信官方文档全集为覆盖基准（服务号侧文档为准，订阅号差异在各域 change 清点时核对），
// 域铺满随 wechat-ecosystem roadmap 各域 change 推进——底座（access_token 缓存与并发去重、回调验签解密、
// JS-SDK 签名、网页授权）起步。回调验签解密 SHALL 复用 wecom 包的 WXBizMsgCrypt（AppId 尾校验变体），
// 即 wxoa 对 wecom 存在单向依赖（依赖方向：wxoa → wecom）。
//
// EasyWeChat 工厂不迁移：Go 侧定案全自主复刻、零厂商 SDK（含 EasyWeChat 形态的工厂封装）。
package wxoa

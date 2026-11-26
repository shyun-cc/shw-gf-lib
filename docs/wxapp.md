# wxapp — 微信小程序服务端 API

> 面向项目侧接入者（第 1–3 节）与库维护者（加读第 4–5 节）。事实来源是 `wxapp/` 源码，本文的代码示例、签名与错误行为均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

微信小程序服务端 API 自主复刻（对应 PHP WxaLoginService），零厂商 SDK。由原 `wxa` 包改名承接（**BREAKING**，见第 5 节迁移指引），导出类型采用 `WxApp` 前缀。**登录三件套**：

- `Code2Session`：登录凭证校验（code 换 openid / session_key / unionid）；
- `GetPhoneNumber`：手机号获取（新版 POST code 接口）；
- `GetAccessToken`：接口调用凭证（**Redis 缓存 TTL 7000s + redislock 并发去重 + double-check + 自旋**——并发安全是 PHP 侧缺失、Go 侧补上的能力）。

域覆盖以微信官方文档全集为基准（登录三件套之外的小程序域随 wechat-ecosystem roadmap 各域 change 铺设）。依赖方向：wxapp → redislock（token 并发去重）。前提：项目 Redis 配置就绪（g.Redis() 可用）。

## 2. 快速接入

```go
package handler

import (
	"github.com/shyun-cc/shw-gf-lib/wxapp"

	"github.com/gogf/gf/v2/net/ghttp"
)

var svc = wxapp.NewWxAppService("<AppID>", "<AppSecret>")

// 小程序 wx.login() 后拿 code 换会话
func Login(r *ghttp.Request) {
	code := r.Get("code").String()
	session, err := svc.Code2Session(r.Context(), code)
	// session.OpenID / session.SessionKey / session.UnionID（满足下发条件时非空）

	// 需要手机号时：button open-type="getPhoneNumber" 的 code
	phone, err := svc.GetPhoneNumber(r.Context(), phoneCode)
}
```

`NewWxAppService(appID, secret string)` 构造 `*WxAppService`；`HTTPClient` / `BaseURL` 字段可注入（测试取证用，默认 `https://api.weixin.qq.com`）。

## 3. 契约

**表驱动薄委托**：每个端点是一行 `Endpoint` 表项（Name/Method/Path/Query 清单/Body 标志），域方法经统一 `invoke` 分发（query 键集按表项校验）。当前 249 表项 = 登录三件套（按 2026-08-24 范围裁决收官）（`login.code2session` / `phone.getuserphonenumber` / `token.fetch`）+ 用户信息 4（`user.getpluginopenpid` / `user.checkencryptedmsg` / `user.getpaidunionid`（GET，订单号二选一由调用方保证）/ `user.getuserencryptkey`（GET 三 query 必填，signature=hmac_sha256(session_key,"")））+ 码与链接 9。

**用户信息族**（change wxapp-qrcode-userinfo）：`GetPluginOpenPID`（code 5 分钟有效仅一次）、`CheckEncryptedData`（sha256 hex，仅近 3 天）、`GetPaidUnionID`（支付后 5 分钟内有效）、`GetUserEncryptKey`（近 3 次 key、每次 3600s）。

**凭证与 openApi 运维族**（change wxapp-openapi-kf）：`GetStableAccessToken(forceRefresh)`（POST body 形态、与旧 token 隔离、**方法不缓存**——缓存策略归调用方；强制刷新日 20 次）；额度三件 `GetApiQuota(cgiPath)` / `ClearApiQuota`（月 50 次）/ `ClearQuota` + 兜底 `ClearQuotaByAppSecret`（**POST query 传参特例**，token 耗尽时可用，与 ClearQuota 共享月 10 次）；`GetRidInfo`（rid 7 天）；`CallbackCheck(action, checkOperator)`；IP 双件 `GetApiDomainIPs` / `GetCallbackIPs`。

**微信客服绑定族**（2026-08-24 铺入，wxapp 收官批）：`GetKfWorkBound`（GET，返回 entityName/corpid/bindTime）/ `BindKfWork(corpid)` / `UnbindKfWork(corpid)`——小程序↔企业绑定关系（corpid 须与主体一致、仅非个人主体；区别于 wecom 包微信客服域）。

**微信物流服务族**（change wxapp-weixin-express，39 端点）：查询/消息组件七件 `TraceWaybill` / `QueryTrace` / `UpdateWaybillGoods` + `FollowWaybill`（微信跟踪节点推送）/ `QueryFollowTrace` / `UpdateFollowWaybillGoods` + `GetOpenMsgDeliveryList`（两组共用官方同一接口）；无忧退货十一件（`OpenInsuranceFreight` GET 双件 / `CreateInsuranceOrder`（order_no 仅一次投保）/ `ClaimInsurance` / 充值支付四件（`CreateChargeID`→`ApplyInsurancePay`→`GetInsurancePayOrders`/`RefundInsurance` 只退原账号）/ `GetInsuranceSummary` / `GetInsuranceOrders`（过滤族 ≤100）/ `SetInsuranceNotifyFunds` 24h 一次）；退货三件 `AddReturnId`（wx_pay_id 须已投保）/ `GetReturnId` / `UnbindReturnId`；同城配送十六件（门店四件 / 资金六件（充值 50 元起 1 月有效、退款在途须完结、流水 flow_type 1-3、扣费主体三态管理员确认生效）/ 订单四件（询价分钟级有效、store_order_id 幂等重入、取消 reason 1/2/3/99+违约金语义）/ 城市查询+模拟回调沙盒）；运力推送两件 `DeliveryUserQuery` / `DeliveryPathNotify`。

**物流助手族**（change wxapp-express-delivery，22 端点）：小程序侧 12——账号三件（`BindExpressAccount` bind/unbind + GET 双件）、运单五件（`AddExpressOrder`（order_id ≤512 全局唯一、顺丰 expect_time）/ `CancelExpressOrder` / `GetExpressOrder`（print_html BASE64 面单）/ `BatchGetExpressOrders` ≤100 / `GetExpressPath`）、打印员两件、`GetExpressQuota`（加盟类快递）、`TestUpdateExpressOrder`（沙盒 biz_id=test_biz_id）；运力方 10——审核/轨迹/模板预览（BASE64 渲染）/联系人 + 散单族（取消/运费 need_pay 0-2 支付分/部分退款/对账单 ALL-SUCCESS-REFUND/投诉结果/状态更新取派件员四必填）。

**即时配送族**（15 端点）：商户侧 14——下单三件 `PreAddLocalOrder`（返回 delivery_token 达达锁价 3 分钟）/ `AddLocalOrder` / `ReAddLocalOrder`（**美团不支持重下**）；取消两件 `PreCancelLocalOrder`（违约金预估）/ `CancelLocalOrder`（reason_id 1-6）；`GetLocalOrder`（order_status 102/202/301/302 + 骑手信息）；`AddLocalTips`（**美团不支持**、顺丰 ≤10 次 200 元、闪送档位）；绑定开通三件 + 异常件退回 + 模拟双件 + `GetAllImmeDeliveries`；运力侧 `UpdateLocalDeliveryOrder`（wx_token + agent 骑手族）。**delivery_sign 签名 SHA1(shopid[+shop_order_id]+AppSecret) 由调用方计算**——AppSecret 不入库。

**交易管理服务族**（change wxapp-trade，发货信息管理）：发货录入两件 `UploadShippingInfo`（一支付单一次；logistics_type 1-4；顺丰须掩码 contact）/ `UploadCombinedShippingInfo`（合单子单族、分拆仅快递 ≤15 包裹）；查询两件 `GetTradeOrder`（单号二选一）/ `GetTradeOrderList`（last_index 翻页）；`NotifyConfirmReceive`（每单一次）/ `SetMsgJumpPath`（平台自动追加单号 query）/ `IsTradeManaged(appid)` / `IsTradeManagementConfirmationCompleted(appid)` / `OpSpecialOrder`（预售 15-330 天）/ 品牌两件 `FamousBrandApply` + `GetFamousBrandApplyStatus` / `SetTradeType`（每账号一次）。

**交易保障与付费族**：体验分 `GetPenaltyList(offset, limit)`（申诉状态族）/ `GetGuaranteeStatus`；评价九件 `GetCommentList(startTime, endTime, filterType…)`（filterType 1-6、score 100 分=1 星）/ `GetReplyList` / `GetCommentInfo` / `AddReply`（首条回复即评论）/ `DeleteReply`（连带回复）/ `AddCommentReply` / `DeleteCommentReply` / `ResetApiKfQuota` / `ConfirmCompromise`（差评和解 picList mediaId）；付费两件 `GetChargeUsageDetail(spuID, offset, limit ≤20)` / `GetRecentAverageUsage(spuID)`（模糊化月均）。

**硬件设备族**（change wxapp-hardware-ops-plugin）：设备消息 `SendHardwareDeviceMessage`（日 100 万）/ `GetSnTicket`（5 分钟）/ 设备组四件（≤50 台/组、force_add 慎用）/ license 三件——**官方 2024-12-31 已回收，表项保留供存量调用方**（godoc 注记）。

**运维中心族**：`GetDomainInfo(action)` / `GetOpsPerformance(body)`（**与数据分析 GetPerformanceData 是不同端点勿混用**）/ `GetSceneList` / `GetVersionList`（GET 无参双件）/ `SearchRealtimeLog(params)`（date 近 7 天、50 次/分；官方 GET 带 Body 按 GET+query 实现留痕）/ `GetFeedbackList` / `GetFeedbackMedia(recordID, mediaID) ([]byte, error)`（图片二进制）/ `GetJsErrDetail`（md5 双键 12 必填）/ `GetJsErrList`（limit ≤30）/ `GetGrayReleasePlan`（GET 无参）。

**插件管理族**：`ManagePluginApplication(body)`（开发者侧 dev_agree/dev_refuse/dev_delete/dev_apply_list）/ `ManagePlugin(body)`（使用方侧 list/apply/unbind/update）。

**小程序直播族**（change wxapp-live，38 端点）：直播间 CRUD 四件（`CreateLiveRoom` 13 必填、`GetLiveInfo` 列表与回放 action=get_replay）+ GET 四件（推流地址/分享二维码/副号查询/小助手列表——官方标 GET 带 Body 的按 GET+query 实现留痕）+ 主播副号四件 + 直播间商品五件（导入/删除/推送/上下架/排序）+ 小助手四件 + 开关四件（禁言/官方收录/客服/回放）+ 讲解视频 + 挂件全局 KEY 两件（建议只设一次）；商品库七件 `AddLiveGoods`（返回 goodsId+auditId 须保存、与重提审共享日 500）/ `ResubmitLiveGoodsAudit` / `GetLiveGoodsAuditInfo`（≤20 批查）/ `ResetLiveGoodsAudit`（撤回不返还次数）/ `UpdateLiveGoods`（通过后仅价格族）/ `ListApprovedLiveGoods`（GET）/ `DeleteLiveGoods`（不可恢复）；成员三件（role 1/2/3 上限 10/500/500）；长期订阅两件 `PushLiveStartMessage`（≤2000 人次日 5000）/ `GetLiveFollowers`（page_break 翻页）。

**图像处理与 OCR 族**（change wxapp-ocr-face-misc）：九件统一双形态 `(ctx, imgURL string, img []byte)`——img_url 走 query / img 走 multipart（官方 part 名 img），二选一空则参数错误；`ImgAiCrop(ratios ≤5 个)` / `ImgQrcode`（条码/QR/DataMatrix/PDF417）/ `ImgSuperResolution`（**官方已下架**，表项保留注记）；OCR 六件 `OCRComm` / `OCRIdcard` / `OCRDriving` / `OCRBankcard` / `OCRBizlicense` / `OCRDrivingLicense`（认证账号日 100 次）。

**生物认证与人脸核身族**：`VerifySoterSignature(openid, jsonString, jsonSignature)`（前端 startSoterAuthentication 回调字段，返回 is_ok）/ `GetFaceVerifyID(body)`（out_seq_no appid 内唯一 + cert_info 三元组，verify_id 3600s）/ `QueryFaceVerifyInfo(body)`（cert_hash SHA256；通过 = errcode 0 且 verify_ret 10000）。

**搜一搜/红包封面/服务市场族**：`SubmitSearchPages(body)`（pages ≤1000 结构化、日 50 万）/ `GetRedPacketCoverURL(openid, ctoken)` / `InvokeService(body)`（async 返回 request_id）/ `RetrieveServiceData(requestID)`（9301015 处理中）。

**小程序安全族**（change wxapp-security-nearby-data）：`MsgSecCheck`（文本 v2，content ≤2500、scene 1-4、openid 须近 2h 访问过）/ `MediaCheckAsync`（异步 30 分钟内经事件推送结果——**回调事件常量挂起**待接线；单文件 ≤10M）/ `GetUserRiskRank`（scene 0 注册/1 营销/2 UGC，risk_rank 0-4）。

**附近小程序族**：`AddNearbyPoi`（11 项必填；不支持并发建议 1s 串行）/ `DelNearbyPoi` / `GetNearbyPoiList(page, pageRows ≤1000)` / `SetNearbyPoiShowStatus(poiID, status)`。

**数据分析族**：留存三件 `GetDaily/Weekly/MonthlyRetain`（日单日/周自然周/月自然月）+ 趋势三件 `GetDaily/Weekly/MonthlyVisitTrend` + 概况 `GetDailySummary` + 页面 `GetVisitPage`（top200）+ 画像 `GetUserPortrait`（相差 0/6/29 天）+ 分布 `GetVisitDistribution`（十件统一 (ctx, beginDate, endDate)）+ `GetPerformanceData(body)`（性能，time 跨度 ≤30 天）。

**动态消息与订阅消息族**（change wxapp-subscribe-message）：动态消息三件 `CreateActivityID(unionid, openid)`（GET 二选一、默认 24h）/ `SetUpdatableMsg`（target_state 条件必填族）/ `SetChatToolMsg`（聊天工具卡片）；订阅消息模板六件（`GetSubscribeCategory` / `ListPublicTemplateTitles` / `GetPublicTemplateKeywords` / `AddSubscribeTemplate`（kidList 2-5 个返回 priTmplId）/ `ListSubscribeTemplates` / `DelSubscribeTemplate`）+ `SendSubscribeMessage`（48h 窗口外需订阅授权；miniprogram_state/lang 必填）+ 服务卡片三件 `SetUserNotify` / `SetUserNotifyExt` / `GetUserNotify`；客服子商户四件 `RegisterKFBusiness`（account_name 一次性、仅电商类目）/ `UpdateKFBusiness` / `GetKFBusiness`（二选一）/ `ListKFBusiness`（≤200 分页）。

**小程序客服·客服消息族**：账号四件 `AddKFAccount`（≤100 个客服）/ `ListKFAccounts` / `ListOnlineKFAccounts`（GET，business_id 选填）/ `DelKFAccount`；管理员 GET 双件 `SetKFAdmin(kfOpenid)` / `CancelKFAdmin`；`SendCustomMessage`（48h 窗口条数限制）/ `SetTyping(touser, command)`（小程序形态 path 含 /business 段；须 30s 内有交互）；素材两件 `UploadTempMedia(mediaType, filename, content)`（**wxapp 侧 invokeMultipart 首例**：access_token+type 走 query）+ `GetTempMedia(mediaID) ([]byte, error)`（invokeRaw；视频形态返回 video_url 由调用方二跳）。

**小程序码与链接族**：图片三件 `CreateWXACode`（path ≤1024）/ `CreateUnlimitedWXACode`（scene ≤32 字符限字符集、5000 次/分）/ `CreateWXAQRCode`（path ≤128 仅已发布版；前两码合计 ≤10 万个）——**应答为图片二进制 buffer**（`invokeRaw` 分发：Content-Type 含 image 返回原始字节，失败 JSON errcode → BizError）；scheme/链接六件 `QueryScheme` / `GenerateScheme`（日生成 50 万共享额度、≤100 QPS、≤30 天）/ `GenerateNFCScheme`（须 NFC 能力，sn 一机一码必填一型一码必空）/ `GenerateURLLink`（cloud_base 可定制中转页）/ `QueryURLLink` / `GenerateShortLink`（短期 30 天不限量、永久 ≤10 万、日 ≤1000 万）。

**异常两层**（与 wecom 同构，类型在 wxapp 包内各自定义）：

- **`BizError`**（业务异常）：响应 errcode ≠ 0 时返回，`Errcode` 携带微信原始数值、`Errmsg` 原文——`errors.As(err, &biz)` 差异化捕获（如 40029 invalid code 与 45011 频率限制走不同分支）；
- **`InfraError`**（基础设施异常）：HTTP 请求失败 / 响应非 JSON / 凭证链路失败 / 响应缺关键字段——`Op` 标环节（http/decode/token/encode）、`Unwrap` 暴露底层错误，可触发告警分流。两层类型互斥。

**Code2Session**：GET `/sns/jscode2session`（appid/secret/js_code/grant_type=authorization_code）。成功响应无 errcode——errcode 存在且 ≠0 时返回 `BizError`。

**GetPhoneNumber**：POST `/wxa/business/getuserphonenumber`（JSON body `{"code":…}`、access_token 走 query）。响应 `phone_info.phoneNumber` 缺失时返回 `InfraError{Op:"decode"}`。

**GetAccessToken 并发语义**（缓存键 `wxapp:access_token:<AppID>`、锁 `wxapp:token:<AppID>` TTL 10s）：

1. 缓存命中直接返回；
2. miss 时 redislock 并发去重——抢到锁者 double-check 缓存（排队期间他人可能已回写）后 fetch 回写（TTL 7000s，微信侧 7200s 提前 200s 刷新）；
3. 未抢到锁者自旋 5×200ms 读缓存；
4. 自旋上限后兜底直接 fetch——**宁可重复 fetch 也不报错**。

## 4. FAQ

**Q：access_token 缓存回写失败会怎样？**

记 Warning 日志但不影响本次返回（拿到 token 就返回）——下次调用会再 fetch，缓存自愈。

**Q：session_key 怎么用？**

code2Session 只换不发——session_key 用于解密小程序侧加密数据（如旧版手机号解密）。新版接口（GetPhoneNumber 的 code 换手机号）不再需要它，session_key 仅按需暂存。

**Q：UnionID 什么时候为空？**

微信侧条件不满足时（未绑定开放平台等）响应不含 unionid 字段——WxAppSession.UnionID 为空串，业务按「可能无 UnionID」设计。

**Q：多小程序（多 AppID）共存？**

缓存键与锁键都带 AppID 后缀，各 `NewWxAppService(...)` 实例互不干扰。

**Q：怎么按错误类型分流处理？**

```go
var biz *wxapp.BizError
if errors.As(err, &biz) {
	switch biz.Errcode {
	case 40029: // invalid code → 引导重新登录
	case 45011: // 频率限制 → 退避重试
	}
	return // 业务 errcode 不触发告警
}
var infra *wxapp.InfraError
if errors.As(err, &infra) {
	// 基础设施异常：触发告警，infra.Op 区分环节
}
```

## 5. 迁移指引（wxa → wxapp，BREAKING）

| 旧（wxa） | 新（wxapp） |
|---|---|
| `import gf-lib/wxa` | `import gf-lib/wxapp` |
| `wxa.New(appID, secret)` | `wxapp.NewWxAppService(appID, secret)` |
| `*wxa.Service` | `*wxapp.WxAppService` |
| `*wxa.Session` | `*wxapp.WxAppSession` |
| 错误字符串匹配（`strings.Contains(err.Error(), "errcode=")`） | `errors.As(err, &biz)` / `errors.As(err, &infra)` |

方法名 `Code2Session` / `GetPhoneNumber` / `GetAccessToken` 与行为不变。Redis 缓存/锁键前缀由 `wxa:` 改为 `wxapp:`——升级后首次调用缓存 miss 重新 fetch 一次，无破坏。旧 `wxa` 包整体移除、无兼容 alias。

## 6. 维护者注记

**PHP 侧缺失的补齐**：hyperf-lib 的 token 获取无并发去重——多请求同时 miss 时会并发 fetch（微信侧有配额压力且可能触发频率限制）。Go 侧 redislock + double-check + 自旋三件套是刻意的增强，不是过度设计。

**测试**：包内单测经 miniredis（token 缓存/锁/自旋链路）+ httptest（code2Session/getPhoneNumber/gettoken 响应解析与 errcode 判定）+ 表驱动端点断言（遍历全量表项断言 method/path/query 键集/body 形态，计数相等防漂移——新增表项必须同步配测试用例）。

## 凭证缓存回写日志（#137，v0.14.2）

凭证缓存 SET 失败时，本次仍返回已获取的 token，后续调用可能重新获取。组件只记录固定警告：`wxapp access_token 缓存回写失败`；不附加原始 Redis 错误、SET 参数或凭证值。原因是 GoFrame Redis adapter 会在错误中携带完整命令参数。此约束只覆盖组件自身的缓存回写警告，不代替调用方的 Redis trace、外部日志采集与凭证保护配置。

## 7. 服务端 API 安全通信（#140）

保护业务服务器与微信 API 之间的通信，不是小程序请求业务服务器的加密，也不是消息回调 Token/EncodingAESKey。官方依据：[签名加密指南](https://developers.weixin.qq.com/miniprogram/dev/server/getting_started/api_signature.html)。

### 7.1 支持范围与兼容

首期实现 `AES256_GCM` + `RSAwithSHA256`（RSA-PSS，签名 salt 长度 32）；不支持 `SM4_GCM` / `SM2withSM3`，配置其他算法立即失败。仅 `GetPhoneNumber`、`GetUserRiskRank` 使用安全链路：两者官方页面明确标注支持服务通信二次加密和签名。`Code2Session`、token 获取、其他 JSON/图片/资源上传等端点继续原有协议，不能把本能力理解为所有 249 个端点都加密。

`NewWxAppService`、原 `WxAppService` 四个字段与已有方法签名不变；安全包装类型嵌入原服务副本，覆盖两个受保护 API，并对其 token 获取错误做安全处理。未调用 `WithAPISecurity` 时保留原行为。启用后，支持范围内的接口自动请求加密、签名、响应验签、解密；任何安全失败直接返回错误，不重试、不退回普通请求。

### 7.2 材料与配置

每个 AppID 独立配置：32 字节对称密钥、微信后台对称密钥编号、应用 RSA 私钥，以及平台编号到平台证书 PEM 的映射。平台编号来自微信后台，**不是 X.509 证书内置序列号**。对称密钥若从后台以 Base64 下载，业务先标准 Base64 解码再传入。

`WithAPISecurity` 返回 `*SecureWxAppService` 包装服务副本，复制密钥与证书材料，并绑定创建时 AppID。配置轮换时先构造并校验新副本，再由业务原子替换正在使用的客户端；在途调用继续使用原快照。不能并发修改客户端公开的 AppID、Secret、HTTPClient、BaseURL 字段。

平台证书轮换时可同时配置多个可信编号；响应中的主签名和 Deprecated 签名分别只使用对应已配置证书验证。即将过期的证书及时由业务更新；库不自动下载或信任响应附带的陌生证书。

### 7.3 业务上线顺序

先准备微信后台密钥/公钥和平台证书材料，完成业务安全客户端接线与发布，再协调微信后台启用。各小程序按 AppID 分别配置，不共用 AppID 或证书键。库不替业务保存密钥、不操作后台，不将 CI 协议测试视为真实手机号授权或登录验收。

`GenerateRSAKeyPair` 为纯本地辅助工具，返回 PKCS#8 私钥与 SPKI 公钥 PEM；私钥仅存服务端秘密存储，公钥按平台流程配置。签名为 `GenerateRSAKeyPair(bits int) (privatePEM, publicPEM []byte, err error)`；bits 为 2048–8192 范围内的 256 倍数，工具不写文件、不联网。

### 7.4 接入 API 与错误

```go
func (s *WxAppService) WithAPISecurity(c APISecurityConfig) (*SecureWxAppService, error)
```

`APISecurityConfig` 的字段：`EncryptionAlgorithm`、`SignatureAlgorithm`、`SymmetricKey`、`SymmetricKeySerial`、`PrivateKeyPEM`、`PlatformCertificates`，以及可选 `RequestTimeout`（零值 8 秒）、`MaxClockSkew`（零值 5 分钟，过去/未来双向检查）、`MaxResponseBytes`（零值 1 MiB，上限 64 MiB）；负值拒绝。私钥支持 PKCS#8 / PKCS#1 PEM；RSA 密钥为 2048–8192 位。平台证书必须包含 RSA 公钥且处于有效期；HTTP 测试地址仅允许 localhost/回环 IP，正式地址必须 HTTPS。安全链路拒绝重定向。

安全错误为 `*SecurityError`，`Op` 仅表示失败阶段，固定错误文本，不附带原始 HTTP URL、密码材料或报文。取消/超时保留 `errors.Is(err, context.Canceled/context.DeadlineExceeded)` 能力。经验证解密后的微信业务错误保留 `BizError.Errcode`，`Errmsg` 固定为 `API request rejected`，失败不返回原始响应 map，避免上游错误反射凭证。

完整且可编译的材料读取与构造示例位于 `wxapp/example_api_security_test.go`。业务从自身 Secret/配置服务读材料，传入以下配置：

```go
secure, err := wxapp.NewWxAppService(appID, appSecret).WithAPISecurity(wxapp.APISecurityConfig{
    EncryptionAlgorithm: "AES256_GCM",
    SignatureAlgorithm: "RSAwithSHA256",
    SymmetricKey: decodedKey,
    SymmetricKeySerial: symmetricSerial,
    PrivateKeyPEM: privatePEM,
    PlatformCertificates: map[string][]byte{platformID: certificatePEM},
})
if err != nil { return err }
phone, err := secure.GetPhoneNumber(ctx, phoneCode)
```

测试位于 `wxapp/api_security_test.go`、`wxapp/key_material_test.go`，使用虚构材料和 HTTP 桩验证协议；未进行真实微信接口/后台开关联调。

## 8. 小程序分享签名（#141）

依据[小程序分享签名](https://developers.weixin.qq.com/miniprogram/dev/framework/share_signature.html)，开启分享校验前先配置开发者公钥或证书。首期仅 `RSAwithSHA256`（RSA-PSS/SHA256，salt 长度 32），显式拒绝 SM2 等未实现算法。本能力独立于 API 安全配置与公众号 `wxoa.SignJSURL`，不调用微信接口、不需要 access_token 或 Redis。

### 8.1 构造与业务调用

```go
func NewShareSigner(c ShareSignerConfig) (*ShareSigner, error)
func (s *ShareSigner) BuildShare(in ShareInput) (*ShareConfig, error)
func (s *ShareSigner) BuildShareWithHash(in ShareHashInput) (*ShareConfig, error)
func ShareImageHash(image []byte) (string, error)
```

`ShareSignerConfig` 含 `AppID`、`SignatureAlgorithm`、`PrivateKeyPEM`；签名器解析并独立持有私钥，AppID 不接受每次请求覆盖，材料轮换创建新签名器。私钥支持 2048–8192 位 RSA 的 PKCS#8/PKCS#1 PEM，可使用 `GenerateRSAKeyPair` 准备。

`ShareInput` 含 `Title`、`Path`、`ImageURL` 和 `ImageBytes`；`BuildShare` 对实际图片字节计算摘要再签名。`ShareHashInput` 使用 `ThumbDataHash` 替代图片字节，摘要必须为 64 字符小写十六进制；业务可在可信上传流程中预计算并缓存摘要。`ShareImageHash` 拒绝空图片，不做图片格式解码或远程下载。

返回 `ShareConfig` 恰好包含五个前端字段：`title`、`path`、`imageUrl`、`thumbDataHash`、`signature`。业务根据记录 ID 鉴权并生成标题/路径/图片，不应开放允许任意客户端内容的公共签名接口。标题、路径与图片 URL 必须非空；路径以 `/` 开头；图片 URL 使用 HTTPS 且不带用户信息或 fragment。库保留标题 Unicode 和路径原始编码、参数顺序，不 trim、不重新 URL 编码签名内容。

### 8.2 图片一致性与前端接线

签名覆盖 `appid`、`path`、`thumbDataHash`、`title`，**不直接覆盖 imageUrl**。同一图片内容换 URL 不会改变签名有效性，但 URL 下载的字节必须与摘要一致；CDN 自动转码、水印或覆盖同 URL 内容都会导致校验失败。推荐固定内容的版本化图片 URL，图片变化时重新计算摘要和签名。库不下载任意 URL，业务负责可信图片来源与可访问性。

后端签名前，各字段值先按 UTF-8 做标准 Base64，按 ASCII 字典序拼为 `appid=...&path=...&thumbDataHash=...&title=...`，末尾无换行，再用 RSA-PSS 签名。前端直接使用完整对象，不再修改其中字段：

```js
// 页面数据加载时，由业务请求层按记录 ID 获取后端已鉴权的分享对象。
// 后端接口由业务项目提供；库不注册路由。失败时提示并禁用自定义分享入口。
onShareAppMessage() {
  return this.shareConfig
}
```

该片段仅展示对象交接；业务应在对象准备成功前隐藏分享入口，避免返回未就绪数据。开启平台验签后 `imageUrl` 缺失或签名失败会降级为默认卡片。不得把后端私钥放入小程序包；此签名协议没有提供到期字段，业务访问权限和链接有效期另行控制，不能把卡片验签当成业务授权。

完整 Go 示例在 `wxapp/example_share_signature_test.go`；协议测试在 `wxapp/share_signature_test.go`，包含官方签名串 SHA256 锚点、独立 PSS 验签、篡改、多 AppID 和并发隔离。尚未进行微信真机分享验收。

## 9. 安全键盘（#142）

依据[安全键盘](https://developers.weixin.qq.com/miniprogram/dev/framework/open-ability/safe-password.html)与[input 组件](https://developers.weixin.qq.com/miniprogram/dev/component/input.html)，通过原生 `input type="safe-password"` 在微信客户端加密数字密码。后端只向业务返回受保护的密码摘要，不恢复或输出原密码；解密成功不等于登录成功。前端证书放入代码包，SM2 私钥仅存服务端。

### 9.1 密钥和证书准备

```go
func GenerateSM2KeyAndCSR(commonName string) (privatePEM, publicPEM, csrPEM []byte, err error)
func ValidateSafePasswordCertificate(privatePEM, certPEM []byte, now time.Time) (*SafePasswordCertificateInfo, error)
```

CommonName 通常为 AppID，要求有效 UTF-8、1–64 字节、无控制字符或首尾空白。生成结果依次为未加密 PKCS#8 私钥、SPKI 公钥、PKCS#10 CSR（PEM）。将 CSR 提交微信后台“安全键盘证书”申请，取得腾讯签发的证书再随小程序打包；工具不代替腾讯签发证书。

证书检查验证 SM2 公私钥对应与调用者给定非零时间下的有效期，返回 CommonName/NotBefore/NotAfter。**不验证腾讯 CA 链、吊销或 AppID 授权**，必须使用从微信后台取得的正确应用证书。运行时私钥仅接受单个未加密 PKCS#8 SM2 PEM，不接受裸 hex 或 RSA。国密底层固定 `github.com/emmansun/gmsm v0.44.1`，兼容 Go 1.25；本能力不意味着 API 通信/分享签名已支持 SM2。

### 9.2 共享 challenge 与服务构造

```go
func NewRedisSafePasswordStore(namespace string) (*RedisSafePasswordStore, error)
func NewSafePasswordService(c SafePasswordConfig) (*SafePasswordService, error)
func (s *SafePasswordService) IssueChallenge(ctx context.Context, binding SafePasswordBinding, profile SafePasswordHashProfile) (*SafePasswordChallenge, error)
func (s *SafePasswordService) Validate(ctx context.Context, binding SafePasswordBinding, nonce, ciphertext string) (*SafePasswordResult, error)
```

`SafePasswordConfig` 包含 AppID、PrivateKeyPEM、Store、ChallengeTTL、MaxClockSkew、WireProfile。TTL 由项目显式指定，至少 1 秒且不超过 MaxClockSkew；MaxClockSkew 零值为 5 分钟。没有 Store 或后端不可用时拒绝，不回退内存存储。Redis/Valkey 适配使用项目已注册的 GoFrame 默认连接；namespace 必填，1–128 个 ASCII 字母/数字/冒号/下划线/短横线。缓存键由 namespace 加 `:safe-password:` 及 AppID/nonce 的 SHA256 组成，不直接暴露会话标识；完整记录最多 16 KiB，使用 NX+PX 保存、Lua 精确比较并删除，一次 challenge 只有一个并发消费成功。

绑定 `SafePasswordBinding{SessionID, Purpose, CredentialID}` 必须由服务端当前会话、操作和凭证确定，不信任前端传来的身份或用途。记录同时绑定 AppID、摘要 profile、wire profile、签发时间及到期时间。Validate 先检查记录/绑定，再解密并验证安全字段，最后原子消费；错误绑定和非法密文不删除合法 challenge。消费成功后返回 `{Hash, ProfileID}`，仅用于服务端比较/保存，不发给前端、不记日志；业务仍执行自己的密码校验、鉴权与频控。

前端响应只有 `timeStamp`（Unix 秒）、`nonce`（高熵十进制字符串）、`salt`/`customHash`（有值才输出）和 `expiresAt`（Unix 秒），不包含私钥、绑定身份或密码摘要。nonce 保持字符串，不能转 JS Number。每次提交尝试结束后获取新 challenge。

### 9.3 摘要 profile 与现有密码迁移

`SafePasswordHashProfile{ID,Kind,HashLength,Salt,CustomHash}` 由服务端为凭证选择并持久化版本。HashLength 为**小写十六进制字符数**（2–1024 的偶数），不是字节数。Kind 支持：

- `pbkdf_hmac_hex`：header `00 07`，必须有稳定 Salt，CustomHash 为空。
- `custom`：header `00 00`，必须有 CustomHash 表达式，Salt 为空。官方 input 设置 custom-hash 后会忽略 salt 属性；如需稳定 salt，应由项目把它纳入表达式。库不执行表达式，也不代定摘要算法。

**不能每次尝试重新随机 salt 后直接比较摘要。** 盐、算法和 profile 版本按凭证稳定保存，每次变化的是 nonce。现有“明文密码 + bcrypt”不能直接拿新摘要比较旧 bcrypt；消费方须选择显式改密/重置迁移或设计有版本的验密兼容流程。本库不改业务密码表、不自动迁移凭证。

### 9.4 wire profile 与验证边界

官方文档公开 `V02_ + SM2(header + timestamp + NUL + hash + NUL + nonce + NUL + random)`，但没有完整规定外层编码、SM2 序列化和 header 总长度，也没有完整设备密文测试向量。因此构造器**不猜默认格式**，要求业务传入已通过其微信客户端样本确认的 `SafePasswordWireProfile`：

- Encoding：`hex` 或标准有 padding、无空白的 `base64`。
- CiphertextFormat：`asn1` 或带 `0x04` 未压缩点的 `c1c3c2`，不接受 C1C2C3。
- HeaderLength：2–64；如大于 2，HeaderSuffix 必须逐字节指定其余 header 内容，不能跳过未知字节。

未知版本、格式、header、摘要长度、时间戳、nonce、绑定或缓存错误均拒绝，固定 `SecurityError` 不附底层敏感值。Validate 输入最长 32 KiB。测试覆盖配置格式的独立合成密文、篡改、错钥、过期、绑定、重放和并发；**未用真实微信密文证明某个 profile 已适配具体设备**。消费方应在自己的已授权设备联调中确认 profile、基础库/客户端能力与摘要长度，再开启业务入口。

### 9.5 业务接线示例

完整 Go 示例见 `wxapp/example_safe_password_test.go`。原生页面片段见 `examples/wxapp/safe-password/`，由业务通过 `getApp().safePasswordAPI` 注入已认证的 `issue()`、`verify({nonce,encryptedValue})` 请求适配，并设置 `safePasswordUI.certPath/passwordLength`。示例不注册后端路由、不包含证书或真实凭证，不是可直接发布的独立小程序。verify 接口只有在协议校验和业务验密均成功后才返回 `{verified:true}`。

页面仅读取 blur/confirm 的 `encryptedValue` 与 `encryptError`，不使用/记录密码原文；处理加载、提交防重、页面卸载/旧 challenge 竞态、失败提示与提交后刷新。基础库从 2.18.0 支持；当前官方 input 文档注明鸿蒙 OS 不支持安全键盘相关参数，业务需按目标客户端核实能力，不能静默改成明文提交。示例仅做语法检查，未进行开发者工具构建或设备验收。

## 10. 离线配置辅助命令（#142）

安装正式版本后运行：

```bash
go install github.com/shyun-cc/shw-gf-lib/cmd/wxapp-security@v0.14.5
wxapp-security api-keygen -out ./wechat-api-materials
wxapp-security share-check -appid YOUR_APPID -private-key ./wechat-api-materials/private.pem
wxapp-security sm2-keygen -appid YOUR_APPID -out ./wechat-keyboard-materials
wxapp-security keyboard-check -private-key ./wechat-keyboard-materials/private.pem -certificate ./downloaded-keyboard.pem
wxapp-security api-check -config ./wechat-api-config.json
```

`api-keygen` 输出 private.pem/public.pem 与 Base64 编码的 32 字节 symmetric.key；单独 `rsa-keygen -out DIR [-bits 2048]` 仅输出 RSA 密钥对。`sm2-keygen` 输出 private.pem/public.pem/request.csr。out 必须是尚不存在的新目录且父目录存在，目录权限 0700、文件 0600，拒绝覆盖；写入失败保留部分结果以供操作方处理。私钥和对称密钥不上屏、不入仓库。

`api-check` 的 JSON 字段为 appId、encryptionAlgorithm、signatureAlgorithm、symmetricKeyBase64、symmetricKeySerial、privateKeyFile、platformCertificates。前两种算法分别填 `AES256_GCM`、`RSAwithSHA256`；platformCertificates 为“微信后台平台编号→证书文件路径”的映射。路径相对 JSON 文件目录解析；文件大小上限 64 KiB，未知 JSON 字段或多余内容拒绝。对称密钥字符串取 symmetric.key 内容并去掉末尾换行。检查实际使用 WithAPISecurity 的材料校验，不需要 AppSecret。

全部命令离线运行，不连接微信、不操作后台、不证明后台材料已配置正确或业务联调成功；证书检查只验证匹配及时间，不验证腾讯信任链。工具失败仅输出安全分类。工具与安全键盘回归由 PR CI 执行，真实厂商/真机验证由消费方完成。

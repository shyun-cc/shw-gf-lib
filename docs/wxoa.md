# wxoa — 微信公众号服务端 API（按范围裁决收官）

> 面向项目侧接入者。wxoa 已按 2026-08-24 范围裁决收官（底座 1 + 六基础域 51 + 消息族 37 + 收官批 35 + JS-SDK ticket 1 = 125 表项；终态清单见第 2 节挂起与不做）；本文各节与 `wxoa/` 源码逐字一致，若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

微信公众号（服务号）服务端 API 自主复刻，零厂商 SDK。以微信官方文档全集为覆盖基准（服务号侧文档为准，订阅号差异在各域 change 清点时核对）。

铺设顺序（wechat-ecosystem roadmap 方向解法）：底座（access_token 缓存与并发去重、回调验签解密、JS-SDK 签名、网页授权）→ 基础消息与订阅通知 → 自定义菜单 → 模板消息 → 客服消息 → 群发 → 素材 → 草稿与发布 → 用户管理 → 留言 → 数据统计 → 智能接口 → 卡券 → 长尾域。

## 2. 底座（2026-08-22 铺入，change wxoa-foundation）

- **`NewWxOAService(appID, secret)`**：公众号服务构造；`GetAccessToken(ctx)` 接口调用凭证——**Redis 缓存 TTL 7000s + redislock 并发去重 + double-check + 自旋**（缓存 key `wxoa:access_token:{appId}`），与 wxapp 同构。
- **`NewCallbackCrypt(appID, token, encodingAESKey)`**：回调验签解密——**复用 wecom 包 WXBizMsgCrypt**（同一 AES-CBC/SHA1/PKCS7 算法栈，receiveid 传 appID 实现公众号侧尾校验）；`VerifyURL`（服务器配置验证）/ `DecryptEvent`（回调验签解密）/ `EncryptMsg`（被动回复加密）。**wxoa→wecom 编译依赖由此建立**（`go list -deps ./wxoa | grep wecom` 可取证）。
- **异常两层**：`BizError`（errcode≠0，errors.As 捕获原始码值）与 `InfraError`（http/decode/token/encode/ticket/sign 分环节，Unwrap 底层）。
- **表驱动薄委托**：`Endpoint` 表项 + 统一 `invoke` 分发；底座表项 `token.fetch`（GET /cgi-bin/token——与小程序共用端点，取证复用归档 spec wxa-login-chain），后续各域表项见 2.5 / 2.7 / 2.9 节，当前共 125 表项。
- **挂起与不做**（2026-08-24 范围裁决终态）：已铺 菜单 / 用户 / 素材 / 留言 / 发布 / 草稿 / 群发 / 订阅通知 / 模板 / 客服 / 数据统计 / 智能 / 网页授权；JS-SDK 签名已由 #134 补齐；仍挂起 nlp 语义理解与 Img_Proc 图像处理（官方导航 404，需业务时补）；不做 卡券 / 门店 / 就医族 / 长辈就医 / 一物一码 / 发票族 / 非税 / 广告分析。

## 2.1 JS-SDK ticket 与网页签名（#134）

```go
func (s *WxOAService) GetJSAPITicket(ctx context.Context) (string, error)
func (s *WxOAService) SignJSURL(ctx context.Context, rawURL string) (*JSConfig, error)
```

`GetJSAPITicket` 复用 `GetAccessToken`，以 `GET /cgi-bin/ticket/getticket?access_token=...&type=jsapi` 获取 ticket（表项 `ticket.jsapi`）。默认 Redis 分组缓存 key 为 `wxoa:jsapi_ticket:{appId}`；相同 AppID 的实例共享缓存，因此同一 Redis 内不同账号须使用不同 AppID。有效期按微信 `expires_in` 提前刷新：减去约 10%（至少 1s、最多 200s），缓存最多 7000s；标准 7200s 响应缓存 7000s，剩余不足以缓存时仅返回本次结果。读取失败返回 `InfraError{Op:"ticket"}`；回写失败只输出固定警告 `wxoa jsapi_ticket 缓存回写失败`，仍返回 ticket。服务端凭证仅在后端使用，禁止将此方法的返回值提供给浏览器。

并发通过 `redis-lock:wxoa:jsapi_ticket:{appId}`（10s 租期）与 double-check 去重，等待者每 50ms 重读缓存，最多等待 12s（调用方 context 可更早取消）；持锁凭证请求最多 8s。抢锁失败不直接请求微信；Redis 故障不降级为无锁请求。缓存正常且请求在租期内完成时，并发 miss 仅一次获取；缓存写失败或极短有效期不保证后续调用复用。`42001/40014` 仍按现有契约返回 `BizError`，不新增强制刷新重试；异常还包括 `http/decode/ticket/sign` 环节的 `InfraError`。新链路不在错误中携带凭证请求 URL 或原始响应体。

`SignJSURL` 返回 `JSConfig`：`AppID string`、`Timestamp int64`（Unix 秒）、`NonceStr string`（16 随机字节转 32 位 hex）、`Signature string`。JSON 字段为 `appId/timestamp/nonceStr/signature`；不含 secret、access_token 或 ticket。签名原文为 `jsapi_ticket=...&noncestr=...&timestamp=...&url=...`，SHA-1 小写 hex，不对参数值再做 URL 编码。

调用方传浏览器实际完整 HTTP(S) URL；组件去掉第一个 `#` 及之后内容，保留此前的路径大小写、百分号编码、重复查询参数、参数顺序、加号和空值，不经 `url.Values` 重建。拒绝相对地址、非 HTTP(S)、含用户凭证、非法转义或空白的 URL；不访问网页 URL。业务项目负责签名接口的鉴权、域名许可、公众号 JS 接口安全域名配置和前端 `jsApiList`；本能力不执行 OAuth 登录或用户绑定。

接入示例（与 `ExampleWxOAService_SignJSURL` 一致，由 CI 编译；调用前需按项目方式配置默认 Redis 并注册 GoFrame Redis adapter）：

```go
s := wxoa.NewWxOAService("APP_ID", "APP_SECRET")
config, err := s.SignJSURL(ctx, "https://example.com/page?b=2&a=%2F")
if err != nil {
    return err
}
// 将 config 序列化到业务响应，前端另行配置 jsApiList。
_ = config
```

协议参考：[微信 JS-SDK](https://developers.weixin.qq.com/doc/offiaccount/OA_Web_Apps/JS-SDK.html)、[腾讯云官方接入示例](https://cloud.tencent.com/document/product/884/84160)（后者可访问并明确给出 ticket 路径、type 与缓存；微信旧文档地址在本次调查中无法访问）。包内 `jssdk_test.go` 使用 httptest + miniredis 覆盖并发、AppID 隔离、过期、异常、取消、凭证保护、签名向量与原始 URL；真实分享/扫码由消费方使用正式 tag 验证。

## 2.5 六基础域（2026-08-24 铺入，change wxoa-basic-domains，51 端点）

**自定义菜单（7）**：`CreateMenu`（3 一级×5 二级）/ `GetCurrentSelfMenu`（GET，手机端实际形态）/ `GetMenu` / `DeleteMenu`（GET）/ `AddConditionalMenu`（matchrule 个性化）/ `DeleteConditionalMenu`（POST menuid）/ `TryMatchMenu`（GET user_id）。

**用户管理（15）**：标签八件（创建/编辑/删除/列表 GET + 标签粉丝/批打/批取消/用户标签列表——打标签 ≤50 人/批）；用户七件 `GetUserInfo(openid, lang)`（GET；2021-12 后不出头像昵称）/ `BatchGetUserInfo` ≤100 / `GetUserList(nextOpenID)` GET 翻页 ≤10000 / 拉黑三件（≤20 人/批）/ `UpdateUserRemark`（认证服务号）。

**素材管理（9）**：永久五件（`AddMaterial` multipart type 四类 / `GetMaterial` / `GetMaterialCount` GET / `BatchGetMaterial` ≤20 / `DelMaterial`）；`UploadImg`（图文内图片 url）；临时三件（`UploadTempMedia` multipart 3 天有效 / `GetTempMedia` 二进制（video 形态 JSON video_url 二跳）/ `GetJSSDKVoice` speex 16K）。

**留言管理（8）**：开关双件 + 精选双件 + 回复评论 `AddCommentReply`（content ≤300 字）+ 删除评论/回复双件 + `ListComments`（type 0 全部/1 精选）——须留言功能权限。

**发布能力（5）**：`SubmitPublish`→`GetPublishStatus` 轮询 publish_id→`BatchGetPublished`/`GetPublishedArticle`/`DeletePublish`（articleID 字符串；不可逆；2025-07 起个人/未认证权限回收）。

**草稿箱（7）**：`SetDraftSwitch`（check_only 校验模式）/ `AddDraft`（≤8 篇）/ `GetDraft` / `BatchGetDrafts`（no_content 免正文）/ `GetDraftCount` GET / `DeleteDraft` / `UpdateDraft`（index 位替换）。

**工程形态**：wxoa 引入 `invokeMultipart`（part 名 media）与 `invokeRaw`（素材二进制/JSON 分流）双分发（wxapp 同构、包内独立）；`encodeQuery` 直接以 url.Values 落地（wxapp 踩过的转义坑预防在前）；`checkQuery` 修复 access_token 豁免位置（原在 Query 循环内，Query=nil 端点失效）。

## 2.7 消息族四域（2026-08-24 铺入，change wxoa-message-domains，37 端点）

**群发消息（8）**：`MassSendAll`（按标签全量）/ `MassSend`（openid 列表 ≤10000）/ `MassPreview` / `MassDelete` / `MassGet`（msg_id 状态）/ 速度双件 `MassSpeedGet`·`MassSpeedSet`（speed 0-4）/ `UploadNews`（**官方已切草稿箱**，表项保留注记）。

**订阅通知（7）**：模板管理六件（类目/公共标题/关键词/选用返回 priTmplId/列表/删除——与小程序共用 newtmpl 族）+ `SendSubsMsg`（bizsend：用户订阅后下发，一次性订阅每次消耗）。

**模板消息（7）**：`SetTmplIndustry`（每月一次、改行业删原模板）/ `GetTmplIndustry` / `AddTmpl`（short→template_id）/ `ListTmpl` / `DelTmpl` / `SendTmplMsg`（送达结果经 TEMPLATESENDJOBFINISH 事件）/ `QueryBlockedTmplMsg`。

**客服消息（15）**：账号七件（增改删 + `UploadKFHeadImg` multipart + `InviteKFWorker` 24h 确认 + GET 双件）；会话五件（创建 ≤100 并发/关闭/状态/列表/未接入）；消息三件 `SendKFMsg`（48h 窗口）/ `SetKFTyping`（**公众号形态 path 无 /business 段**，区别于 wxapp）/ `GetKFMsgRecords`（±10 天窗 ≤60 万条）。

## 2.9 收官批：数据统计/智能/网页授权（2026-08-24 铺入，change wxoa-final，35 端点）

**数据统计（21）**：统一 `(ctx, beginDate, endDate)`——用户双件（增减/累计）、图文十件（群发每日·阅读概况·分时·总·转发·分时·发表阅读·分享·概况总·详细）、消息七件（概况·分时·周·月·分布三态）、接口双件（被动回复概要·分布）。

**智能接口（10）**：语音三件 `VoiceTranslate`（文本翻译）/ `VoiceUploadReco`（语音 base64 上传转文字返回 voice_id）/ `VoiceQueryReco`（轮询结果）；OCR 七件（comm/idcard/driving/drivinglicense/bankcard/bizlicense/**menu 纸质菜单**——GET query img_url 形态，与 wxapp 双形态并存）。

**网页授权（4 + helper）**：`GetOAuthAccessToken(code)`（code 一次 5 分钟；快照账号 is_snapshotuser）/ `RefreshOAuthToken`（30 天）/ `GetOAuthUserInfo`（仅 snsapi_userinfo）/ `CheckOAuthAuth`；`BuildOAuthAuthorizeURL`（前端跳转构造 helper，非表项）。sns 体系用网页授权专用 token——Query 显式声明凭证参数不走注入。

## 3. 依赖方向与复用

- **wxoa → wecom（单向）**：回调验签解密复用 wecom 包的 `WXBizMsgCrypt`（公众号侧为 AppId 尾校验变体）——不重复实现消息加解密。
- 异常两层（`BizError` errcode 业务异常 / `InfraError` 基础设施异常）与表驱动薄委托（`Endpoint` 表项 + 统一 `invoke`）遵循四包统一工程模式，见 docs/wxapp.md 第 3 节。

## 4. 显式排除

- **EasyWeChat 不迁**：Go 侧定案全自主复刻、零厂商 SDK（含 EasyWeChat 形态的工厂封装）。

## 凭证缓存回写日志（#137，v0.14.2）

凭证缓存 SET 失败时，本次仍返回已获取的 token/ticket，后续调用可能重新获取。组件只记录固定警告：`wxoa access_token 缓存回写失败`、`wxoa jsapi_ticket 缓存回写失败`；不附加原始 Redis 错误、SET 参数或凭证值。原因是 GoFrame Redis adapter 会在错误中携带完整命令参数。此约束只覆盖组件自身的缓存回写警告，不代替调用方的 Redis trace、外部日志采集与凭证保护配置。

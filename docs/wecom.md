# wecom — 企业微信 API 自主复刻

> 面向项目侧接入者（第 1–3 节）与库维护者（加读第 4 节）。事实来源是 `wecom/` 源码，本文的代码示例、签名与事件名均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

企业微信 API 自主复刻（零厂商 SDK），四块能力：

- **`WXBizMsgCrypt`**：回调消息加解密（AES-256-CBC + 32 字节 PKCS7 + SHA1 四元素字典序签名 + CorpID 尾校验）；
- **`Client`**：API 客户端（GET/POST 自动带 access_token；errcode 42001/40014 token 失效**恰一次**强制刷新重试）+ OAuth 域方法（`GetUserinfoByCode` / `GetUserDetailByTicket`）；
- **`TokenManager`**：access_token 管理（Redis 缓存 TTL 7000s + redislock 并发去重 + double-check + 自旋）；
- **OAuth 编排**（`OauthManager` + `StateStore`：双 scope 授权 URL、state 一次性消费、前端回跳）、**扫码登录 URL**、**9 种消息体**（Text/Markdown/Image/Voice/Video/…，影子类型避免 MarshalJSON 递归）。

依赖方向：wecom → redislock + events（回调经验分发派）。

## 2. 快速接入

### 回调验签解密 + 事件派发

```go
package handler

import (
	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/wecom"
)

var crypt *wecom.WXBizMsgCrypt

func init() {
	var err error
	crypt, err = wecom.NewWXBizMsgCrypt("<Token>", "<EncodingAESKey>", "<CorpID>")
	if err != nil {
		panic(err) // AESKey base64 解码失败等构造期错误
	}
	// 订阅通讯录变更（回调明文经 events 派发，族事件统一走 change_contact）
	events.Subscribe(wecom.EventChangeContact, 0, func(ctx context.Context, e events.Event) {
		ev := e.Payload.(wecom.CallbackEvent) // 值类型；Fields 含 XML 叶子字段 map
		_ = ev
	})
}

// GET 首次配置回调的 URL 验证
func Verify(r *ghttp.Request) {
	echo, err := crypt.VerifyUrl(msgSignature, timestamp, nonce, echostr)
	// echo 为解密后的明文 echostr，原样返回给微信

	// POST 事件回调：验签 + 解密 + events 派发（26 种事件名，另有 wecom.msg.<MsgType> 消息兜底）
	ev, err := crypt.HandleCallback(r.Context(), events.Default(),
		msgSignature, timestamp, nonce, encryptedXML)
}
```

### API 调用与消息推送

```go
client := wecom.NewClient(wecom.Credential{
	CorpID:  "<CorpID>",
	AgentID: 1000002,          // 自建应用必填
	Secret:  "<Secret>",
	AppType: wecom.AppAgent,   // 或 AppContact（通讯录同步应用，仅 corpID+通讯录 secret）
})

resp, err := client.Get(ctx, "/cgi-bin/user/simplelist",
	map[string]string{"department_id": "1", "fetch_child": "1"})

// 消息推送：Msg 类型只序列化 msgtype + 载体段（如 {"msgtype":"text","text":{"content":…}}），
// 接收人等外层字段（touser/agentid）由调用方在外层 map 组装
err = client.SendMessage(ctx, map[string]any{
	"touser":  "zhangsan",
	"agentid": 1000002,
	"msg":     wecom.TextMsg{Content: "hello"}, // "msg" 键经 SendMessage 自动展开合并进外层路由字段
})
```

### OAuth 网页授权（编排层）

```go
// 构造：凭证与回跳地址经构造注入（redirectURI 为企微后台「网页授权及 JS-SDK」配置的可信域名回跳地址）
m := wecom.NewOauthManager(client, wecom.NewStateStore(), corpID, agentID, "https://app.example.com/callback")

// 步骤 1：生成授权 URL——内部自动创建 state（存 returnURL，TTL 30s）：
u, err := m.BuildAuthorizeURL(ctx, "https://fe.example.com/dashboard")          // snsapi_base 静默授权
u, err = m.BuildPrivateInfoAuthorizeURL(ctx, "https://fe.example.com/dashboard") // snsapi_privateinfo 敏感信息授权

// 步骤 2：企微回跳处理——消费 state（一次性），有效时返回拼接 code 的前端回跳 URL：
redirect, ok, err := m.BuildFrontendRedirectURL(ctx, state, code) // ok=false 表 state 无效/过期/已消费

// 步骤 3：code 换身份；敏感信息授权后可再以 user_ticket 换手机号、真实姓名等：
userinfo, err := m.GetUserinfoByCode(ctx, code)
detail, err := m.GetUserDetailByTicket(ctx, userinfo["user_ticket"].(string))
```

也可只用底层件：拼接函数 `BuildAuthorizeURL` / `BuildPrivateInfoAuthorizeURL`（双 scope，格式仅 scope 值差异）/ `BuildFrontendRedirectURL` / `BuildQrConnectURL` 为纯函数；`StateStore` 可独立使用（`Create(ctx, returnURL)` / `Consume(ctx, state) (returnURL, ok, err)`，key `wecom:oauth:state:{state}`、TTL 30s、GETDEL 原子一次性消费，需 Redis ≥6.2）；域方法也可直接走 `client.GetUserinfoByCode(ctx, code)` / `client.GetUserDetailByTicket(ctx, userTicket)`。

## 3. 契约

**WXBizMsgCrypt**：`NewWXBizMsgCrypt(token, encodingAESKey, corpID)`（AESKey 非法返回构造错误）。`VerifyUrl` / `DecryptEvent`（验签→解密→CorpID 尾校验） / `EncryptMsg`（被动回复加密）/ `Decrypt` / `Encrypt`。签名：SHA1(token, timestamp, nonce, encryptMsg) 四元素**字典序**拼接；明文结构 `random[16] + msgLen[4] + msg + receiveid`；PKCS7 块 32 字节；比较恒时。

**Client token 失效重试**：errcode 42001/40014 → `Invalidate` 清缓存 → 强制刷新重试**恰一次**；仍失败或 errcode ≠0 返回 `*BizError`。

**异常两层**（`errors.go`，与 wxapp 同构、类型各自包内定义）：

- **`BizError`**（业务异常）：errcode ≠ 0 时返回，`Errcode` 携带企微原始数值、`Errmsg` 原文——`errors.As(err, &biz)` 差异化捕获（如 40029 invalid code 与 60011 无权限走不同分支）；
- **`InfraError`**（基础设施异常）：HTTP 请求失败 / 响应非 JSON / 凭证链路失败——`Op` 标环节（http/decode/token/encode）、`Unwrap` 暴露底层错误，可触发告警分流。两层类型互斥（errcode 判定发生在 JSON 解码成功之后）。

**表驱动薄委托**：域方法是一行 `Endpoint` 表项（Name/Method/Path/Query 清单/Body 标志）+ 一行薄委托，经统一 `invoke` 分发（query 键集按表项校验、token 自动注入、42001/40014 重试、异常分层）。当前表项 369 个（`auth.getuserinfo` GET /cgi-bin/auth/getuserinfo、`auth.getuserdetail` POST /cgi-bin/auth/getuserdetail 起步，其余各域见下文分域段落）；`Client.Get`/`Client.Post` 保留为通用逃生口。新增端点 = 在 `endpoint.go` 加一行表项 + 薄委托方法 + 测试用例（表驱动端点断言测试遍历全量表项且断言计数相等，缺用例即红）。

**TokenManager 并发语义**：缓存键 `wecom:<corpID>:<agentID|contact>:access_token`、锁 `wecom:token:<corpID>` TTL 10s、自旋 5×200ms、兜底直接 fetch（与 wxapp 同构）。

**回调载荷身份**：`CallbackEvent` 含 `CorpID`（取自 crypt receiveid）与 `AgentID`（应用回调明文 AgentID 在位，通讯录回调为空）——订阅方可直接按企业/应用归因。

**事件派发名**：`subscribe` / `unsubscribe` → 各自事件；`change_contact` 及 change_/create_/delete_/update_ 前缀族 → EventChangeContact（订阅方按 Fields.ChangeType 细分）；消息类 → `wecom.msg.<MsgType>`。

**消息体序列化**：9 种消息导出类型（Text / Markdown / Image / Voice / Video / File / News / MpNews / TextCard）各带 MarshalJSON——只序列化 `msgtype` + 载体段（如 `{"msgtype":"text","text":{"content":…}}`）；touser / agentid 等外层字段由调用方在外层 map 组装（嵌入 Msg 即可）。影子类型拆开「对外字段名」与「序列化形态」，避免 MarshalJSON 内再 json.Marshal 自身的递归。

**state 存取（StateStore）**：`Create(ctx, returnURL)` 生成 32 字符随机 state 并 SETEX TTL 30s（key `wecom:oauth:state:{state}`）；`Consume(ctx, state)` GETDEL 原子一次性取回 returnURL（再次消费 ok=false；需 Redis ≥6.2）。TTL 30s 与 key 格式对齐 PHP `WecomOauthStateStore`（多项目沉淀语义）。旧的 `ConsumeState`（SETNX 300s 纯防重放）已移除，改用 `StateStore.Consume`。

**会议域·子域③会中控制（17 端点 + meeting_change 事件，2026-08-22 首批 7 + 2026-08-23 补数 10）**：realcontrol 族 `SetMeetingRealControl`（全体静音/锁定/聊天权限/等候室开关等）/ `SetMeetingCohost` / `MuteMeetingUser` / `SwitchMeetingUserVideo` / `ManageWaitingroomUsers` / `KickoutMeetingUsers` / `DismissMeeting` / `CloseMeetingScreenShare`（instance_id 须匹配设备，不支持 MRA）/ `SetMeetingNicknames`（≤20 字符批量）；投票族八件 `CreateMeetingPollTheme`（问题 ≤10、选项 2-10 项）/ `UpdateMeetingPollTheme`（全覆盖修改）/ `ListMeetingPolls` / `GetMeetingPollTheme`（不含结果）/ `GetMeetingPollDetail`（含票数与比率）/ `DeleteMeetingPoll`（theme/instance 二选一，同传以 poll_id 为准）/ `StartMeetingPoll` / `FinishMeetingPoll`——全部仅进行中会议可调、操作者须为主持人或会议管理员。回调事件 `meeting_change`（ChangeType: modify_meeting/cancel_meeting，订阅方按 Fields 细分）。端点清点基准：change wecom-meeting-control-domain + wecom-meeting-control2 proposal（17 页取证，挂起清单清零）。

**会议域·子域⑤布局背景/录制/高级账号（30 端点，2026-08-23 铺入）**：布局族 `ListMeetingLayoutTemplates`（GET 无参首例）/ `AddMeetingLayout`（≤10 个）/ `UpdateMeetingLayout` / `SetMeetingDefaultLayout`（空串恢复默认）；高级布局族（仅 H.323/SIP 终端，≤20 个）`Add/Update/Apply/ListMeetingAdvancedLayout(s)`（apply ≤20 人个性布局，优先级 个性>自定义>默认）/ `GetMeetingUserLayout` / `BatchDeleteMeetingLayouts`（≤20 个）；背景族 `AddMeetingBackground`（≤7 张 PNG ≤10MB ≥1920x1080，异步上传）/ `SetMeetingDefaultBackground`（空串恢复黑色）/ `List/Delete/BatchDeleteMeetingBackground(s)`；录制族 `ListMeetingRecords`（时间窗 ≤31 天）/ `GetMeetingRecordStatistics` / `UpdateMeetingRecordSharingConfig`（权限 0-5，4/5 不支持 Rooms）/ `DeleteMeetingRecord`（全删）/ `DeleteMeetingRecordFile` / `GetMeetingRecordFile`（含纪要）/ `ListMeetingRecordFiles`（播放/下载地址）；转写族 `ListMeetingTranscriptParagraphs` / `GetMeetingTranscriptDetail` / `SearchMeetingTranscript`；会议高级账号（**异步任务形态**）`SubmitMeetingVIPAddJob`+`GetMeetingVIPAddJobResult` / `SubmitMeetingVIPDelJob`+`GetMeetingVIPDelJobResult`（≤100 人，jobid 查询）/ `ListMeetingVIPAccounts`（与邮件域 exmail/vip 不同 path）。端点清点基准：change wecom-meeting-layout-record proposal（28 叶子 30 端点——高级账号两页各含双接口）。

**会议域·子域④Webinar/PSTN/Rooms/MRA（33 端点，2026-08-23 铺入，会议域收官）**：Webinar 14 件 `CreateWebinar`（admission_type 0 公开/1 报名/2 密码，回放须开云录制）/ `UpdateWebinar` / `CancelWebinar` / `GetWebinar`（meetingid 与 meeting_code 二选一）/ 嘉宾 `ListWebinarGuests` / `UpdateWebinarGuestList`（内部 userid、外部 area+phone）/ `UpdateWebinarWarmUp`（图片 ≤5M 视频 ≤1G 二选一）/ 报名七件 `Set/GetWebinarEnrollConfig` / `QueryWebinarEnrollIDByTmpOpenid`（≤500 人）/ `ListWebinarEnrolls`（status 0-3）/ `ApproveWebinarEnrolls`（action 1 取消批准/2 拒绝/3 批准）/ `ImportWebinarEnrolls`（返回 enroll_code 电话入会凭证）/ `DeleteWebinarEnrolls`；PSTN 3 件 `PhoneCallout`（≤50 路/次，Webinar 不支持）/ `GetPhoneCalloutStatus` / `GetPhoneTmpOpenid`（≤20 号码，接通后才返回）；Rooms 12 件 预定（≤24h 非周期）/释放/列表（模糊）/详情/配置项/会议室下会议（≤90 天窗）/设备/控制器列表/资源存量（无 body）/呼叫三件（`CallMeetingRoom` 返回 invite_id，目标 Rooms 或 MRA 地址二选一；应答状态 0-6）；MRA 4 件 `QueryMRAStatus` / `SetMRADefaultLayout`（1 等分/2 全屏/3 1+N）/ `SetMRARaiseHand` / `HangupMRA`。**会议域至此收官：5 子域 104 端点 + meeting_change 事件**。端点清点基准：change wecom-meeting-webinar-pstn-rooms-mra proposal（32 叶子 33 端点——Webinar 报名删除单列）。

**基础杂项与安全管理（19 端点，2026-08-24 铺入，wecom 按范围裁决收官批）**：域名 IP 三件 `GetAPIDomainIPs` / `GetCallbackIPs` / `GetServerDomainIPs`（GET，仅自建+域名IP配置）；ID 转换 `ConvertTmpExternalUserid`（business_type 会议/收集表/智能表 × user_type 四类 ≤100）；操作日志双件 `ListMemberOperLogs`（≤7 天窗·可见范围内）/ `ListAdminOperLogs`（全量不筛可见范围）；安全管理——截屏 `GetScreenOperRecords` 与文件防泄漏 `GetFileOperRecords`（≤14 天窗，operation.type/source 细分）、**设备管理六件**（导入 Windows mac_addr/Mac seq_no ≤100、type 1 可信企业/2 未知/3 可信个人、status 2/4/6 脱敏、确认/驳回仅 status 3/4）、**security vip 五件**（异步任务形态，与会议/邮件 vip 族不同 path 各自独立，仅自建+「安全与管理」配置）。

**邮件域（26 端点，2026-08-23 铺入，OA 族）**：账号 `ActivateEmailAccount`（禁用/启用，userid 与 publicemail_id 至少一）/ `GetEmailNewCount`；发信三件 `SendMail` / `SendScheduleMail` / `SendMeetingMail`（**同 path compose_send**，body 结构区分——日程带 schedule、会议再加 meeting.meeting_admins 仅 1 人；附件 ≤200 个且加正文 ≤50M）；收件 `GetMailList`（cursor 翻页）/ `ReadMail`（eml 内容）；应用邮箱 `UpdateEmailAlias`（原账号转别名保留收信）/ `GetEmailAlias`；邮件群组五件 CRUD + `SearchMailGroup`（fuzzy 0 全量/1 模糊）；公共邮箱七件（CRUD 中 get 为 POST 批量 id_list + `SearchPublicMail` + 客户端专用密码 `ListPublicMailAuthCodes` / `DeletePublicMailAuthCode`——**密码明文仅创建/更新时返回一次**）；高级功能账号 `AddVIPAccounts` / `RemoveVIPAccounts`（≤100 人）/ `ListVIPAccounts`（has_more 翻页）；用户功能属性 `GetUserOptions` / `UpdateUserOptions`（type 1-4：强制安全登录/IMAP/POP/安全登录）。邮件回调事件（97495/100180）报文格式未取证，事件常量挂起。端点清点基准：change wecom-mail-domain proposal（31 叶子 26 端点页逐页取证）。

**会议域·子域②高级管理（19 端点，2026-08-22 铺入）**：受邀成员 2（`GetMeetingInvitees` / `SetMeetingInvitees`，创建限 300 人、更新限 2000）、专属参会链接 2（`CreateCustomerShortURL` / `GetCustomerShortURL`）、会中/已参会 2（`GetRealtimeAttendeeList` / `GetAttendeeList`）、等候室 2（`GetWaitingroomCurrentUserList` / `GetWaitingroomUserList`）、`CheckDeviceInMeeting`、嘉宾 2（`GetMeetingGuests` / `SetMeetingGuests`）、`GetMeetingQuality`（健康度）、报名族 7（`Set/GetMeetingEnrollConfig` / `QueryMeetingEnrollByTmpOpenid` / `ListMeetingEnroll` / `ApproveMeetingEnroll` / `ImportMeetingEnroll` / `DeleteMeetingEnroll`）。导航 24 叶子中 5 个为子域①基础族同端点重复（不重复立表项，留痕）。端点清点基准：change wecom-meeting-advanced-domain proposal（19 页逐页取证）。

**会议域·子域①基础管理（5 端点，2026-08-22 铺入）**：`CreateMeeting` / `UpdateMeeting` / `CancelMeeting` / `GetUserMeetingIDList` / `GetMeetingInfo`。会议域为巨型域（约 108 端点拆 5 子域），高级管理/会中控制/Webinar/Rooms/录制等归后续子域；回调事件族（97451）随子域②接线。端点清点基准：change wecom-meeting-basic-domain proposal（5 页逐页取证）。

**日程域（11 端点，2026-08-22 铺入，OA 族）**：日历 4（`AddCalendar` / `UpdateCalendar` / `GetCalendar` / `DelCalendar`）、日程 7（`AddSchedule` / `UpdateSchedule`（重复日程 op_mode 语义见官方 96204）/ `AddScheduleAttendees` / `DelScheduleAttendees` / `ListScheduleByCalendar` / `GetSchedule` / `CancelSchedule`）。回调事件 5：`modify_calendar` / `delete_calendar` / `modify_schedule` / `delete_schedule` / `respond_schedule`（回执结果需调查询接口）各自专属事件名。端点清点基准：change wecom-schedule-domain proposal（19 叶子中 11 API 页逐页取证；96204 为说明页留痕）。

**审批域（11 端点，2026-08-22 铺入，OA 族）**：模板 3（`GetApprovalTemplateDetail` / `CreateApprovalTemplate` / `UpdateApprovalTemplate`）、申请与查询 4（`ApplyApprovalEvent` / `GetApprovalInfo` / `GetApprovalDetail` / `GetCorpApprovalData` 旧接口）、假期 3（`GetVacationCorpConf` / `GetUserVacationQuota` / `SetOneUserVacationQuota`）、自建审批 `GetOpenApprovalData`（thirdNo）。回调事件：审批状态变化 `sys_approval_change` 与自建应用审批 `open_approval_change` 各自专属事件名。端点清点基准：change wecom-approval-domain proposal（12 页逐页取证）。

**打卡域（15 端点，2026-08-22 铺入，OA 族首域）**：规则 6（`GetCorpCheckinOption` / `GetCheckinOption` / `AddCheckinOption` / `UpdateCheckinOption` / `ClearCheckinOptionArrayField` / `DelCheckinOption`）、数据 3（`GetCheckinData` / `GetCheckinDayData` / `GetCheckinMonthData`）、排班 2（`GetCheckinScheduList` / `SetCheckinScheduList`）、补卡/记录/人脸 3（`PunchCorrection` / `AddCheckinRecord` / `AddCheckinUserFace`）、设备 1（`GetHardwareCheckinData`）。全部 map 透传。端点清点基准：change wecom-checkin-domain proposal（12 页逐页取证，98041 一页 4 接口）。

**微信客服域（26 端点，2026-08-22 铺入）**：升级服务 3（`GetUpgradeServiceConfig` / `UpgradeService` / `CancelUpgradeService`）、客服账号 5（`Add/Del/Update/ListKfAccount` / `GetKfContactWay`）、接待人员 3（`Add/Del/ListKfServicer`）、会话分配 2（`GetKfServiceState` / `TransKfServiceState`）、消息 2（`SendKfMsg` / `SendKfMsgOnEvent`，支持 msg 键展开）、客户基础信息 `BatchGetKfCustomer`、统计 2（`GetKfCorpStatistic` / `GetKfServicerStatistic`）、知识库 8（`Add/Del/Mod/ListKfGroup` + `Add/Del/Mod/ListKfIntent`）。回调事件：客服消息 `kf_msg_or_event` 派发 `wecom.kf_msg_or_event`。端点清点基准：change wecom-kf-domain proposal（15 页逐页取证，95971/95972 各一页 4 接口）。

**客户联系域·子域⑥（12 端点，2026-08-22 铺入，域收官）**：商品图册 5（`Add/Get/Get…List/Update/DeleteProductAlbum`）、聊天敏感词 5（`AddInterceptRule` / `GetInterceptRuleList` / `GetInterceptRule` / `UpdateInterceptRule` / `DelInterceptRule`）、`UploadAttachment`（multipart 附件上传，attachmentType 1=朋友圈 2=商品图册）、`GetServedContactList`（已服务外部联系人）。**客户联系域六子域至此全量闭环**（①客户管理标签 20 ②继承 7 ③客户群联系我 13 ④朋友圈获客 23 ⑤群发统计 14 ⑥图册杂项 12 = 89 端点）。端点清点基准：change wecom-contact-misc-domain proposal（4 页逐页取证，95096/95097 各一页 5 接口）。

**客户联系域·子域⑤（14 端点，2026-08-22 铺入）**：企业群发 6（`AddMsgTemplate` / `RemindGroupmsgSend` / `CancelGroupmsgSend` / `GetGroupmsgList` / `GetGroupmsgTask` / `GetGroupmsgSendResult`）、新客户欢迎语 `SendWelcomeMsg`（welcome_code 来自 add_external_contact 事件回调）、入群欢迎语素材 4（`Add/ Edit/Get/DelGroupWelcomeTemplate`，text 占位符 %NICKNAME%、载体类型按优先级取一）、统计 3（`GetUserBehaviorData` / `GetGroupChatStatistic` 按群主 / `GetGroupChatStatisticByDay` 按自然日）。端点清点基准：change wecom-contact-msgpush-stats proposal（8 页逐页取证，93338 一页 3 接口、92366 一页 4 接口）。

**客户联系域·子域④（23 端点，2026-08-22 铺入）**：客户朋友圈 14（发表任务 `AddMomentTask` / `GetMomentTaskResult` / `CancelMomentTask`、发表记录查询 `GetMomentList` / `GetMomentTask` / `GetMomentCustomerList` / `GetMomentSendResult` / `GetMomentComments`、朋友圈规则组 6 `List/Get/GetRange/Create/Edit/DeleteMomentStrategy`）、获客助手 9（获客链接 5 `List/Get/Create/Update/DeleteAcquisitionLink` + `GetAcquisitionCustomer` / `GetAcquisitionQuota` / `GetAcquisitionStatistic` / `GetAcquisitionChatInfo`——chat_key 来自 message_from_customer 事件回调，30 分钟内有效）。回调事件：获客助手族 `customer_acquisition`（11 ChangeType：balance_*/link_unavailable/delete_link/quota_expire_soon/open_profile/friend_request/customer_start_chat/message_from_customer/del_follow_user）派发 `wecom.customer_acquisition`。端点清点基准：change wecom-contact-moment-acquisition proposal（9 页逐页取证；99596 运营指南页非 API 留痕）。

**客户联系域·子域③（13 端点，2026-08-22 铺入）**：客户群 3（`ListGroupChat` / `GetGroupChat` / `OpenGidToChatId`）、「联系我」6（`AddContactWay` / `GetContactWay` / `ListContactWay` / `UpdateContactWay` / `DelContactWay` / `CloseTempChat`）、进群方式 4（`AddJoinWay` / `GetJoinWay` / `UpdateJoinWay` / `DelJoinWay`）。回调事件：客户群变更 `change_external_chat`（create/update/dismiss）派发 `wecom.change_external_chat`（external 族第三员）。端点清点基准：change wecom-contact-groupchat-domain proposal（5 页逐页取证，92228 一页 6 接口、92229 一页 4 接口）。

**客户联系域·子域②继承族（7 端点，2026-08-22 铺入）**：在职继承 3（`TransferOnjobCustomer` / `GetOnjobTransferResult` / `TransferOnjobGroupChat`）+ 离职继承 4（`GetUnassignedList` / `TransferResignedCustomer` / `GetResignedTransferResult` / `TransferResignedGroupChat`），全部 map body 透传；接替失败回调 transfer_fail 已随子域①派发（EventChangeExternalContact 的 ChangeType 之一）。端点清点基准：change wecom-contact-inherit-domain proposal（7 页逐页取证）。

**群机器人 webhook / 多应用体系 / JS-SDK（2026-08-22 铺入，change wecom-robot-multiapp-jsapi）**：
- **`NewRobotClient(key)`**：仅凭 webhook key（无 access_token）POST `qyapi.weixin.qq.com/cgi-bin/webhook/send?key=KEY`；六 msgtype `SendRobotText`（支持 mentioned 列表）/ `SendRobotMarkdown` / `SendRobotImage`（base64+md5 预计算）/ `SendRobotNews` / `SendRobotFile` / `SendRobotTemplateCard`——表驱动桩断言六类型。
- **`NewCorpManager(corpID)` 多应用体系**：`Register(name, Credential, callbackToken, callbackAESKey)` 同 corpID 注册一个通讯录应用 + 多个自建应用（corpID 不一致拒绝、重名覆盖）；`Client(name)` / `Crypt(name)` 按名取用（未知名报错）；token 缓存键含 agentID 天然隔离、回调各持独立 Token/AESKey（不同应用密文互解失败）。
- **`NewTicketManager` + `BuildJsApiConfig`**：jsapi_ticket 缓存 TTL 7000s + redislock 并发去重 + forceRefresh（缓存键 `wecom:<corp>:<agent|contact>:jsapi_ticket`，端点 GET /cgi-bin/get_jsapi_ticket——挂起注记：限流解除后补页面取证）；`BuildJsApiConfig(ticket, nonce, timestamp, url)` 返回五键，signature=SHA1("jsapi_ticket=…&noncestr=…&timestamp=…&url=…")。
- **扫码登录嵌入式**：`BuildQrConnectParams(corpID, agentID, redirectURI, state, href)` 五键参数集（app 内嵌二维码用）；跳转模式 `BuildQrConnectURL`（login_type=CorpApp）既有。

**客户联系域·子域①（20 端点，2026-08-22 铺入）**：客户管理 5（`GetFollowUserList` / `ListExternalContacts` / `GetExternalContact`（cursor 分页）/ `BatchGetExternalContactsByUser` / `UpdateExternalContactRemark`）、客户联系规则组 6（`ListCustomerStrategy` / `GetCustomerStrategy` / `GetCustomerStrategyRange` / `CreateCustomerStrategy` / `EditCustomerStrategy` / `DeleteCustomerStrategy`）、企业标签 4（`GetCorpTagList` / `AddCorpTag` / `EditCorpTag` / `DelCorpTag`）、规则组标签 4（`GetStrategyTagList` / `AddStrategyTag` / `EditStrategyTag` / `DelStrategyTag`）、`MarkExternalContactTag`。**回调事件修正**：`change_external_contact`（ChangeType：add/edit/add_half/del_external_contact/del_follow_user/transfer_fail）与 `change_external_tag`（create/update/delete/shuffle）现派发各自专属事件 `wecom.change_external_contact` / `wecom.change_external_tag`——此前误落通讯录事件 wecom.change_contact（行为修正，订阅方需注意）。端点清点基准：change wecom-contact-customer-tags proposal（10 页逐页取证，聚合页 94883 一页 6 接口、92117/94882 各一页 4 接口）。

**应用管理域（11 端点，2026-08-22 铺入）**：应用信息 3（`GetAgent` / `ListAgents` / `SetAgent`）、自定义菜单 3（`CreateMenu` / `GetMenu` / `DeleteMenu`，agentid 走 query）、工作台自定义展示 5（`SetWorkbenchTemplate` / `GetWorkbenchTemplate` / `SetWorkbenchData` / `BatchSetWorkbenchData` / `GetWorkbenchData`，body 透传）。回调事件：工作台自定义模式开关切换派发 `wecom.switch_workbench_mode`（Fields 含 AgentID/UserID/Mode）。端点清点基准：change wecom-agent-domain proposal（6 页逐页取证，92535 一页五接口）。

**消息收发域（8 端点，2026-08-22 铺入）**：应用消息 `SendMessage`（**msg 键展开**：body `"msg"` 键放 message.go 的 9 种 Msg 类型即自动序列化合并进外层路由字段，原生 map 内联载体透传）、`UpdateTemplateCard`（更新模版卡片）、`RecallMessage`（撤回）；群聊会话四件 `CreateAppChat` / `UpdateAppChat` / `GetAppChat` / `SendAppChat`（发送支持 msg 键展开）；家校「学校通知」`SendSchoolNotice`（path 在 externalcontact 族、官方导航列消息区，归类裁定留痕）。智能机器人（接收/被动回复/主动回复 3 条目）为独立接入形态，留长尾域评估。接收消息与事件回调能力见前文（HandleCallback/事件派发名）。端点清点基准：change wecom-message-domain proposal（8 页逐页取证）。

**素材管理域（6 端点，2026-08-22 铺入）**：`UploadMedia(ctx, type, filename, content)`（multipart 上传临时素材，type=image/voice/video/file，media_id 3 天有效）、`UploadImg`（multipart，得永久 URL，2MB 内、仅企微内可用）、`UploadMediaByURL` / `GetUploadByURLResult`（异步上传任务：JSON body，完成后回调 `wecom.upload_media_job_finish`——仅通知不含结果，需调查询接口取 media_id）、`GetMedia` / `GetJssdkVoice`（二进制下载：返回 ContentType/filename/字节；失败响应为 JSON errcode → BizError）。**两类分发扩展**（四包后续域复用）：`InvokeMultipart(ctx, ep, query, filename, content)` 与 `InvokeDownload(ctx, ep, query)`——异常分层与 token 注入同 invoke。**注记**：>20M 临时素材需 Range 分块下载（分片 16 字节倍数），`InvokeDownload` 单次全量返回，大文件场景由调用方经注入的 HTTPClient 处理；企微无「永久素材族」——那是公众平台接口（wxoa 域），本域不含。端点清点基准：change wecom-media-domain proposal（官方文档 6 页逐页取证；服务商镜像页 96486 留痕不立端点）。

**通讯录管理域（41 端点，2026-08-22 铺入）**：六子域全量——成员管理 15（`CreateUser`/`GetUser`/`UpdateUser`/`DeleteUser`/`BatchDeleteUser`/`SimpleListUser`/`ListUser`/`ConvertToOpenid`/`ConvertToUserid`/`AuthSucc`/`InviteUser`/`GetJoinQrcode`/`GetUserIdByMobile`/`GetUserIdByEmail`/`ListUserId`）、部门管理 6（`CreateDepartment`/`UpdateDepartment`/`DeleteDepartment`/`ListDepartment`/`SimpleListDepartment`/`GetDepartment`）、标签管理 7（`CreateTag`/`UpdateTag`/`DeleteTag`/`GetTag`/`AddTagUsers`/`DelTagUsers`/`ListTag`）、查看权限规则 4（`CreateContactRule`/`ListContactRule`/`UpdateContactRule`/`DeleteContactRule`，仅通讯录同步应用可调）、异步导入 4（`BatchSyncUser`/`BatchReplaceUser`/`BatchReplaceParty`/`BatchGetResult`）、异步导出 5（`ExportSimpleUser`/`ExportUser`/`ExportDepartment`/`ExportTagUser`/`ExportGetResult`）。签名形态：GET 端点具名参数（bool→`fetch_child` 0/1、可选参数零值跳过）；POST 端点大 body 以 `map[string]any` 透传（字段结构见官方文档，库不做字段级建模）。异步导出的结果文件下载与解密由调用方处理（`encoding_aeskey` 由调用方生成传入）。回调事件：导出任务完成通知派发 `wecom.batch_job_result`（嵌套 BatchJob 字段展平进 Fields）；`update_tag` 派发 `wecom.update_tag`（独立于 change_contact 变更族）。端点清点基准：change wecom-contact-domain proposal（官方文档 39 页逐页取证）。

**OAuth 编排（OauthManager）**：`NewOauthManager(client, store, corpID, agentID, redirectURI)`（仅用 URL 构造方法时 client 可传 nil）。五方法：`BuildAuthorizeURL(ctx, returnURL)` / `BuildPrivateInfoAuthorizeURL(ctx, returnURL)`（内部建 state 后拼 URL）、`BuildFrontendRedirectURL(ctx, state, code) (string, bool, error)`（消费 state 拼 code；ok=false 表无效 state，err 仅表 Redis 故障）、`GetUserinfoByCode` / `GetUserDetailByTicket`（委托 Client）。

## 4. FAQ

**Q：通讯录同步应用与自建应用差在哪？**

Credential.AppType：AppContact（通讯录 secret、无 agentID、缓存键 contact 段）与 AppAgent（应用 secret+agentID）。两者的 access_token 独立缓存互不干扰。

**Q：HandleCallback 之后事件在哪消费？**

events 分发器（默认全局 Default()，可注入独立实例）——监听方 events.Subscribe 按事件名订阅，载荷是 `wecom.CallbackEvent`（值类型）。

**Q：Crypto 与 PHP 侧互通吗？**

互通且经双向跨验：testdata/wecom_php_fixture.json 是 PHP hyperf-lib Crypt（docker php:8 加载其六个 Crypt 类）生成的固定密文——单场景（固定 token/AESKey/corpId/明文），Go 解 PHP 密文一致、Go 加密输出 PHP 可解。存量 PHP 密文可无缝迁 Go。

**Q：消息体还有哪几种？**

Text / Markdown / Image / Voice / Video / File / TextCard / News / MpNews 九种（message.go 各带导出类型与 MarshalJSON）。

## 5. 维护者注记

**PHP 夹具双向跨验**：docker php:8 加载 hyperf-lib Crypt 六类生成固定密文落 testdata——跨语言兼容性不是「按文档实现」的期票，是密文级取证。

**errcode 判定全部经 errcodeOf**：float64 断言后转 int（微信 JSON 数字默认浮点），漏断言直接比较会 panic——集中一处防散落。

**测试**：包内单测（crypt 固定向量 + PHP 夹具双向、token 并发链路 miniredis、client 42001 恰一次重试 httptest、九消息体序列化快照、OAuth 全链——StateStore 生命周期、OauthManager 编排与域方法委托、双 scope URL 逐参数 diff）。

**改动规矩**：事件派发名、Credential 形态、消息 JSON 字段是对外契约，改动须走 change 流程并同步本文档。

## 凭证缓存回写日志（#137，v0.14.2）

凭证缓存 SET 失败时，本次仍返回已获取的 token/ticket，后续调用可能重新获取。组件只记录固定警告：`wecom token 缓存回写失败`、`wecom jsapi_ticket 缓存回写失败`；不附加原始 Redis 错误、SET 参数或凭证值。原因是 GoFrame Redis adapter 会在错误中携带完整命令参数。此约束只覆盖组件自身的缓存回写警告，不代替调用方的 Redis trace、外部日志采集与凭证保护配置。

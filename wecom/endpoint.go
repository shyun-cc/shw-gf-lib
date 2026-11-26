package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Endpoint 服务端 API 端点声明式表项：每个域方法 = 一行表项 + 一行薄委托（经 invoke 统一分发）。
// 新增/修正一个端点 = 改一行表项；表项是表驱动端点断言测试的遍历对照面。
type Endpoint struct {
	Name   string   // 端点名（表键 + 测试标识，如 "auth.getuserinfo"）
	Method string   // HTTP method
	Path   string   // API path
	Query  []string // 显式 query 参数名清单（不含自动注入的 access_token）
	Body   bool     // 是否携带 JSON body
}

// 端点表：全量端点在此登记（allEndpoints 为测试遍历对照面，新表项必须同步配测试用例）
var (
	epAuthGetUserinfo   = Endpoint{"auth.getuserinfo", http.MethodGet, "/cgi-bin/auth/getuserinfo", []string{"code"}, false}
	epAuthGetUserDetail = Endpoint{"auth.getuserdetail", http.MethodPost, "/cgi-bin/auth/getuserdetail", nil, true}
)

// allEndpoints 全量端点注册表：auth 2 + 通讯录 41（contact.go 28 + contact_admin.go 13）
var allEndpoints = []Endpoint{
	epAuthGetUserinfo, epAuthGetUserDetail,
	epUserCreate, epUserGet, epUserUpdate, epUserDelete, epUserBatchDelete,
	epUserSimpleList, epUserList, epUserToOpenid, epUserToUserid, epUserAuthSucc,
	epBatchInvite, epCorpJoinQrcode, epUserGetByMobile, epUserGetByEmail, epUserListID,
	epDeptCreate, epDeptUpdate, epDeptDelete, epDeptList, epDeptSimpleList, epDeptGet,
	epTagCreate, epTagUpdate, epTagDelete, epTagGet, epTagAddTagUsers, epTagDelTagUsers, epTagList,
	epRuleCreate, epRuleList, epRuleUpdate, epRuleDelete,
	epBatchSyncUser, epBatchReplaceUser, epBatchReplaceDept, epBatchGetResult,
	epExportSimpleUser, epExportUser, epExportDept, epExportTagUser, epExportGetResult,
	epMediaUpload, epMediaUploadImg, epMediaUploadByURL, epMediaUploadResult, epMediaGet, epMediaGetJssdk,
	epMsgSend, epMsgUpdateCard, epMsgRecall,
	epAppChatCreate, epAppChatUpdate, epAppChatGet, epAppChatSend, epSchoolNotice,
	epAgentGet, epAgentList, epAgentSet, epMenuCreate, epMenuGet, epMenuDelete,
	epWBSetTemplate, epWBGetTemplate, epWBSetData, epWBBatchSetData, epWBGetData,
	epECFollowUserList, epECList, epECGet, epECBatchGet, epECRemark,
	epECStrategyList, epECStrategyGet, epECStrategyRange, epECStrategyCreate, epECStrategyEdit, epECStrategyDel,
	epECCorpTagList, epECCorpTagAdd, epECCorpTagEdit, epECCorpTagDel,
	epECStrategyTagList, epECStrategyTagAdd, epECStrategyTagEdit, epECStrategyTagDel,
	epECMarkTag,
	epECOnjobTransferCustomer, epECOnjobTransferResult, epECOnjobTransferGroup,
	epECUnassignedList, epECResignedTransferCust, epECResignedTransferRes, epECResignedTransferGroup,
	epECGroupChatList, epECGroupChatGet, epECOpenGidToChat,
	epECContactWayAdd, epECContactWayGet, epECContactWayList, epECContactWayUpdate, epECContactWayDel, epECCloseTempChat,
	epECJoinWayAdd, epECJoinWayGet, epECJoinWayUpdate, epECJoinWayDel,
	epMomentAddTask, epMomentTaskResult, epMomentCancel, epMomentList, epMomentTask,
	epMomentCustomerList, epMomentSendResult, epMomentComments,
	epMomentStrategyList, epMomentStrategyGet, epMomentStrategyRange, epMomentStrategyNew, epMomentStrategyEdit, epMomentStrategyDel,
	epAcqListLink, epAcqGet, epAcqCreateLink, epAcqUpdateLink, epAcqDeleteLink,
	epAcqCustomer, epAcqQuota, epAcqStatistic, epAcqChatInfo,
	epMsgPushAdd, epMsgPushRemind, epMsgPushCancel, epMsgPushList, epMsgPushTask, epMsgPushResult,
	epWelcomeSend, epWelcomeTplAdd, epWelcomeTplEdit, epWelcomeTplGet, epWelcomeTplDel,
	epStatsBehavior, epStatsGroupChat, epStatsGroupChatD,
	epAlbumAdd, epAlbumGet, epAlbumList, epAlbumUpdate, epAlbumDelete,
	epInterceptAdd, epInterceptList, epInterceptGet, epInterceptEdit, epInterceptDel,
	epUploadAttach, epServedContacts,
	epKfUpgradeCfg, epKfUpgrade, epKfUpgradeCancel,
	epKfAccountAdd, epKfAccountDel, epKfAccountUpdate, epKfAccountList, epKfContactWay,
	epKfServicerAdd, epKfServicerDel, epKfServicerList,
	epKfStateGet, epKfStateTrans, epKfSendMsg, epKfSendMsgOnEvent, epKfCustomerGet,
	epKfCorpStat, epKfServicerStat,
	epKfGroupAdd, epKfGroupDel, epKfGroupMod, epKfGroupList,
	epKfIntentAdd, epKfIntentDel, epKfIntentMod, epKfIntentList,
	epCheckinCorpOption, epCheckinOption, epCheckinData, epCheckinDayData, epCheckinMonthData,
	epCheckinScheduGet, epCheckinScheduSet, epCheckinPunchFix, epCheckinAddRecord, epCheckinUserFace,
	epCheckinHardware, epCheckinOptionAdd, epCheckinOptionUpd, epCheckinOptionClear, epCheckinOptionDel,
	epApprovalTemplateGet, epApprovalApply, epApprovalInfo, epApprovalDetail, epApprovalOldData,
	epVacationCorpConf, epVacationQuotaGet, epVacationQuotaSet, epOpenApprovalData,
	epApprovalTplCreate, epApprovalTplUpdate,
	epCalendarAdd, epCalendarUpdate, epCalendarGet, epCalendarDel,
	epScheduleAdd, epScheduleUpdate, epScheduleAddAttd, epScheduleDelAttd,
	epScheduleListByCal, epScheduleGet, epScheduleDel,
	epMeetingCreate, epMeetingUpdate, epMeetingCancel, epMeetingUserID, epMeetingInfo,
	epMtgGetInvitees, epMtgSetInvitees, epMtgCreateShortURL, epMtgGetShortURL,
	epMtgRealtimeAtt, epMtgAttendeeList, epMtgWaitCurrent, epMtgWaitRecord, epMtgDeviceCheck,
	epMtgGetGuests, epMtgSetGuests, epMtgQuality,
	epMtgEnrollSetCfg, epMtgEnrollGetCfg, epMtgEnrollQueryID, epMtgEnrollList,
	epMtgEnrollApprove, epMtgEnrollImport, epMtgEnrollDelete,
	epRCSet, epRCSetCohost, epRCMuteUser, epRCVideo, epRCWaitRoom, epRCKickout, epRCDismiss,
	epRCCloseShare, epRCSetNickname,
	epPollCreateTheme, epPollUpdateTheme, epPollGetList, epPollGetThemeInfo,
	epPollGetDetail, epPollDelete, epPollStart, epPollFinish,
	epMtgLayoutListTpl, epMtgLayoutAdd, epMtgLayoutUpdate, epMtgLayoutSetDefault,
	epAdvLayoutAdd, epAdvLayoutUpdate, epAdvLayoutApply, epAdvLayoutList,
	epAdvLayoutUserGet, epAdvLayoutBatchDel,
	epMtgBgAdd, epMtgBgSetDefault, epMtgBgList, epMtgBgDelete, epMtgBgBatchDelete,
	epMtgRecList, epMtgRecStats, epMtgRecShareUpd, epMtgRecDelete,
	epMtgRecFileDelete, epMtgRecFileGet, epMtgRecFileList,
	epMtgTsParagraphs, epMtgTsDetail, epMtgTsSearch,
	epMtgVipAddJob, epMtgVipAddResult, epMtgVipDelJob, epMtgVipDelResult, epMtgVipList,
	epWbCreate, epWbUpdate, epWbCancel, epWbGet, epWbGuestList, epWbGuestUpdate, epWbWarmUp,
	epWbEnrollSet, epWbEnrollGet, epWbEnrollQuery, epWbEnrollList, epWbEnrollAppr, epWbEnrollImp, epWbEnrollDel,
	epPhoneCallout, epPhoneStatus, epPhoneTmpOID,
	epRoomsBook, epRoomsRelease, epRoomsList, epRoomsGetInfo, epRoomsGetConfig, epRoomsListMtgs,
	epRoomsListDev, epRoomsListCtrl, epRoomsInventory, epRoomsCall, epRoomsCancelCall, epRoomsRespStatus,
	epMRAStatus, epMRALayout, epMRARaiseHand, epMRAHangup,
	epEmailActAccount, epEmailNewCount, epMailSend, epMailSendSched, epMailSendMeeting,
	epMailList, epMailRead, epEmailAliasUpd, epEmailAliasGet,
	epMailGroupCreate, epMailGroupUpdate, epMailGroupDelete, epMailGroupGet, epMailGroupSearch,
	epPubMailCreate, epPubMailUpdate, epPubMailDelete, epPubMailGet, epPubMailSearch,
	epPubMailAuthList, epPubMailAuthDel,
	epVipAdd, epVipDel, epVipList, epUserOptGet, epUserOptUpdate,
	epMiscAPIDomainIP, epMiscCallbackIP, epMiscServerDomain, epMiscConvertTmpID,
	epMiscMemberLog, epMiscAdminLog,
	epSecScreen, epSecFile,
	epSecDevImport, epSecDevList, epSecDevByUser, epSecDevDelete, epSecDevApprove, epSecDevReject,
	epSecVipAddJob, epSecVipAddRes, epSecVipDelJob, epSecVipDelRes, epSecVipList,
}

// checkQuery query 键集按表项 Query 清单校验（未声明参数 fail fast——编码 bug 不伪装成业务异常）
func checkQuery(ep Endpoint, query map[string]string) error {
	for k := range query {
		declared := false
		for _, q := range ep.Query {
			if k == q {
				declared = true
				break
			}
		}
		if !declared {
			return fmt.Errorf("wecom: 端点 %s 未声明 query 参数 %s（补表项 Query 清单或改传已声明参数）", ep.Name, k)
		}
	}
	return nil
}

// invoke 统一分发：query 键集按表项校验、token 自动注入（含 42001/40014 恰一次刷新重试）、
// errcode ≠ 0 → BizError、HTTP/解码失败 → InfraError。表项字段变更不需要改动本实现。
func (c *Client) invoke(ctx context.Context, ep Endpoint, query map[string]string, body any) (map[string]any, error) {
	if err := checkQuery(ep, query); err != nil {
		return nil, err
	}
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, &InfraError{Op: "encode", Err: fmt.Errorf("请求体编码失败: %w", err)}
		}
		raw = b
	}
	return c.do(ctx, ep.Method, ep.Path, query, raw)
}

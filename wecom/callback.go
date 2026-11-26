package wecom

import (
	"context"
	"encoding/xml"
	"strings"

	"github.com/shyun-cc/shw-gf-lib/events"
)

// 回调事件名（十种通讯录变更 + 通用消息 + 导出任务完成；订阅方按名监听）
const (
	EventSubscribe             = "wecom.subscribe"
	EventUnsubscribe           = "wecom.unsubscribe"
	EventChangeContact         = "wecom.change_contact"
	EventChangeUser            = "wecom.change_user"
	EventCreateUser            = "wecom.create_user"
	EventDeleteUser            = "wecom.delete_user"
	EventCreateParty           = "wecom.create_party"
	EventUpdateParty           = "wecom.update_party"
	EventDeleteParty           = "wecom.delete_party"
	EventUpdateTag             = "wecom.update_tag"
	EventBatchJobResult        = "wecom.batch_job_result"
	EventUploadMediaJobFinish  = "wecom.upload_media_job_finish"
	EventSwitchWorkbenchMode   = "wecom.switch_workbench_mode"
	EventChangeExternalContact = "wecom.change_external_contact"
	EventChangeExternalTag     = "wecom.change_external_tag"
	EventChangeExternalChat    = "wecom.change_external_chat"
	EventCustomerAcquisition   = "wecom.customer_acquisition"
	EventKfMsgOrEvent          = "wecom.kf_msg_or_event"
	EventSysApprovalChange     = "wecom.sys_approval_change"
	EventOpenApprovalChange    = "wecom.open_approval_change"
	EventModifyCalendar        = "wecom.modify_calendar"
	EventDeleteCalendar        = "wecom.delete_calendar"
	EventModifySchedule        = "wecom.modify_schedule"
	EventDeleteSchedule        = "wecom.delete_schedule"
	EventRespondSchedule       = "wecom.respond_schedule"
	EventMeetingChange         = "wecom.meeting_change" // 会议修改/取消（ChangeType: modify_meeting/cancel_meeting，官方 97451）
)

// CallbackEvent 派发给订阅方的事件载荷（原始字段全量透传 + 身份字段）
type CallbackEvent struct {
	MsgType    string
	Event      string
	ChangeType string
	ToUserName string
	CreateTime string
	CorpID     string            // 身份：取自 crypt receiveid（企微侧即 corpID）
	AgentID    string            // 身份：取自明文 AgentID 字段（应用回调在位，通讯录回调为空）
	Fields     map[string]string // 明文 XML 全部叶子字段
}

// HandleCallback 处理回调：验签 → 解密 → 提取事件 → events 派发。
// 返回派发的事件名（Event 字段），供上层记录。
func (c *WXBizMsgCrypt) HandleCallback(ctx context.Context, dispatcher *events.Dispatcher, msgSignature, timestamp, nonce, encryptedXML string) (*CallbackEvent, error) {
	plain, err := c.DecryptEvent(msgSignature, timestamp, nonce, encryptedXML)
	if err != nil {
		return nil, err
	}

	fields, err := parseXMLFields(plain)
	if err != nil {
		return nil, err
	}
	ev := &CallbackEvent{
		MsgType:    fields["MsgType"],
		Event:      fields["Event"],
		ChangeType: fields["ChangeType"],
		ToUserName: fields["ToUserName"],
		CreateTime: fields["CreateTime"],
		CorpID:     c.corpID,
		AgentID:    fields["AgentID"],
		Fields:     fields,
	}

	name := eventDispatchName(ev)
	if dispatcher != nil {
		dispatcher.Dispatch(ctx, name, *ev)
	}
	return ev, nil
}

func eventDispatchName(ev *CallbackEvent) string {
	switch ev.Event {
	case "subscribe":
		return EventSubscribe
	case "unsubscribe":
		return EventUnsubscribe
	case "change_contact":
		return EventChangeContact
	case "update_tag":
		return EventUpdateTag // 标签变更语义独立于通讯录变更族
	case "batch_job_result":
		return EventBatchJobResult // 导出任务完成通知
	case "upload_media_job_finish":
		return EventUploadMediaJobFinish // 异步上传素材完成（仅通知不含结果，需调查询接口取 media_id）
	case "customer_acquisition":
		return EventCustomerAcquisition // 获客助手事件族（11 ChangeType：balance/link/friend_request/message_from_customer 等）
	case "modify_calendar":
		return EventModifyCalendar
	case "delete_calendar":
		return EventDeleteCalendar
	case "modify_schedule":
		return EventModifySchedule
	case "delete_schedule":
		return EventDeleteSchedule
	case "respond_schedule":
		return EventRespondSchedule // 日程回执（结果需调查询接口）
	case "sys_approval_change":
		return EventSysApprovalChange // 审批单状态变化（SpNoStr/SpStatus/StatuChangeEvent）
	case "open_approval_change":
		return EventOpenApprovalChange // 自建应用审批单状态
	case "kf_msg_or_event":
		return EventKfMsgOrEvent // 微信客服消息事件
	case "meeting_change":
		return EventMeetingChange // 会议修改/取消（ChangeType 细分；显式 case 防误落 change_ 前缀族）
	case "switch_workbench_mode":
		return EventSwitchWorkbenchMode // 工作台自定义模式开关切换
	case "change_external_contact":
		return EventChangeExternalContact // 客户变更族（ChangeType: add/edit/add_half/del_external_contact/del_follow_user/transfer_fail）
	case "change_external_chat":
		return EventChangeExternalChat // 客户群变更族（ChangeType: create/update/dismiss）
	case "change_external_tag":
		return EventChangeExternalTag // 客户标签变更族（ChangeType: create/update/delete/shuffle）
	default:
		if strings.HasPrefix(ev.Event, "change_") || strings.HasPrefix(ev.Event, "create_") ||
			strings.HasPrefix(ev.Event, "delete_") || strings.HasPrefix(ev.Event, "update_") {
			return EventChangeContact // 变更族统一走 change_contact（订阅方可按 Fields.ChangeType 细分）
		}
		return "wecom.msg." + ev.MsgType
	}
}

// parseXMLFields 解析明文事件 XML 的叶子字段为 map（simplexml 同构语义）
func parseXMLFields(plainXML string) (map[string]string, error) {
	dec := xml.NewDecoder(strings.NewReader(plainXML))
	fields := map[string]string{}
	var key string
	var depth int
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth >= 2 {
				key = t.Name.Local // 2/3 层均收集：嵌套节点（如 BatchJob.JobId）以叶子键名展平
			}
		case xml.CharData:
			if depth >= 2 && key != "" {
				fields[key] = string(t)
			}
		case xml.EndElement:
			if depth >= 2 {
				key = ""
			}
			depth--
		}
	}
	if len(fields) == 0 {
		return nil, ErrIllegalBuffer
	}
	return fields, nil
}

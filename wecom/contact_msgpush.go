package wecom

import (
	"context"
	"net/http"
)

// 客户联系子域⑤表项：企业群发 6 + 新客户欢迎语 1 + 入群欢迎语素材 4 + 统计 3
// （官方清点见 change wecom-contact-msgpush-stats proposal）
var (
	epMsgPushAdd      = Endpoint{"ec.msgpush.add", http.MethodPost, "/cgi-bin/externalcontact/add_msg_template", nil, true}
	epMsgPushRemind   = Endpoint{"ec.msgpush.remind", http.MethodPost, "/cgi-bin/externalcontact/remind_groupmsg_send", nil, true}
	epMsgPushCancel   = Endpoint{"ec.msgpush.cancel", http.MethodPost, "/cgi-bin/externalcontact/cancel_groupmsg_send", nil, true}
	epMsgPushList     = Endpoint{"ec.msgpush.list", http.MethodPost, "/cgi-bin/externalcontact/get_groupmsg_list_v2", nil, true}
	epMsgPushTask     = Endpoint{"ec.msgpush.task", http.MethodPost, "/cgi-bin/externalcontact/get_groupmsg_task", nil, true}
	epMsgPushResult   = Endpoint{"ec.msgpush.send_result", http.MethodPost, "/cgi-bin/externalcontact/get_groupmsg_send_result", nil, true}
	epWelcomeSend     = Endpoint{"ec.welcome.send", http.MethodPost, "/cgi-bin/externalcontact/send_welcome_msg", nil, true}
	epWelcomeTplAdd   = Endpoint{"ec.welcome.template_add", http.MethodPost, "/cgi-bin/externalcontact/group_welcome_template/add", nil, true}
	epWelcomeTplEdit  = Endpoint{"ec.welcome.template_edit", http.MethodPost, "/cgi-bin/externalcontact/group_welcome_template/edit", nil, true}
	epWelcomeTplGet   = Endpoint{"ec.welcome.template_get", http.MethodPost, "/cgi-bin/externalcontact/group_welcome_template/get", nil, true}
	epWelcomeTplDel   = Endpoint{"ec.welcome.template_del", http.MethodPost, "/cgi-bin/externalcontact/group_welcome_template/del", nil, true}
	epStatsBehavior   = Endpoint{"ec.stats.user_behavior", http.MethodPost, "/cgi-bin/externalcontact/get_user_behavior_data", nil, true}
	epStatsGroupChat  = Endpoint{"ec.stats.groupchat", http.MethodPost, "/cgi-bin/externalcontact/groupchat/statistic", nil, true}
	epStatsGroupChatD = Endpoint{"ec.stats.groupchat_by_day", http.MethodPost, "/cgi-bin/externalcontact/groupchat/statistic_group_by_day", nil, true}
)

// AddMsgTemplate 创建企业群发（body：chat_type/external_userid/tag_filter/sender/text/attachments 等）
func (c *Client) AddMsgTemplate(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMsgPushAdd, nil, body)
}

// RemindGroupmsgSend 提醒成员群发
func (c *Client) RemindGroupmsgSend(ctx context.Context, msgid string) (map[string]any, error) {
	return c.invoke(ctx, epMsgPushRemind, nil, map[string]any{"msgid": msgid})
}

// CancelGroupmsgSend 停止企业群发
func (c *Client) CancelGroupmsgSend(ctx context.Context, msgid string) (map[string]any, error) {
	return c.invoke(ctx, epMsgPushCancel, nil, map[string]any{"msgid": msgid})
}

// GetGroupmsgList 获取群发记录列表（body：chat_type/start_time/end_time/creator/filter_type/limit/cursor）
func (c *Client) GetGroupmsgList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMsgPushList, nil, body)
}

// GetGroupmsgTask 获取群发成员发送任务列表（body：msgid/limit/cursor）
func (c *Client) GetGroupmsgTask(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMsgPushTask, nil, body)
}

// GetGroupmsgSendResult 获取群发成员执行结果（body：msgid/userid/limit/cursor）
func (c *Client) GetGroupmsgSendResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMsgPushResult, nil, body)
}

// SendWelcomeMsg 发送新客户欢迎语（body：welcome_code/text/attachments；welcome_code 来自 add_external_contact 事件）
func (c *Client) SendWelcomeMsg(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWelcomeSend, nil, body)
}

// AddGroupWelcomeTemplate 添加入群欢迎语素材（body：text/image/link/miniprogram/file/video/agentid/notify）
func (c *Client) AddGroupWelcomeTemplate(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWelcomeTplAdd, nil, body)
}

// EditGroupWelcomeTemplate 编辑入群欢迎语素材
func (c *Client) EditGroupWelcomeTemplate(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWelcomeTplEdit, nil, body)
}

// GetGroupWelcomeTemplate 获取入群欢迎语素材
func (c *Client) GetGroupWelcomeTemplate(ctx context.Context, templateID string) (map[string]any, error) {
	return c.invoke(ctx, epWelcomeTplGet, nil, map[string]any{"template_id": templateID})
}

// DelGroupWelcomeTemplate 删除入群欢迎语素材
func (c *Client) DelGroupWelcomeTemplate(ctx context.Context, templateID string, agentid int) (map[string]any, error) {
	return c.invoke(ctx, epWelcomeTplDel, nil, map[string]any{"template_id": templateID, "agentid": agentid})
}

// GetUserBehaviorData 获取联系客户统计数据（body：userid/partyid/start_time/end_time）
func (c *Client) GetUserBehaviorData(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epStatsBehavior, nil, body)
}

// GetGroupChatStatistic 群聊数据统计——按群主聚合（body：day_begin_time/day_end_time/owner_filter/order_by/…）
func (c *Client) GetGroupChatStatistic(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epStatsGroupChat, nil, body)
}

// GetGroupChatStatisticByDay 群聊数据统计——按自然日聚合（body：day_begin_time/day_end_time/owner_filter）
func (c *Client) GetGroupChatStatisticByDay(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epStatsGroupChatD, nil, body)
}

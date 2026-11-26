package wecom

import (
	"context"
	"net/http"
)

// 会议域子域②表项（预约会议高级管理）：受邀成员 2 + 专属链接 2 + 会中/已参会 2 + 等候室 2 +
// 设备 1 + 嘉宾 2 + 健康度 1 + 报名族 7（官方清点见 change wecom-meeting-advanced-domain proposal）
var (
	epMtgGetInvitees    = Endpoint{"meeting.get_invitees", http.MethodPost, "/cgi-bin/meeting/get_invitees", nil, true}
	epMtgSetInvitees    = Endpoint{"meeting.set_invitees", http.MethodPost, "/cgi-bin/meeting/set_invitees", nil, true}
	epMtgCreateShortURL = Endpoint{"meeting.create_customer_short_url", http.MethodPost, "/cgi-bin/meeting/create_customer_short_url", nil, true}
	epMtgGetShortURL    = Endpoint{"meeting.get_customer_short_url", http.MethodPost, "/cgi-bin/meeting/get_customer_short_url", nil, true}
	epMtgRealtimeAtt    = Endpoint{"meeting.realtime_attendee", http.MethodPost, "/cgi-bin/meeting/get_realtime_attendee_list", nil, true}
	epMtgAttendeeList   = Endpoint{"meeting.attendee_list", http.MethodPost, "/cgi-bin/meeting/get_attendee_list", nil, true}
	epMtgWaitCurrent    = Endpoint{"meeting.waitingroom_current", http.MethodPost, "/cgi-bin/meeting/waitingroom/get_current_user_list", nil, true}
	epMtgWaitRecord     = Endpoint{"meeting.waitingroom_record", http.MethodPost, "/cgi-bin/meeting/waitingroom/get_user_list", nil, true}
	epMtgDeviceCheck    = Endpoint{"meeting.check_device", http.MethodPost, "/cgi-bin/meeting/check_device_in_meeting", nil, true}
	epMtgGetGuests      = Endpoint{"meeting.get_guests", http.MethodPost, "/cgi-bin/meeting/get_guests", nil, true}
	epMtgSetGuests      = Endpoint{"meeting.set_guests", http.MethodPost, "/cgi-bin/meeting/set_guests", nil, true}
	epMtgQuality        = Endpoint{"meeting.quality", http.MethodPost, "/cgi-bin/meeting/get_quality", nil, true}
	epMtgEnrollSetCfg   = Endpoint{"meeting.enroll_set_config", http.MethodPost, "/cgi-bin/meeting/enroll/set_config", nil, true}
	epMtgEnrollGetCfg   = Endpoint{"meeting.enroll_get_config", http.MethodPost, "/cgi-bin/meeting/enroll/get_config", nil, true}
	epMtgEnrollQueryID  = Endpoint{"meeting.enroll_query_by_tmp_openid", http.MethodPost, "/cgi-bin/meeting/enroll/query_by_tmp_openid", nil, true}
	epMtgEnrollList     = Endpoint{"meeting.enroll_list", http.MethodPost, "/cgi-bin/meeting/enroll/list", nil, true}
	epMtgEnrollApprove  = Endpoint{"meeting.enroll_approve", http.MethodPost, "/cgi-bin/meeting/enroll/approve", nil, true}
	epMtgEnrollImport   = Endpoint{"meeting.enroll_import", http.MethodPost, "/cgi-bin/meeting/enroll/import", nil, true}
	epMtgEnrollDelete   = Endpoint{"meeting.enroll_delete", http.MethodPost, "/cgi-bin/meeting/enroll/delete", nil, true}
)

// GetMeetingInvitees 获取会议受邀成员列表（cursor 分页；创建时最多 300 人，超出走 SetMeetingInvitees）
func (c *Client) GetMeetingInvitees(ctx context.Context, meetingID string) (map[string]any, error) {
	return c.invoke(ctx, epMtgGetInvitees, nil, map[string]any{"meetingid": meetingID})
}

// SetMeetingInvitees 更新会议受邀成员列表（最多 2000 人；管理员必须在列表中）
func (c *Client) SetMeetingInvitees(ctx context.Context, meetingID string, invitees []map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgSetInvitees, nil, map[string]any{"meetingid": meetingID, "invitees": invitees})
}

// CreateCustomerShortURL 创建用户专属参会链接（不支持网络研讨会）
func (c *Client) CreateCustomerShortURL(ctx context.Context, meetingID string, customerData map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgCreateShortURL, nil, map[string]any{"meetingid": meetingID, "customer_data": customerData})
}

// GetCustomerShortURL 获取用户专属参会链接
func (c *Client) GetCustomerShortURL(ctx context.Context, meetingID string) (map[string]any, error) {
	return c.invoke(ctx, epMtgGetShortURL, nil, map[string]any{"meetingid": meetingID})
}

// GetRealtimeAttendeeList 获取实时会中成员列表（body：meetingid/sub_meetingid/cursor/limit）
func (c *Client) GetRealtimeAttendeeList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgRealtimeAtt, nil, body)
}

// GetAttendeeList 获取已参会成员列表（body：meetingid/sub_meetingid/start_time/end_time/cursor/limit）
func (c *Client) GetAttendeeList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgAttendeeList, nil, body)
}

// GetWaitingroomCurrentUserList 获取实时等候室成员列表（body：meetingid/limit/cursor）
func (c *Client) GetWaitingroomCurrentUserList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgWaitCurrent, nil, body)
}

// GetWaitingroomUserList 获取等候室成员记录（body：meetingid/limit/cursor）
func (c *Client) GetWaitingroomUserList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgWaitRecord, nil, body)
}

// CheckDeviceInMeeting 获取成员设备是否入会（body：userid/instance_id_list/meetingid_list）
func (c *Client) CheckDeviceInMeeting(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgDeviceCheck, nil, body)
}

// GetMeetingGuests 获取会议嘉宾列表
func (c *Client) GetMeetingGuests(ctx context.Context, meetingID string) (map[string]any, error) {
	return c.invoke(ctx, epMtgGetGuests, nil, map[string]any{"meetingid": meetingID})
}

// SetMeetingGuests 更新会议嘉宾列表（body：meetingid/guests）
func (c *Client) SetMeetingGuests(ctx context.Context, meetingID string, guests []map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgSetGuests, nil, map[string]any{"meetingid": meetingID, "guests": guests})
}

// GetMeetingQuality 获取会议健康度（body：meetingid/sub_meetingid/start_time/cursor/limit）
func (c *Client) GetMeetingQuality(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgQuality, nil, body)
}

// SetMeetingEnrollConfig 修改会议报名配置（body：meetingid/approve_type/question_list 等）
func (c *Client) SetMeetingEnrollConfig(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgEnrollSetCfg, nil, body)
}

// GetMeetingEnrollConfig 获取会议报名配置
func (c *Client) GetMeetingEnrollConfig(ctx context.Context, meetingID string) (map[string]any, error) {
	return c.invoke(ctx, epMtgEnrollGetCfg, nil, map[string]any{"meetingid": meetingID})
}

// QueryMeetingEnrollByTmpOpenid 获取会议成员报名 ID（body：meetingid/sorting_rules/tmp_openid_list）
func (c *Client) QueryMeetingEnrollByTmpOpenid(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgEnrollQueryID, nil, body)
}

// ListMeetingEnroll 获取会议报名信息（body：meetingid/status/cursor/limit）
func (c *Client) ListMeetingEnroll(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgEnrollList, nil, body)
}

// ApproveMeetingEnroll 审批会议报名信息（body：meetingid/action/enroll_id_list）
func (c *Client) ApproveMeetingEnroll(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgEnrollApprove, nil, body)
}

// ImportMeetingEnroll 导入会议报名信息（body：meetingid/enroll_list）
func (c *Client) ImportMeetingEnroll(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgEnrollImport, nil, body)
}

// DeleteMeetingEnroll 删除会议报名信息（body：meetingid/enroll_id_list）
func (c *Client) DeleteMeetingEnroll(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgEnrollDelete, nil, body)
}

package wecom

import (
	"context"
	"net/http"
)

// 会议域子域①表项（基础管理）：预约会议 CRUD + 成员会议 ID 列表
// （官方清点见 change wecom-meeting-basic-domain proposal；高级/会中控制/Webinar 等归后续子域）
var (
	epMeetingCreate = Endpoint{"meeting.create", http.MethodPost, "/cgi-bin/meeting/create", nil, true}
	epMeetingUpdate = Endpoint{"meeting.update", http.MethodPost, "/cgi-bin/meeting/update", nil, true}
	epMeetingCancel = Endpoint{"meeting.cancel", http.MethodPost, "/cgi-bin/meeting/cancel", nil, true}
	epMeetingUserID = Endpoint{"meeting.get_user_meetingid", http.MethodPost, "/cgi-bin/meeting/get_user_meetingid", nil, true}
	epMeetingInfo   = Endpoint{"meeting.get_info", http.MethodPost, "/cgi-bin/meeting/get_info", nil, true}
)

// CreateMeeting 创建预约会议（body：admin_userid/title/meeting_start/meeting_duration/invitees/settings/…）
func (c *Client) CreateMeeting(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMeetingCreate, nil, body)
}

// UpdateMeeting 修改预约会议（body：meetingid + 可更新字段）
func (c *Client) UpdateMeeting(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMeetingUpdate, nil, body)
}

// CancelMeeting 取消预约会议
func (c *Client) CancelMeeting(ctx context.Context, meetingID string) (map[string]any, error) {
	return c.invoke(ctx, epMeetingCancel, nil, map[string]any{"meetingid": meetingID})
}

// GetUserMeetingIDList 获取成员会议 ID 列表（body：userid/cursor/begin_time/end_time/limit）
func (c *Client) GetUserMeetingIDList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMeetingUserID, nil, body)
}

// GetMeetingInfo 获取会议详情
func (c *Client) GetMeetingInfo(ctx context.Context, meetingID string) (map[string]any, error) {
	return c.invoke(ctx, epMeetingInfo, nil, map[string]any{"meetingid": meetingID})
}

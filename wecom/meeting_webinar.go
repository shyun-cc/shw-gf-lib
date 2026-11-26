package wecom

import (
	"context"
	"net/http"
)

// 会议域子域④表项·网络研讨会 Webinar（14 端点，官方清点见 change
// wecom-meeting-webinar-pstn-rooms-mra proposal）
var (
	epWbCreate      = Endpoint{"meeting.webinar.create", http.MethodPost, "/cgi-bin/meeting/webinar/create", nil, true}
	epWbUpdate      = Endpoint{"meeting.webinar.update", http.MethodPost, "/cgi-bin/meeting/webinar/update", nil, true}
	epWbCancel      = Endpoint{"meeting.webinar.cancel", http.MethodPost, "/cgi-bin/meeting/webinar/cancel", nil, true}
	epWbGet         = Endpoint{"meeting.webinar.get", http.MethodPost, "/cgi-bin/meeting/webinar/get", nil, true}
	epWbGuestList   = Endpoint{"meeting.webinar.list_guest", http.MethodPost, "/cgi-bin/meeting/webinar/list_guest", nil, true}
	epWbGuestUpdate = Endpoint{"meeting.webinar.update_guest_list", http.MethodPost, "/cgi-bin/meeting/webinar/update_guest_list", nil, true}
	epWbWarmUp      = Endpoint{"meeting.webinar.update_warm_up", http.MethodPost, "/cgi-bin/meeting/webinar/update_warm_up", nil, true}
	epWbEnrollSet   = Endpoint{"meeting.webinar.enroll.set_config", http.MethodPost, "/cgi-bin/meeting/webinar/enroll/set_config", nil, true}
	epWbEnrollGet   = Endpoint{"meeting.webinar.enroll.get_config", http.MethodPost, "/cgi-bin/meeting/webinar/enroll/get_config", nil, true}
	epWbEnrollQuery = Endpoint{"meeting.webinar.enroll.query_by_tmp_openid", http.MethodPost, "/cgi-bin/meeting/webinar/enroll/query_by_tmp_openid", nil, true}
	epWbEnrollList  = Endpoint{"meeting.webinar.enroll.list", http.MethodPost, "/cgi-bin/meeting/webinar/enroll/list", nil, true}
	epWbEnrollAppr  = Endpoint{"meeting.webinar.enroll.approve", http.MethodPost, "/cgi-bin/meeting/webinar/enroll/approve", nil, true}
	epWbEnrollImp   = Endpoint{"meeting.webinar.enroll.import", http.MethodPost, "/cgi-bin/meeting/webinar/enroll/import", nil, true}
	epWbEnrollDel   = Endpoint{"meeting.webinar.enroll.delete", http.MethodPost, "/cgi-bin/meeting/webinar/enroll/delete", nil, true}
)

// CreateWebinar 创建网络研讨会（admin_userid/title 1-255/start_time ≥当前+30min/end_time/
// admission_type 0 公开 1 报名 2 密码/playback_for_audience（开回放须开云录制）必填；
// 返回 meetingid、meeting_code 与观众/嘉宾入会链接）。
func (c *Client) CreateWebinar(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbCreate, nil, body)
}

// UpdateWebinar 修改网络研讨会（meetingid + 同创建必填族）。
func (c *Client) UpdateWebinar(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbUpdate, nil, body)
}

// CancelWebinar 取消网络研讨会。
func (c *Client) CancelWebinar(ctx context.Context, meetingid string) (map[string]any, error) {
	return c.invoke(ctx, epWbCancel, nil, map[string]any{"meetingid": meetingid})
}

// GetWebinar 获取网络研讨会详情（meetingid 与 meeting_code 二选一，同传以 meetingid 为准）。
func (c *Client) GetWebinar(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbGet, nil, body)
}

// ListWebinarGuests 获取嘉宾列表（guest_type 1 内部/2 外部/3 链接嘉宾）。
func (c *Client) ListWebinarGuests(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbGuestList, nil, body)
}

// UpdateWebinarGuestList 更新嘉宾列表（内部嘉宾 userid；外部嘉宾 area+phone_number+guest_name）。
func (c *Client) UpdateWebinarGuestList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbGuestUpdate, nil, body)
}

// UpdateWebinarWarmUp 管理暖场配置（图片 png/jpg ≤5M 与视频 mp4 ≤1G 二选一，同传以图片为准；
// 仅会前 15 分钟进入的观众可见）。
func (c *Client) UpdateWebinarWarmUp(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbWarmUp, nil, body)
}

// SetWebinarEnrollConfig 修改报名配置（approve_type 1 自动/2 手动；question_list 选项 ≤8 项；
// special_type 2 手机/3 邮箱/4 姓名/5 公司/6 规模）。
func (c *Client) SetWebinarEnrollConfig(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbEnrollSet, nil, body)
}

// GetWebinarEnrollConfig 获取报名配置（未开启报名时报错）。
func (c *Client) GetWebinarEnrollConfig(ctx context.Context, meetingid string) (map[string]any, error) {
	return c.invoke(ctx, epWbEnrollGet, nil, map[string]any{"meetingid": meetingid})
}

// QueryWebinarEnrollIDByTmpOpenid 获取成员报名 ID（tmp_openid_list ≤500；
// sorting_rules 1 手机导入优先/2 手动报名优先）。
func (c *Client) QueryWebinarEnrollIDByTmpOpenid(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbEnrollQuery, nil, body)
}

// ListWebinarEnrolls 获取报名信息（status 0 全部/1 待审批/2 已拒绝/3 已批准；limit ≤50）。
func (c *Client) ListWebinarEnrolls(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbEnrollList, nil, body)
}

// ApproveWebinarEnrolls 审批报名信息（action 1 取消批准/2 拒绝/3 批准；返回 handled_count）。
func (c *Client) ApproveWebinarEnrolls(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbEnrollAppr, nil, body)
}

// ImportWebinarEnrolls 导入报名信息（userid 或 area+phone_number；返回 enroll_id 与
// enroll_code 电话入会凭证）。
func (c *Client) ImportWebinarEnrolls(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbEnrollImp, nil, body)
}

// DeleteWebinarEnrolls 删除报名信息（手动报名与导入的均可删；返回 total_count）。
func (c *Client) DeleteWebinarEnrolls(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWbEnrollDel, nil, body)
}

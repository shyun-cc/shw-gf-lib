package wecom

import (
	"context"
	"net/http"
)

// 会议域子域④表项·PSTN 电话入会 3 + Rooms 会议室 12 + 会议室连接器 MRA 4
// （官方清点见 change wecom-meeting-webinar-pstn-rooms-mra proposal）
var (
	epPhoneCallout = Endpoint{"meeting.phone.callout", http.MethodPost, "/cgi-bin/meeting/phone/callout", nil, true}
	epPhoneStatus  = Endpoint{"meeting.phone.get_callout_status", http.MethodPost, "/cgi-bin/meeting/phone/get_callout_status", nil, true}
	epPhoneTmpOID  = Endpoint{"meeting.phone.get_tmp_openid", http.MethodPost, "/cgi-bin/meeting/phone/get_tmp_openid", nil, true}

	epRoomsBook       = Endpoint{"meeting.rooms.book", http.MethodPost, "/cgi-bin/meeting/rooms/book", nil, true}
	epRoomsRelease    = Endpoint{"meeting.rooms.release", http.MethodPost, "/cgi-bin/meeting/rooms/release", nil, true}
	epRoomsList       = Endpoint{"meeting.rooms.list", http.MethodPost, "/cgi-bin/meeting/rooms/list", nil, true}
	epRoomsGetInfo    = Endpoint{"meeting.rooms.get_info", http.MethodPost, "/cgi-bin/meeting/rooms/get_info", nil, true}
	epRoomsGetConfig  = Endpoint{"meeting.rooms.get_config", http.MethodPost, "/cgi-bin/meeting/rooms/get_config", nil, true}
	epRoomsListMtgs   = Endpoint{"meeting.rooms.list_meetings", http.MethodPost, "/cgi-bin/meeting/rooms/list_meetings", nil, true}
	epRoomsListDev    = Endpoint{"meeting.rooms.list_devices", http.MethodPost, "/cgi-bin/meeting/rooms/list_devices", nil, true}
	epRoomsListCtrl   = Endpoint{"meeting.rooms.list_controllers", http.MethodPost, "/cgi-bin/meeting/rooms/list_controllers", nil, true}
	epRoomsInventory  = Endpoint{"meeting.rooms.get_inventory", http.MethodPost, "/cgi-bin/meeting/rooms/get_inventory", nil, false}
	epRoomsCall       = Endpoint{"meeting.rooms.call", http.MethodPost, "/cgi-bin/meeting/rooms/call", nil, true}
	epRoomsCancelCall = Endpoint{"meeting.rooms.cancel_call", http.MethodPost, "/cgi-bin/meeting/rooms/cancel_call", nil, true}
	epRoomsRespStatus = Endpoint{"meeting.rooms.get_response_status", http.MethodPost, "/cgi-bin/meeting/rooms/get_response_status", nil, true}

	epMRAStatus    = Endpoint{"meeting.mra.query_status", http.MethodPost, "/cgi-bin/meeting/mra/query_status", nil, true}
	epMRALayout    = Endpoint{"meeting.mra.set_default_layout", http.MethodPost, "/cgi-bin/meeting/mra/set_default_layout", nil, true}
	epMRARaiseHand = Endpoint{"meeting.mra.set_raise_hand", http.MethodPost, "/cgi-bin/meeting/mra/set_raise_hand", nil, true}
	epMRAHangup    = Endpoint{"meeting.mra.hangup", http.MethodPost, "/cgi-bin/meeting/mra/hangup", nil, true}
)

// PhoneCallout 批量外呼（≤50 路/次；area+phone(+extension_number)；Webinar 暂不支持外呼）。
func (c *Client) PhoneCallout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPhoneCallout, nil, body)
}

// GetPhoneCalloutStatus 获取会议外呼状态（cursor/limit ≤100；不按呼叫人隔离数据）。
func (c *Client) GetPhoneCalloutStatus(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPhoneStatus, nil, body)
}

// GetPhoneTmpOpenid 获取电话入会成员 ID（phone_numbers ≤20；仅接通后返回 tmp_openid；
// 同一座机可对应多个 tmp_openid）。
func (c *Client) GetPhoneTmpOpenid(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPhoneTmpOID, nil, body)
}

// BookMeetingRooms 预定 Rooms 会议室（会议 ≤24 小时、不支持周期性会议；可同时预定多个）。
func (c *Client) BookMeetingRooms(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsBook, nil, body)
}

// ReleaseMeetingRooms 释放 Rooms 会议室（批量）。
func (c *Client) ReleaseMeetingRooms(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsRelease, nil, body)
}

// ListMeetingRooms 获取 Rooms 会议室列表（名称模糊匹配；limit 1-50，翻页 limit 须一致）。
func (c *Client) ListMeetingRooms(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsList, nil, body)
}

// GetMeetingRoomInfo 获取 Rooms 会议室详情（basic/account/hardware/pmi 四组信息）。
func (c *Client) GetMeetingRoomInfo(ctx context.Context, meetingRoomID string) (map[string]any, error) {
	return c.invoke(ctx, epRoomsGetInfo, nil, map[string]any{"meeting_room_id": meetingRoomID})
}

// GetMeetingRoomConfig 获取 Rooms 会议室配置项（会议配置 + 录制配置两组）。
func (c *Client) GetMeetingRoomConfig(ctx context.Context, meetingRoomID string) (map[string]any, error) {
	return c.invoke(ctx, epRoomsGetConfig, nil, map[string]any{"meeting_room_id": meetingRoomID})
}

// ListMeetingRoomMeetings 获取 Rooms 会议室下的会议列表（meeting_room_id 与 rooms_id 二选一；
// 时间窗 ≤90 天）。
func (c *Client) ListMeetingRoomMeetings(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsListMtgs, nil, body)
}

// ListMeetingRoomDevices 获取设备列表（名称模糊匹配）。
func (c *Client) ListMeetingRoomDevices(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsListDev, nil, body)
}

// ListMeetingRoomControllers 获取控制器列表（名称模糊匹配）。
func (c *Client) ListMeetingRoomControllers(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsListCtrl, nil, body)
}

// GetMeetingRoomInventory 获取 Rooms 会议室资源（普通/专款设备总数、已使用与已过期数）。
func (c *Client) GetMeetingRoomInventory(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epRoomsInventory, nil, nil)
}

// CallMeetingRoom 呼叫 Rooms 会议室（meeting_room_id 与 mra_address{protocol 1 SIP/2 H.323,
// dial_string} 二选一；返回 invite_id）。
func (c *Client) CallMeetingRoom(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsCall, nil, body)
}

// CancelCallMeetingRoom 取消呼叫（invite_id + 目标二选一）。
func (c *Client) CancelCallMeetingRoom(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsCancelCall, nil, body)
}

// GetMeetingRoomResponseStatus 获取应答状态（status 0 无应答/1 未呼叫/2 入会中/3 被拒绝/
// 4 呼叫中/5 取消呼叫/6 已离会）。
func (c *Client) GetMeetingRoomResponseStatus(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRoomsRespStatus, nil, body)
}

// QueryMRAStatus 获取 MRA 状态信息（返回名称/静音/视频/举手/默认布局状态）。
func (c *Client) QueryMRAStatus(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMRAStatus, nil, body)
}

// SetMRADefaultLayout 切换 MRA 默认布局（default_layout 1 等分/2 全屏/3 1+N；
// 已显示自定义/个性布局或焦点视频时不可设置）。
func (c *Client) SetMRADefaultLayout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMRALayout, nil, body)
}

// SetMRARaiseHand 设置 MRA 举手或手放下（raise_hand bool）。
func (c *Client) SetMRARaiseHand(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMRARaiseHand, nil, body)
}

// HangupMRA 挂断 MRA 呼叫（mra.tmp_openid）。
func (c *Client) HangupMRA(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMRAHangup, nil, body)
}

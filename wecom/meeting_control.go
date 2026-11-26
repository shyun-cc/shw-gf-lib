package wecom

import (
	"context"
	"net/http"
)

// 会议域子域③表项（会中控制管理）：进行中会议实时控制族
// （官方清点见 change wecom-meeting-control-domain + wecom-meeting-control2 proposal——
// 补数轮铺入屏幕共享/昵称两件与投票族八件，挂起清单清零）
var (
	epRCSet       = Endpoint{"meeting.realcontrol.set", http.MethodPost, "/cgi-bin/meeting/realcontrol/set", nil, true}
	epRCSetCohost = Endpoint{"meeting.realcontrol.set_cohost", http.MethodPost, "/cgi-bin/meeting/realcontrol/set_cohost", nil, true}
	epRCMuteUser  = Endpoint{"meeting.realcontrol.mute_user", http.MethodPost, "/cgi-bin/meeting/realcontrol/mute_user", nil, true}
	epRCVideo     = Endpoint{"meeting.realcontrol.switch_user_video", http.MethodPost, "/cgi-bin/meeting/realcontrol/switch_user_video", nil, true}
	epRCWaitRoom  = Endpoint{"meeting.realcontrol.manage_waiting_room_users", http.MethodPost, "/cgi-bin/meeting/realcontrol/manage_waiting_room_users", nil, true}
	epRCKickout   = Endpoint{"meeting.realcontrol.kickout_users", http.MethodPost, "/cgi-bin/meeting/realcontrol/kickout_users", nil, true}
	epRCDismiss   = Endpoint{"meeting.realcontrol.dismiss", http.MethodPost, "/cgi-bin/meeting/realcontrol/dismiss", nil, true}

	epRCCloseShare  = Endpoint{"meeting.realcontrol.close_screen_share", http.MethodPost, "/cgi-bin/meeting/realcontrol/close_screen_share", nil, true}
	epRCSetNickname = Endpoint{"meeting.realcontrol.set_nicknames", http.MethodPost, "/cgi-bin/meeting/realcontrol/set_nicknames", nil, true}

	epPollCreateTheme  = Endpoint{"meeting.poll.create_theme", http.MethodPost, "/cgi-bin/meeting/poll/create_theme", nil, true}
	epPollUpdateTheme  = Endpoint{"meeting.poll.update_theme", http.MethodPost, "/cgi-bin/meeting/poll/update_theme", nil, true}
	epPollGetList      = Endpoint{"meeting.poll.get_poll_list", http.MethodPost, "/cgi-bin/meeting/poll/get_poll_list", nil, true}
	epPollGetThemeInfo = Endpoint{"meeting.poll.get_theme_info", http.MethodPost, "/cgi-bin/meeting/poll/get_theme_info", nil, true}
	epPollGetDetail    = Endpoint{"meeting.poll.get_poll_detail", http.MethodPost, "/cgi-bin/meeting/poll/get_poll_detail", nil, true}
	epPollDelete       = Endpoint{"meeting.poll.delete", http.MethodPost, "/cgi-bin/meeting/poll/delete", nil, true}
	epPollStart        = Endpoint{"meeting.poll.start", http.MethodPost, "/cgi-bin/meeting/poll/start", nil, true}
	epPollFinish       = Endpoint{"meeting.poll.finish", http.MethodPost, "/cgi-bin/meeting/poll/finish", nil, true}
)

// SetMeetingRealControl 管理会中设置（body：meetingid/mute_all/enable_enter_mute/meeting_locked/allow_chat/…）
func (c *Client) SetMeetingRealControl(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCSet, nil, body)
}

// SetMeetingCohost 管理联席主持人（body：meetingid/action/operated_user）
func (c *Client) SetMeetingCohost(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCSetCohost, nil, body)
}

// MuteMeetingUser 静音成员（body：meetingid/option/operated_user）
func (c *Client) MuteMeetingUser(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCMuteUser, nil, body)
}

// SwitchMeetingUserVideo 关闭或开启成员视频（body：meetingid/video/operated_user）
func (c *Client) SwitchMeetingUserVideo(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCVideo, nil, body)
}

// ManageWaitingroomUsers 管理等候室成员（body：meetingid/operate_type/allow_rejoin/operated_users）
func (c *Client) ManageWaitingroomUsers(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCWaitRoom, nil, body)
}

// KickoutMeetingUsers 移出成员（body：meetingid/allow_rejoin/operated_users）
func (c *Client) KickoutMeetingUsers(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCKickout, nil, body)
}

// DismissMeeting 结束会议（body：meetingid/force_dismiss/retrieve_code）
func (c *Client) DismissMeeting(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCDismiss, nil, body)
}

// CloseMeetingScreenShare 关闭成员屏幕共享（operated_user.instance_id 须与被操作者实际
// 设备一致否则不生效；暂不支持 MRA 设备作为被操作者）。
func (c *Client) CloseMeetingScreenShare(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCCloseShare, nil, body)
}

// SetMeetingNicknames 修改成员在会中显示的昵称（operated_users 批量，nickname ≤20 字符）。
func (c *Client) SetMeetingNicknames(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRCSetNickname, nil, body)
}

// CreateMeetingPollTheme 创建会议投票主题（仅进行中会议、操作者须为主持人或会议管理员；
// poll_questions ≤10 问题、每题选项 2-10 项 ≤36 字符；返回 poll_theme_id）。
func (c *Client) CreateMeetingPollTheme(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPollCreateTheme, nil, body)
}

// UpdateMeetingPollTheme 修改会议投票主题（全覆盖修改，原内容被整体覆盖）。
func (c *Client) UpdateMeetingPollTheme(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPollUpdateTheme, nil, body)
}

// ListMeetingPolls 获取会议投票列表（返回 polls_theme_info，含 poll_theme_id 与 status）。
func (c *Client) ListMeetingPolls(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPollGetList, nil, body)
}

// GetMeetingPollTheme 获取会议投票主题信息（含问题与选项，不含投票结果）。
func (c *Client) GetMeetingPollTheme(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPollGetThemeInfo, nil, body)
}

// GetMeetingPollDetail 获取会议投票详情（含结果：vote_total_num、各选项票数与比率）。
func (c *Client) GetMeetingPollDetail(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPollGetDetail, nil, body)
}

// DeleteMeetingPoll 删除会议投票（poll_theme_id 与 poll_id 二选一，同传以 poll_id 为准；
// 主题下实例全删则主题一并删除）。
func (c *Client) DeleteMeetingPoll(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPollDelete, nil, body)
}

// StartMeetingPoll 发起会议投票（以已有主题发起，返回 poll_id）。
func (c *Client) StartMeetingPoll(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPollStart, nil, body)
}

// FinishMeetingPoll 结束会议投票。
func (c *Client) FinishMeetingPoll(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPollFinish, nil, body)
}

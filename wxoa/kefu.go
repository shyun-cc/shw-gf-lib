package wxoa

import (
	"context"
	"net/http"
)

// 客服消息表项（账号 7 + 会话 5 + 消息 3；官方 kf 页取证 2026-08-24。
// typing 为公众号形态 path（无 /business 段，区别于 wxapp 小程序形态））
var (
	epKFAccountAdd = Endpoint{"kf.account_add", http.MethodPost, "/customservice/kfaccount/add", nil, true}
	epKFAccountUpd = Endpoint{"kf.account_update", http.MethodPost, "/customservice/kfaccount/update", nil, true}
	epKFAccountDel = Endpoint{"kf.account_del", http.MethodPost, "/customservice/kfaccount/del", nil, true}
	epKFHeadImg    = Endpoint{"kf.headimg", http.MethodPost, "/customservice/kfaccount/uploadheadimg", []string{"kf_account"}, true}
	epKFInvite     = Endpoint{"kf.invite", http.MethodPost, "/customservice/kfaccount/inviteworker", nil, true}
	epKFList       = Endpoint{"kf.list", http.MethodGet, "/cgi-bin/customservice/getkflist", nil, false}
	epKFOnlineList = Endpoint{"kf.online_list", http.MethodGet, "/cgi-bin/customservice/getonlinekflist", nil, false}

	epKFSessionCreate = Endpoint{"kf.session_create", http.MethodPost, "/customservice/kfsession/create", nil, true}
	epKFSessionClose  = Endpoint{"kf.session_close", http.MethodPost, "/customservice/kfsession/close", nil, true}
	epKFSessionGet    = Endpoint{"kf.session_get", http.MethodGet, "/customservice/kfsession/getsession", []string{"openid"}, false}
	epKFSessionList   = Endpoint{"kf.session_list", http.MethodGet, "/customservice/kfsession/getsessionlist", []string{"kf_account"}, false}
	epKFWaitCases     = Endpoint{"kf.waitcase", http.MethodGet, "/customservice/kfsession/getwaitcase", nil, false}

	epKFSend    = Endpoint{"kf.send", http.MethodPost, "/cgi-bin/message/custom/send", nil, true}
	epKFTyping  = Endpoint{"kf.typing", http.MethodPost, "/cgi-bin/message/custom/typing", nil, true}
	epKFMsgList = Endpoint{"kf.msg_records", http.MethodPost, "/customservice/msgrecord/getmsglist", nil, true}
)

// AddKFAccount 添加客服账号（完整账号 前缀@公众号微信号；≤100 个）。
func (s *WxOAService) AddKFAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epKFAccountAdd, body)
}

// UpdateKFAccount 修改客服账号。
func (s *WxOAService) UpdateKFAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epKFAccountUpd, body)
}

// DelKFAccount 删除客服账号。
func (s *WxOAService) DelKFAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epKFAccountDel, body)
}

// UploadKFHeadImg 设置客服头像（multipart，part 名 media；jpg ≤5M）。
func (s *WxOAService) UploadKFHeadImg(ctx context.Context, kfAccount, filename string, content []byte) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeMultipart(ctx, epKFHeadImg, map[string]string{
		"access_token": token, "kf_account": kfAccount,
	}, "media", filename, content)
}

// InviteKFWorker 邀请绑定客服账号（invite_wx 微信号；被邀者 24h 内确认）。
func (s *WxOAService) InviteKFWorker(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epKFInvite, body)
}

// ListKFAccounts 获取所有客服账号（GET）。
func (s *WxOAService) ListKFAccounts(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epKFList, nil)
}

// ListOnlineKF 获取在线客服列表（GET）。
func (s *WxOAService) ListOnlineKF(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epKFOnlineList, nil)
}

// CreateKFSession 创建会话（kf_account+openid；该客服同时接待 ≤100 人）。
func (s *WxOAService) CreateKFSession(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epKFSessionCreate, body)
}

// CloseKFSession 关闭会话。
func (s *WxOAService) CloseKFSession(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epKFSessionClose, body)
}

// GetKFSession 获取客户会话状态（GET，openid）。
func (s *WxOAService) GetKFSession(ctx context.Context, openid string) (map[string]any, error) {
	return s.oaGet(ctx, epKFSessionGet, map[string]string{"openid": openid})
}

// ListKFSessions 获取客服会话列表（GET，kf_account）。
func (s *WxOAService) ListKFSessions(ctx context.Context, kfAccount string) (map[string]any, error) {
	return s.oaGet(ctx, epKFSessionList, map[string]string{"kf_account": kfAccount})
}

// ListKFWaitCases 获取未接入会话列表（GET）。
func (s *WxOAService) ListKFWaitCases(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epKFWaitCases, nil)
}

// SendKFMsg 发送客服消息（touser+msgtype；48h 交互窗口；须已开通客服功能）。
func (s *WxOAService) SendKFMsg(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epKFSend, body)
}

// SetKFTyping 客服输入状态（command Typing/CancelTyping；公众号形态 path——
// wxapp 铺的是小程序 /business 变体）。
func (s *WxOAService) SetKFTyping(ctx context.Context, touser, command string) (map[string]any, error) {
	return s.oaPost(ctx, epKFTyping, map[string]any{"touser": touser, "command": command})
}

// GetKFMsgRecords 获取聊天记录（endtime/begin_time ±10 天；pageindex/pagesize ≤50/页 ≤60 万条）。
func (s *WxOAService) GetKFMsgRecords(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epKFMsgList, body)
}

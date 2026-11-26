package wxapp

import (
	"context"
	"errors"
)

// 小程序客服·客服消息族（官方取证见 change wxapp-openapi-kf proposal；
// typing 为小程序形态 path（含 /business 段），公众号变体归 wxoa 域）。

// AddKFAccount 添加客服账号（kf_account 前缀 ≤10 字符@公众号微信号、nickname ≤16 字；
// 每账号 ≤100 个客服）。
func (s *WxAppService) AddKFAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFAccountAdd, map[string]string{"access_token": token}, body)
}

// ListKFAccounts 获取所有客服账号（businessID 为客服子商户 ID，普通账号留空）。
func (s *WxAppService) ListKFAccounts(ctx context.Context, businessID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]string{"access_token": token}
	if businessID != "" {
		query["business_id"] = businessID
	}
	return s.invoke(ctx, epKFList, query, nil)
}

// ListOnlineKFAccounts 获取在线客服列表（在线状态/当前接待数/kf_openid）。
func (s *WxAppService) ListOnlineKFAccounts(ctx context.Context, businessID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]string{"access_token": token}
	if businessID != "" {
		query["business_id"] = businessID
	}
	return s.invoke(ctx, epKFOnlineList, query, nil)
}

// DelKFAccount 删除客服账号。
func (s *WxAppService) DelKFAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFAccountDel, map[string]string{"access_token": token}, body)
}

// SendCustomMessage 发送客服消息（48 小时交互窗口内可发、有条数限制；
// customservice.kf_account 可指定客服发送）。
func (s *WxAppService) SendCustomMessage(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFSend, map[string]string{"access_token": token}, body)
}

// SetTyping 客服输入状态（command: Typing 下发正在输入 / CancelTyping 取消；
// 须此前 30 秒内与用户有过消息交互，否则 45080）。
func (s *WxAppService) SetTyping(ctx context.Context, touser, command string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFTyping, map[string]string{"access_token": token},
		map[string]string{"touser": touser, "command": command})
}

// SetKFAdmin 设置客服管理员（GET query kf_openid）。
func (s *WxAppService) SetKFAdmin(ctx context.Context, kfOpenid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFSetAdmin, map[string]string{"access_token": token, "kf_openid": kfOpenid}, nil)
}

// CancelKFAdmin 取消客服管理员（GET query kf_openid）。
func (s *WxAppService) CancelKFAdmin(ctx context.Context, kfOpenid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFCancelAdmin, map[string]string{"access_token": token, "kf_openid": kfOpenid}, nil)
}

// UploadTempMedia 新增临时素材（multipart 上传；mediaType: image 10M / voice 2M≤60s /
// video 10M / thumb 64KB；素材保留 3 天，media_id 可复用）。
func (s *WxAppService) UploadTempMedia(ctx context.Context, mediaType, filename string, content []byte) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeMultipart(ctx, epKFMediaUpload, map[string]string{"access_token": token, "type": mediaType}, "media", filename, content)
}

// GetTempMedia 获取临时素材（图片/语音为二进制流（返回原始字节）；视频返回 video_url
// 由调用方二次下载；errcode 形态走 BizError）。
func (s *WxAppService) GetTempMedia(ctx context.Context, mediaID string) ([]byte, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeRaw(ctx, epKFMediaGet, map[string]string{"access_token": token, "media_id": mediaID}, nil)
}

// errMissingField 供本文件与 openapi_mgmt.go 复用的缺字段错误（避免再引 errors 包到多处）
func errMissingField(msg string) error { return errors.New(msg) }

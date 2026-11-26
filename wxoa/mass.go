package wxoa

import (
	"context"
	"net/http"
)

// 群发消息表项（官方 Batch_Sends 页取证 2026-08-24；uploadimg 与首批素材域同 path 不重复立）
var (
	epMassSendAll = Endpoint{"mass.sendall", http.MethodPost, "/cgi-bin/message/mass/sendall", nil, true}
	epMassSend    = Endpoint{"mass.send", http.MethodPost, "/cgi-bin/message/mass/send", nil, true}
	epMassPreview = Endpoint{"mass.preview", http.MethodPost, "/cgi-bin/message/mass/preview", nil, true}
	epMassDelete  = Endpoint{"mass.delete", http.MethodPost, "/cgi-bin/message/mass/delete", nil, true}
	epMassGet     = Endpoint{"mass.get", http.MethodPost, "/cgi-bin/message/mass/get", nil, true}
	epMassSpdGet  = Endpoint{"mass.speed_get", http.MethodPost, "/cgi-bin/message/mass/speed/get", nil, true}
	epMassSpdSet  = Endpoint{"mass.speed_set", http.MethodPost, "/cgi-bin/message/mass/speed/set", nil, true}
	epMassNews    = Endpoint{"mass.uploadnews", http.MethodPost, "/cgi-bin/media/uploadnews", nil, true}
)

// MassSendAll 按标签群发（tag_id 全量；图文须用草稿箱 media_id）。
func (s *WxOAService) MassSendAll(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epMassSendAll, body)
}

// MassSend 按 OpenID 列表群发（touser ≤10000）。
func (s *WxOAService) MassSend(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epMassSend, body)
}

// MassPreview 预览消息（指定用户校验样式排版）。
func (s *WxOAService) MassPreview(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epMassPreview, body)
}

// MassDelete 删除群发消息（群发后随时可调）。
func (s *WxOAService) MassDelete(ctx context.Context, msgID int) (map[string]any, error) {
	return s.oaPost(ctx, epMassDelete, map[string]any{"msg_id": msgID})
}

// MassGet 查询群发消息发送状态（msg_id）。
func (s *WxOAService) MassGet(ctx context.Context, msgID int) (map[string]any, error) {
	return s.oaPost(ctx, epMassGet, map[string]any{"msg_id": msgID})
}

// MassSpeedGet 获取群发速度。
func (s *WxOAService) MassSpeedGet(ctx context.Context) (map[string]any, error) {
	return s.oaPost(ctx, epMassSpdGet, nil)
}

// MassSpeedSet 设置群发速度（speed 0 快速-4 匀速）。
func (s *WxOAService) MassSpeedSet(ctx context.Context, speed int) (map[string]any, error) {
	return s.oaPost(ctx, epMassSpdSet, map[string]any{"speed": speed})
}

// UploadNews 上传图文素材（官方注明该能力已更新为草稿箱——mpnews 内容现用草稿箱
// media_id；表项保留供存量调用方）。
func (s *WxOAService) UploadNews(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epMassNews, body)
}

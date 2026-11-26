package wxoa

import (
	"context"
	"net/http"
)

// 模板消息表项（官方 template_message 页取证 2026-08-24）
var (
	epTmplSetIndustry = Endpoint{"tmpl.set_industry", http.MethodPost, "/cgi-bin/template/api_set_industry", nil, true}
	epTmplGetIndustry = Endpoint{"tmpl.get_industry", http.MethodPost, "/cgi-bin/template/get_industry", nil, true}
	epTmplAdd         = Endpoint{"tmpl.add", http.MethodPost, "/cgi-bin/template/api_add_template", nil, true}
	epTmplList        = Endpoint{"tmpl.list", http.MethodPost, "/cgi-bin/template/get_all_private_template", nil, true}
	epTmplDel         = Endpoint{"tmpl.del", http.MethodPost, "/cgi-bin/template/del_private_template", nil, true}
	epTmplSend        = Endpoint{"tmpl.send", http.MethodPost, "/cgi-bin/message/template/send", nil, true}
	epTmplBlocked     = Endpoint{"tmpl.blocked", http.MethodPost, "/wxa/sec/queryblocktmplmsg", nil, true}
)

// SetTmplIndustry 设置所属行业（改行业后原行业模板被删除；每月可修改一次）。
func (s *WxOAService) SetTmplIndustry(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epTmplSetIndustry, body)
}

// GetTmplIndustry 获取行业信息。
func (s *WxOAService) GetTmplIndustry(ctx context.Context) (map[string]any, error) {
	return s.oaPost(ctx, epTmplGetIndustry, nil)
}

// AddTmpl 选用模板（template_id_short；返回 template_id）。
func (s *WxOAService) AddTmpl(ctx context.Context, shortID string) (map[string]any, error) {
	return s.oaPost(ctx, epTmplAdd, map[string]any{"template_id_short": shortID})
}

// ListTmpl 获取已选用模板列表。
func (s *WxOAService) ListTmpl(ctx context.Context) (map[string]any, error) {
	return s.oaPost(ctx, epTmplList, nil)
}

// DelTmpl 删除模板。
func (s *WxOAService) DelTmpl(ctx context.Context, templateID string) (map[string]any, error) {
	return s.oaPost(ctx, epTmplDel, map[string]any{"template_id": templateID})
}

// SendTmplMsg 发送模板消息（touser/template_id/url 或 miniprogram 跳转二选一/data；
// 送达结果经 TEMPLATESENDJOBFINISH 事件推送——Status success/failed:user block 等）。
func (s *WxOAService) SendTmplMsg(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epTmplSend, body)
}

// QueryBlockedTmplMsg 查询拦截的模板消息。
func (s *WxOAService) QueryBlockedTmplMsg(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epTmplBlocked, body)
}

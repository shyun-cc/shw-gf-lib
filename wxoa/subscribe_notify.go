package wxoa

import (
	"context"
	"net/http"
	"strconv"
)

// 订阅通知表项（公众号与小程序共用 newtmpl 模板管理族；bizsend 为公众号发送形态）
var (
	epSubsCategory = Endpoint{"subsmsg.getcategory", http.MethodGet, "/wxaapi/newtmpl/getcategory", nil, false}
	epSubsPubTitle = Endpoint{"subsmsg.pubtitles", http.MethodGet, "/wxaapi/newtmpl/getpubtemplatetitles", []string{"ids", "start", "limit"}, false}
	epSubsPubKw    = Endpoint{"subsmsg.pubkeywords", http.MethodGet, "/wxaapi/newtmpl/getpubtemplatekeywords", []string{"tid"}, false}
	epSubsAddTpl   = Endpoint{"subsmsg.addtemplate", http.MethodPost, "/wxaapi/newtmpl/addtemplate", nil, true}
	epSubsGetTpl   = Endpoint{"subsmsg.gettemplate", http.MethodGet, "/wxaapi/newtmpl/gettemplate", nil, false}
	epSubsDelTpl   = Endpoint{"subsmsg.deltemplate", http.MethodPost, "/wxaapi/newtmpl/deltemplate", nil, true}
	epSubsBizSend  = Endpoint{"subsmsg.bizsend", http.MethodPost, "/cgi-bin/message/subscribe/bizsend", nil, true}
)

// GetSubsCategory 获取类目（公共模板库检索入口）。
func (s *WxOAService) GetSubsCategory(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epSubsCategory, nil)
}

// ListPubTitles 获取类目下公共模板（ids 多类目逗号分隔；limit ≤30）。
func (s *WxOAService) ListPubTitles(ctx context.Context, ids string, start, limit int) (map[string]any, error) {
	return s.oaGet(ctx, epSubsPubTitle, map[string]string{
		"ids": ids, "start": strconv.Itoa(start), "limit": strconv.Itoa(limit),
	})
}

// GetPubKeywords 获取模板关键词（kid 供选用组合）。
func (s *WxOAService) GetPubKeywords(ctx context.Context, tid string) (map[string]any, error) {
	return s.oaGet(ctx, epSubsPubKw, map[string]string{"tid": tid})
}

// AddSubsTemplate 选用模板（tid+kidList+sceneDesc；返回 priTmplId）。
func (s *WxOAService) AddSubsTemplate(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epSubsAddTpl, body)
}

// ListSubsTemplates 获取已有模板列表（GET）。
func (s *WxOAService) ListSubsTemplates(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epSubsGetTpl, nil)
}

// DelSubsTemplate 删除模板（priTmplId）。
func (s *WxOAService) DelSubsTemplate(ctx context.Context, priTmplID string) (map[string]any, error) {
	return s.oaPost(ctx, epSubsDelTpl, map[string]any{"priTmplId": priTmplID})
}

// SendSubsMsg 发送订阅通知（touser/template_id/page(小程序跳转)/data/miniprogram 族；
// 用户须先订阅，一次性订阅每次发送消耗一次）。
func (s *WxOAService) SendSubsMsg(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epSubsBizSend, body)
}

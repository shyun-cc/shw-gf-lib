package wxapp

import (
	"context"
	"net/http"
)

// 搜一搜 / 红包封面 / 服务市场表项（官方取证见 change wxapp-ocr-face-misc proposal）
var (
	epSearchSubmit   = Endpoint{"search.submit", http.MethodPost, "/wxa/search/wxaapi_submitpages", []string{"access_token"}, true}
	epRedCoverURL    = Endpoint{"redcover.get_url", http.MethodPost, "/redpacketcover/wxapp/cover_url/get_by_token", []string{"access_token"}, true}
	epSvcMktInvoke   = Endpoint{"svcmkt.invoke", http.MethodPost, "/wxa/servicemarket", []string{"access_token"}, true}
	epSvcMktRetrieve = Endpoint{"svcmkt.retrieve", http.MethodPost, "/wxa/servicemarketretrieve", []string{"access_token"}, true}
)

// SubmitSearchPages 搜一搜数据推送（pages ≤1000/次、日 50 万；page_type 固定 2；
// update 1 新增/3 删除；不支持第三方平台调用）。
func (s *WxAppService) SubmitSearchPages(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSearchSubmit, map[string]string{"access_token": token}, body)
}

// GetRedPacketCoverURL 获取微信红包封面（ctoken 为红包封面平台发放 token，须指定可发放
// appid；返回用户可领取的封面地址）。
func (s *WxAppService) GetRedPacketCoverURL(ctx context.Context, openid, ctoken string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epRedCoverURL, map[string]string{"access_token": token}, map[string]string{
		"openid": openid, "ctoken": ctoken,
	})
}

// InvokeService 调用服务市场接口（service/api/data/client_msg_id 必填；async=true 时
// 返回 request_id 供异步取回）。
func (s *WxAppService) InvokeService(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSvcMktInvoke, map[string]string{"access_token": token}, body)
}

// RetrieveServiceData 异步获取服务市场处理数据（requestId 来自 InvokeService；
// 9301015 处理中 / 9301012 超时 / 9301010 配额不足）。
func (s *WxAppService) RetrieveServiceData(ctx context.Context, requestID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSvcMktRetrieve, map[string]string{"access_token": token}, map[string]string{
		"request_id": requestID,
	})
}

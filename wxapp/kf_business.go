package wxapp

import (
	"context"
	"net/http"
)

// 客服子商户表项（仅电商类目小程序；官方取证见 change wxapp-subscribe-message proposal）
var (
	epKFBizRegister = Endpoint{"kf.business_register", http.MethodPost, "/cgi-bin/business/register", []string{"access_token"}, true}
	epKFBizUpdate   = Endpoint{"kf.business_update", http.MethodPost, "/cgi-bin/business/update", []string{"access_token"}, true}
	epKFBizGet      = Endpoint{"kf.business_get", http.MethodPost, "/cgi-bin/business/get", []string{"access_token"}, true}
	epKFBizList     = Endpoint{"kf.business_list", http.MethodPost, "/cgi-bin/business/list", []string{"access_token"}, true}
)

// RegisterKFBusiness 客服子商户创建商户（account_name 6-30 字符仅可创建一次；nickname 4-30；
// icon_media_id 须经 UploadTempMedia 上传取得；transfer_to_commkf 开启后可用客服管理 API；
// 返回 business_id）。
func (s *WxAppService) RegisterKFBusiness(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFBizRegister, map[string]string{"access_token": token}, body)
}

// UpdateKFBusiness 更新商户信息（business_id 必填；nickname/icon_media_id 选填，空则不更新）。
func (s *WxAppService) UpdateKFBusiness(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFBizUpdate, map[string]string{"access_token": token}, body)
}

// GetKFBusiness 拉取单个商户信息（businessID 与 accountName 二选一）。
func (s *WxAppService) GetKFBusiness(ctx context.Context, businessID, accountName string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if businessID != "" {
		body["business_id"] = businessID
	}
	if accountName != "" {
		body["account_name"] = accountName
	}
	return s.invoke(ctx, epKFBizGet, map[string]string{"access_token": token}, body)
}

// ListKFBusiness 拉取多个商户信息（offset 从 0 起、count ≤200；返回量小于 count 即到末端）。
func (s *WxAppService) ListKFBusiness(ctx context.Context, offset, count int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKFBizList, map[string]string{"access_token": token}, map[string]any{
		"offset": offset, "count": count,
	})
}

package wxapp

import (
	"context"
	"net/http"
)

// 微信客服（小程序侧绑定族；官方取证 kf-work 三页 2026-08-24，见 roadmap 轨迹。
// 区别于 wecom 包微信客服域：此处为小程序↔企业绑定关系管理）
var (
	epKfWorkGet    = Endpoint{"kfwork.get", http.MethodGet, "/customservice/work/get", []string{"access_token"}, false}
	epKfWorkBind   = Endpoint{"kfwork.bind", http.MethodPost, "/customservice/work/bind", []string{"access_token"}, true}
	epKfWorkUnbind = Endpoint{"kfwork.unbind", http.MethodPost, "/customservice/work/unbind", []string{"access_token"}, true}
)

// GetKfWorkBound 查询微信客服绑定情况（返回 entityName/corpid/bindTime，未绑定时无此三字段）。
func (s *WxAppService) GetKfWorkBound(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKfWorkGet, map[string]string{"access_token": token}, nil)
}

// BindKfWork 绑定微信客服（corpid 须与小程序主体一致；仅非个人主体小程序）。
func (s *WxAppService) BindKfWork(ctx context.Context, corpid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKfWorkBind, map[string]string{"access_token": token}, map[string]string{"corpid": corpid})
}

// UnbindKfWork 解除绑定微信客服（corpid 同绑定校验）。
func (s *WxAppService) UnbindKfWork(ctx context.Context, corpid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epKfWorkUnbind, map[string]string{"access_token": token}, map[string]string{"corpid": corpid})
}

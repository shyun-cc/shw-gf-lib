package wxoa

import (
	"context"
	"net/http"
)

// 自定义菜单表项（官方取证 menu/intro 七接口 2026-08-24，见 change wxoa-basic-domains proposal）
var (
	epMenuCreate   = Endpoint{"menu.create", http.MethodPost, "/cgi-bin/menu/create", nil, true}
	epMenuSelfGet  = Endpoint{"menu.selfmenu", http.MethodGet, "/cgi-bin/get_current_selfmenu_info", nil, false}
	epMenuGet      = Endpoint{"menu.get", http.MethodGet, "/cgi-bin/menu/get", nil, false}
	epMenuDelete   = Endpoint{"menu.delete", http.MethodGet, "/cgi-bin/menu/delete", nil, false}
	epMenuAddCond  = Endpoint{"menu.addconditional", http.MethodPost, "/cgi-bin/menu/addconditional", nil, true}
	epMenuDelCond  = Endpoint{"menu.delconditional", http.MethodPost, "/cgi-bin/menu/delconditional", nil, true}
	epMenuTryMatch = Endpoint{"menu.trymatch", http.MethodGet, "/cgi-bin/menu/trymatch", []string{"user_id"}, false}
)

// oaGet 带 token 的 GET 分发。
func (s *WxOAService) oaGet(ctx context.Context, ep Endpoint, extra map[string]string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]string{"access_token": token}
	for k, v := range extra {
		query[k] = v
	}
	return s.invoke(ctx, ep, query, nil)
}

// oaPost 带 token 的 POST 分发（nil body 转接口 nil 防 "null"）。
func (s *WxOAService) oaPost(ctx context.Context, ep Endpoint, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return s.invoke(ctx, ep, map[string]string{"access_token": token}, nil)
	}
	return s.invoke(ctx, ep, map[string]string{"access_token": token}, body)
}

// CreateMenu 创建自定义菜单（最多 3 个一级、每级最多 5 个二级）。
func (s *WxOAService) CreateMenu(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epMenuCreate, body)
}

// GetCurrentSelfMenu 查询自定义菜单（手机端实际展示形态）。
func (s *WxOAService) GetCurrentSelfMenu(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epMenuSelfGet, nil)
}

// GetMenu 获取自定义菜单配置（含个性化菜单组；接口停用态返回 is_menu_open 0）。
func (s *WxOAService) GetMenu(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epMenuGet, nil)
}

// DeleteMenu 删除自定义菜单（含个性化）。
func (s *WxOAService) DeleteMenu(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epMenuDelete, nil)
}

// AddConditionalMenu 创建个性化菜单（matchrule 用户分组/性别/客户端版本/地区匹配）。
func (s *WxOAService) AddConditionalMenu(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epMenuAddCond, body)
}

// DeleteConditionalMenu 删除个性化菜单（menuid）。
func (s *WxOAService) DeleteConditionalMenu(ctx context.Context, menuID string) (map[string]any, error) {
	return s.oaPost(ctx, epMenuDelCond, map[string]any{"menuid": menuID})
}

// TryMatchMenu 测试个性化菜单匹配结果（user_id 粉丝 openid）。
func (s *WxOAService) TryMatchMenu(ctx context.Context, userID string) (map[string]any, error) {
	return s.oaGet(ctx, epMenuTryMatch, map[string]string{"user_id": userID})
}

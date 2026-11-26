package wxapp

import (
	"context"
	"net/http"
)

// 插件管理表项（官方取证见 change wxapp-hardware-ops-plugin proposal）
var (
	epPluginApplication = Endpoint{"plugin.application", http.MethodPost, "/wxa/devplugin", []string{"access_token"}, true}
	epPluginManage      = Endpoint{"plugin.manage", http.MethodPost, "/wxa/plugin", []string{"access_token"}, true}
)

// ManagePluginApplication 插件申请管理（插件开发者侧）：action dev_agree 同意（须 appid）/
// dev_refuse 拒绝（可 reason）/ dev_delete 删除已拒申请者 / dev_apply_list 拉取使用方
// （page+num 分页）。
func (s *WxAppService) ManagePluginApplication(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epPluginApplication, map[string]string{"access_token": token}, body)
}

// ManagePlugin 插件管理（使用方侧）：action list 查询已添加 / apply 申请使用（须 reason）/
// unbind 删除 / update 快速更新版本（须 user_version 且该版本支持快速更新）；
// plugin_appid 必填。
func (s *WxAppService) ManagePlugin(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epPluginManage, map[string]string{"access_token": token}, body)
}

package wxapp

import (
	"context"
)

// 凭证与 openApi 运维族（官方取证见 change wxapp-openapi-kf proposal）。

// GetStableAccessToken 获取稳定版接口调用凭据（POST body 形态；与旧版 GetAccessToken
// 完全隔离互不影响——本方法不做缓存，普通模式有效期内重复调用返回同一 token 与剩余时间，
// 缓存策略归调用方；forceRefresh 强制刷新模式日 20 次且需间隔 30s，慎用）。
func (s *WxAppService) GetStableAccessToken(ctx context.Context, forceRefresh bool) (string, error) {
	body := map[string]any{
		"grant_type": "client_credential",
		"appid":      s.AppID,
		"secret":     s.Secret,
	}
	if forceRefresh {
		body["force_refresh"] = true
	}
	decoded, err := s.invoke(ctx, epStableToken, nil, body)
	if err != nil {
		return "", err
	}
	token, _ := decoded["access_token"].(string)
	if token == "" {
		return "", &InfraError{Op: "token", Err: errMissingField("stable_token 响应缺少 access_token")}
	}
	return token, nil
}

// GetApiQuota 查询指定接口的每日调用额度与频率限制（cgi_path 须以 / 开头且不带域名）。
func (s *WxAppService) GetApiQuota(ctx context.Context, cgiPath string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epQuotaGet, map[string]string{"access_token": token}, map[string]string{"cgi_path": cgiPath})
}

// ClearQuota 重置账号全部 API 调用次数（与 ClearQuotaByAppSecret 共享每月 10 次）。
func (s *WxAppService) ClearQuota(ctx context.Context, appid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epQuotaClearAll, map[string]string{"access_token": token}, map[string]string{"appid": appid})
}

// ClearApiQuota 重置指定接口调用次数（每月 50 次）。
func (s *WxAppService) ClearApiQuota(ctx context.Context, cgiPath string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epQuotaClearOne, map[string]string{"access_token": token}, map[string]string{"cgi_path": cgiPath})
}

// ClearQuotaByAppSecret 使用 AppSecret 重置调用次数（POST query 传参特例——access_token
// 耗尽时的兜底通道；appid/appsecret 取服务实例配置）。
func (s *WxAppService) ClearQuotaByAppSecret(ctx context.Context) (map[string]any, error) {
	return s.invoke(ctx, epQuotaClearV2, map[string]string{"appid": s.AppID, "appsecret": s.Secret}, nil)
}

// GetRidInfo 查询 rid 详情（rid 有效期 7 天、仅同账号；返回请求时间/耗时/URL/报文等）。
func (s *WxAppService) GetRidInfo(ctx context.Context, rid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epRidGet, map[string]string{"access_token": token}, map[string]string{"rid": rid})
}

// CallbackCheck 网络通信检测（action: dns/ping/all；checkOperator: CHINANET/UNICOM/CAP/DEFAULT；
// 对回调 URL 做解析与 ping，返回丢包率与耗时）。
func (s *WxAppService) CallbackCheck(ctx context.Context, action, checkOperator string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epCallbackCheck, map[string]string{"access_token": token},
		map[string]string{"action": action, "check_operator": checkOperator})
}

// GetApiDomainIPs 获取微信 API 服务器 IP 列表（官方建议每日刷新一次）。
func (s *WxAppService) GetApiDomainIPs(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epApiDomainIP, map[string]string{"access_token": token}, nil)
}

// GetCallbackIPs 获取微信推送服务器 IP 列表（回调白名单维护用）。
func (s *WxAppService) GetCallbackIPs(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epCallbackIP, map[string]string{"access_token": token}, nil)
}

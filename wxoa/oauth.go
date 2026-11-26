package wxoa

import (
	"context"
	"net/http"
	"net/url"
)

// 网页授权表项（官方 h5/auth 页取证 2026-08-24；GET 全族。
// sns 体系用网页授权专用 token——Query 显式声明各自凭证参数，不走 access_token 注入）
var (
	epSnsToken    = Endpoint{"sns.token", http.MethodGet, "/sns/oauth2/access_token", []string{"appid", "secret", "code", "grant_type"}, false}
	epSnsRefresh  = Endpoint{"sns.refresh", http.MethodGet, "/sns/oauth2/refresh_token", []string{"appid", "grant_type", "refresh_token"}, false}
	epSnsUserinfo = Endpoint{"sns.userinfo", http.MethodGet, "/sns/userinfo", []string{"access_token", "openid", "lang"}, false}
	epSnsAuth     = Endpoint{"sns.auth", http.MethodGet, "/sns/auth", []string{"access_token", "openid"}, false}
)

// BuildOAuthAuthorizeURL 构造网页授权跳转链接（前端 <a> 或重定向用，非服务端端点）。
// scope：snsapi_base 静默仅 openid / snsapi_userinfo 弹窗可拿昵称等；state ≤128 字节；
// 返回形如 https://open.weixin.qq.com/connect/oauth2/authorize?...#wechat_redirect。
func BuildOAuthAuthorizeURL(appID, redirectURI, scope, state string) string {
	return "https://open.weixin.qq.com/connect/oauth2/authorize?appid=" + url.QueryEscape(appID) +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&response_type=code&scope=" + url.QueryEscape(scope) +
		"&state=" + url.QueryEscape(state) + "#wechat_redirect"
}

// GetOAuthAccessToken code 换网页授权 access_token（code 一次性、5 分钟有效；
// 返回 access_token/expires_in/refresh_token/openid/scope；快照账号 is_snapshotuser=1）。
// secret 仅在服务端使用——严禁下发客户端。
func (s *WxOAService) GetOAuthAccessToken(ctx context.Context, code string) (map[string]any, error) {
	return s.invoke(ctx, epSnsToken, map[string]string{
		"appid": s.AppID, "secret": s.Secret, "code": code, "grant_type": "authorization_code",
	}, nil)
}

// RefreshOAuthToken 刷新网页授权 token（refresh_token 30 天有效，失效需用户重新授权）。
func (s *WxOAService) RefreshOAuthToken(ctx context.Context, refreshToken string) (map[string]any, error) {
	return s.invoke(ctx, epSnsRefresh, map[string]string{
		"appid": s.AppID, "grant_type": "refresh_token", "refresh_token": refreshToken,
	}, nil)
}

// GetOAuthUserInfo 拉取用户信息（仅 snsapi_userinfo 授权可调；access_token 为网页授权专用
// 而非基础凭证；返回 openid/nickname/sex/省市/headimgurl/privilege/unionid）。
func (s *WxOAService) GetOAuthUserInfo(ctx context.Context, accessToken, openid, lang string) (map[string]any, error) {
	if lang == "" {
		lang = "zh_CN"
	}
	return s.invoke(ctx, epSnsUserinfo, map[string]string{
		"access_token": accessToken, "openid": openid, "lang": lang,
	}, nil)
}

// CheckOAuthAuth 检验网页授权是否有效（errcode 0 即有效）。
func (s *WxOAService) CheckOAuthAuth(ctx context.Context, accessToken, openid string) (map[string]any, error) {
	return s.invoke(ctx, epSnsAuth, map[string]string{
		"access_token": accessToken, "openid": openid,
	}, nil)
}

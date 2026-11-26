// Package wxapp 微信小程序服务端 API 自主复刻（零厂商 SDK），由原 wxa 包改名承接（BREAKING，
// 导出类型前缀 WxApp）。API 域覆盖以微信官方文档全集为基准，本包当前承载登录三件套，其余域随
// wechat-ecosystem roadmap 各域 change 铺设。
//
// 登录三件套：code2Session（换 openid/session_key）、getPhoneNumber（换手机号明文）、
// getAccessToken（Redis 缓存 TTL 7000s + redislock 并发去重——补上 PHP 侧缺失）。
package wxapp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shyun-cc/shw-gf-lib/redislock"

	"github.com/gogf/gf/v2/frame/g"
)

const (
	defaultBaseURL = "https://api.weixin.qq.com"

	accessTokenKeyPrefix = "wxapp:access_token:"
	accessTokenTTL       = 7000 // 微信侧 7200s，提前 200s 刷新
	tokenLockTTL         = 10
)

// WxAppService 微信小程序服务（四包同 import 时以 WxApp 前缀与其他包类型可辨）
type WxAppService struct {
	AppID      string
	Secret     string
	HTTPClient *http.Client
	BaseURL    string
}

// NewWxAppService 构造服务
func NewWxAppService(appID, secret string) *WxAppService {
	return &WxAppService{AppID: appID, Secret: secret}
}

// WxAppSession code2Session 结果
type WxAppSession struct {
	OpenID     string
	SessionKey string
	UnionID    string // 满足下发条件时非空
}

// Code2Session 登录凭证校验：GET /sns/jscode2session
func (s *WxAppService) Code2Session(ctx context.Context, code string) (*WxAppSession, error) {
	query := map[string]string{
		"appid":      s.AppID,
		"secret":     s.Secret,
		"js_code":    code,
		"grant_type": "authorization_code",
	}
	decoded, err := s.invoke(ctx, epCode2Session, query, nil)
	if err != nil {
		return nil, err
	}
	unionID, _ := decoded["unionid"].(string)
	openid, _ := decoded["openid"].(string)
	sessionKey, _ := decoded["session_key"].(string)
	return &WxAppSession{OpenID: openid, SessionKey: sessionKey, UnionID: unionID}, nil
}

// GetPhoneNumber 获取手机号（新版 POST code 接口）：POST /wxa/business/getuserphonenumber
func (s *WxAppService) GetPhoneNumber(ctx context.Context, code string) (string, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return "", err
	}
	decoded, err := s.invoke(ctx, epGetUserPhone, map[string]string{"access_token": token}, map[string]string{"code": code})
	if err != nil {
		return "", err
	}
	phoneInfo, _ := decoded["phone_info"].(map[string]any)
	phone, _ := phoneInfo["phoneNumber"].(string)
	if phone == "" {
		return "", &InfraError{Op: "decode", Err: errors.New("getPhoneNumber 响应缺少手机号")}
	}
	return phone, nil
}

// GetAccessToken 获取接口调用凭证：缓存命中直接返回；miss 时 redislock 并发去重
// （抢到锁者 double-check 后 fetch 回写；未抢到者自旋 5×200ms 读缓存，兜底再 fetch）
func (s *WxAppService) GetAccessToken(ctx context.Context) (string, error) {
	cacheKey := accessTokenKeyPrefix + s.AppID

	if cached, err := g.Redis().Get(ctx, cacheKey); err == nil && cached.String() != "" {
		return cached.String(), nil
	}

	lock := redislock.Create("wxapp:token:"+s.AppID, tokenLockTTL)
	for attempt := 0; attempt < 5; attempt++ {
		if lock.Get(ctx) {
			defer lock.Release(ctx)
			// double-check：排队期间他人可能已回写
			if cached, err := g.Redis().Get(ctx, cacheKey); err == nil && cached.String() != "" {
				return cached.String(), nil
			}
			return s.fetchAndCacheToken(ctx, cacheKey)
		}
		// 未抢到锁：等待持锁者回写后重读
		time.Sleep(200 * time.Millisecond)
		if cached, err := g.Redis().Get(ctx, cacheKey); err == nil && cached.String() != "" {
			return cached.String(), nil
		}
	}
	// 自旋上限后兜底直接 fetch（宁可重复 fetch 也不报错）
	return s.fetchAndCacheToken(ctx, cacheKey)
}

func (s *WxAppService) fetchAndCacheToken(ctx context.Context, cacheKey string) (string, error) {
	query := map[string]string{
		"grant_type": "client_credential",
		"appid":      s.AppID,
		"secret":     s.Secret,
	}
	decoded, err := s.invoke(ctx, epToken, query, nil)
	if err != nil {
		return "", err
	}
	token, _ := decoded["access_token"].(string)
	if token == "" {
		return "", &InfraError{Op: "token", Err: errors.New("getAccessToken 响应缺少 access_token")}
	}
	if _, err := g.Redis().Do(ctx, "SET", cacheKey, token, "EX", accessTokenTTL); err != nil {
		// 回写失败不影响本次返回（下次会再 fetch）
		// Redis adapter 错误可能携带含凭证的 SET 参数，只记录固定文本。
		g.Log().Warning(ctx, "wxapp access_token 缓存回写失败")
	}
	return token, nil
}

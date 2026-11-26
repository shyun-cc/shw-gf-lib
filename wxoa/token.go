package wxoa

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/shyun-cc/shw-gf-lib/redislock"

	"github.com/gogf/gf/v2/frame/g"
)

const (
	accessTokenKeyPrefix = "wxoa:access_token:"
	accessTokenTTL       = 7000 // 微信侧 7200s，提前 200s 刷新
	tokenLockTTL         = 10
)

// WxOAService 微信公众号服务（四包同 import 时以 WxOA 前缀与其他包类型可辨）
type WxOAService struct {
	AppID      string
	Secret     string
	HTTPClient *http.Client
	BaseURL    string
}

// NewWxOAService 构造服务
func NewWxOAService(appID, secret string) *WxOAService {
	return &WxOAService{AppID: appID, Secret: secret}
}

// GetAccessToken 获取接口调用凭证：缓存命中直接返回；miss 时 redislock 并发去重
// （抢到锁者 double-check 后 fetch 回写；未抢到者自旋 5×200ms 读缓存，兜底再 fetch）
func (s *WxOAService) GetAccessToken(ctx context.Context) (string, error) {
	cacheKey := accessTokenKeyPrefix + s.AppID

	if cached, err := g.Redis().Get(ctx, cacheKey); err == nil && cached.String() != "" {
		return cached.String(), nil
	}

	lock := redislock.Create("wxoa:token:"+s.AppID, tokenLockTTL)
	for attempt := 0; attempt < 5; attempt++ {
		if lock.Get(ctx) {
			defer lock.Release(ctx)
			// double-check：排队期间他人可能已回写
			if cached, err := g.Redis().Get(ctx, cacheKey); err == nil && cached.String() != "" {
				return cached.String(), nil
			}
			return s.fetchAndCacheToken(ctx, cacheKey)
		}
		time.Sleep(200 * time.Millisecond)
		if cached, err := g.Redis().Get(ctx, cacheKey); err == nil && cached.String() != "" {
			return cached.String(), nil
		}
	}
	// 自旋上限后兜底直接 fetch（宁可重复 fetch 也不报错）
	return s.fetchAndCacheToken(ctx, cacheKey)
}

func (s *WxOAService) fetchAndCacheToken(ctx context.Context, cacheKey string) (string, error) {
	decoded, err := s.invoke(ctx, epToken, map[string]string{
		"grant_type": "client_credential",
		"appid":      s.AppID,
		"secret":     s.Secret,
	}, nil)
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
		g.Log().Warning(ctx, "wxoa access_token 缓存回写失败")
	}
	return token, nil
}

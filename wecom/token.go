package wecom

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/shyun-cc/shw-gf-lib/redislock"

	"github.com/gogf/gf/v2/frame/g"
)

const tokenCacheTTL = 7000 // 微信侧 7200s，提前 200s 刷新

// TokenManager access_token 获取：Redis 缓存 TTL 7000s + redislock 并发去重 + double-check + 自旋
type TokenManager struct {
	creds      Credential
	baseURL    string // 默认 https://qyapi.weixin.qq.com；httptest 注入用
	httpClient *http.Client
}

// NewTokenManager 构造
func NewTokenManager(creds Credential) *TokenManager {
	return &TokenManager{creds: creds}
}

// SetBaseURL 注入 API 基地址与 HTTP 客户端（测试取证用）
func (m *TokenManager) SetBaseURL(u string, client *http.Client) {
	m.baseURL = u
	m.httpClient = client
}

func (m *TokenManager) cacheKey() string {
	if m.creds.AppType == AppAgent {
		return fmt.Sprintf("wecom:%s:%d:access_token", m.creds.CorpID, m.creds.AgentID)
	}
	return fmt.Sprintf("wecom:%s:contact:access_token", m.creds.CorpID)
}

// Token 获取 access_token；forceRefresh=true 时忽略缓存强制刷新
func (m *TokenManager) Token(ctx context.Context, forceRefresh bool) (string, error) {
	key := m.cacheKey()
	if !forceRefresh {
		if cached, err := g.Redis().Get(ctx, key); err == nil && cached.String() != "" {
			return cached.String(), nil
		}
	}

	lock := redislock.Create("wecom:token:"+m.creds.CorpID, 10)
	for attempt := 0; attempt < 5; attempt++ {
		if lock.Get(ctx) {
			defer lock.Release(ctx)
			if !forceRefresh {
				if cached, err := g.Redis().Get(ctx, key); err == nil && cached.String() != "" {
					return cached.String(), nil
				}
			}
			return m.fetchAndCache(ctx, key)
		}
		time.Sleep(200 * time.Millisecond)
		if cached, err := g.Redis().Get(ctx, key); err == nil && cached.String() != "" {
			return cached.String(), nil
		}
	}
	return m.fetchAndCache(ctx, key)
}

// Invalidate 清缓存（errcode 42001/40014 后强制刷新用）
func (m *TokenManager) Invalidate(ctx context.Context) {
	_, _ = g.Redis().Del(ctx, m.cacheKey())
}

func (m *TokenManager) fetchAndCache(ctx context.Context, key string) (string, error) {
	// gettoken 接口（GET，无 access_token）
	base := m.baseURL
	if base == "" {
		base = defaultQyAPI
	}
	target := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s", base, m.creds.CorpID, m.creds.Secret)
	req, err := newJSONRequest(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", &InfraError{Op: "http", Err: err}
	}
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := doJSON(client, req)
	if err != nil {
		return "", err // doJSON 已类型化（InfraError: http/decode）
	}
	if code := errcodeOf(resp); code != 0 {
		errmsg, _ := resp["errmsg"].(string)
		return "", &BizError{Errcode: code, Errmsg: errmsg}
	}
	token, _ := resp["access_token"].(string)
	if token == "" {
		return "", &InfraError{Op: "token", Err: errors.New("gettoken 响应缺少 access_token")}
	}
	if _, err := g.Redis().Do(ctx, "SET", key, token, "EX", tokenCacheTTL); err != nil {
		// Redis adapter 错误可能携带含凭证的 SET 参数，只记录固定文本。
		g.Log().Warning(ctx, "wecom token 缓存回写失败")
	}
	return token, nil
}

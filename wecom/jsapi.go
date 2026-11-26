package wecom

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/shyun-cc/shw-gf-lib/redislock"

	"github.com/gogf/gf/v2/frame/g"
)

const jsapiTicketTTL = 7000 // 微信侧 7200s，提前 200s 刷新

// TicketManager jsapi_ticket 获取：Redis 缓存 TTL 7000s + redislock 并发去重 +
// double-check + 自旋（与 TokenManager 同构；forceRefresh 忽略缓存）。
// 端点 GET /cgi-bin/get_jsapi_ticket（挂起注记：限流解除后补官方页面取证）。
type TicketManager struct {
	creds      Credential
	baseURL    string
	httpClient *http.Client
}

// NewTicketManager 构造
func NewTicketManager(creds Credential) *TicketManager {
	return &TicketManager{creds: creds}
}

// SetBaseURL 注入 API 基地址与 HTTP 客户端（测试取证用）
func (m *TicketManager) SetBaseURL(u string, client *http.Client) {
	m.baseURL = u
	m.httpClient = client
}

func (m *TicketManager) cacheKey() string {
	if m.creds.AppType == AppAgent {
		return fmt.Sprintf("wecom:%s:%d:jsapi_ticket", m.creds.CorpID, m.creds.AgentID)
	}
	return fmt.Sprintf("wecom:%s:contact:jsapi_ticket", m.creds.CorpID)
}

// Ticket 获取 jsapi_ticket；forceRefresh=true 时忽略缓存强制刷新
func (m *TicketManager) Ticket(ctx context.Context, forceRefresh bool) (string, error) {
	key := m.cacheKey()
	if !forceRefresh {
		if cached, err := g.Redis().Get(ctx, key); err == nil && cached.String() != "" {
			return cached.String(), nil
		}
	}
	lock := redislock.Create("wecom:jsapi_ticket:"+m.creds.CorpID, 10)
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

func (m *TicketManager) fetchAndCache(ctx context.Context, key string) (string, error) {
	token, err := NewTokenManager(m.creds).Token(ctx, false)
	if err != nil {
		return "", &InfraError{Op: "token", Err: fmt.Errorf("获取 access_token 失败: %w", err)}
	}
	base := m.baseURL
	if base == "" {
		base = defaultQyAPI
	}
	target := base + "/cgi-bin/get_jsapi_ticket?access_token=" + token
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", &InfraError{Op: "http", Err: err}
	}
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := doJSON(client, req)
	if err != nil {
		return "", &InfraError{Op: "http", Err: fmt.Errorf("get_jsapi_ticket 请求失败: %w", err)}
	}
	if code := errcodeOf(resp); code != 0 {
		errmsg, _ := resp["errmsg"].(string)
		return "", &BizError{Errcode: code, Errmsg: errmsg}
	}
	ticket, _ := resp["ticket"].(string)
	if ticket == "" {
		return "", &InfraError{Op: "decode", Err: fmt.Errorf("get_jsapi_ticket 响应缺少 ticket")}
	}
	if _, err := g.Redis().Do(ctx, "SET", key, ticket, "EX", jsapiTicketTTL); err != nil {
		// Redis adapter 错误可能携带含凭证的 SET 参数，只记录固定文本。
		g.Log().Warning(ctx, "wecom jsapi_ticket 缓存回写失败")
	}
	return ticket, nil
}

// BuildJsApiConfig JS-SDK 签名：signature = SHA1("jsapi_ticket=…&noncestr=…&timestamp=…&url=…")。
// ticket 来自 TicketManager；url 为当前页面 URL（不含 # 及其后）。
func BuildJsApiConfig(ticket, nonceStr, timestamp, url string) map[string]string {
	raw := "jsapi_ticket=" + ticket + "&noncestr=" + nonceStr + "&timestamp=" + timestamp + "&url=" + url
	sum := sha1.Sum([]byte(raw))
	return map[string]string{
		"jsapi_ticket": ticket,
		"noncestr":     nonceStr,
		"timestamp":    timestamp,
		"url":          url,
		"signature":    hex.EncodeToString(sum[:]),
	}
}

// BuildQrConnectParams 扫码登录嵌入式模式参数集（app 内嵌二维码用，恰五键）
func BuildQrConnectParams(corpID string, agentID int, redirectURI, state, href string) map[string]string {
	return map[string]string{
		"appid":        corpID,
		"agentid":      fmt.Sprintf("%d", agentID),
		"redirect_uri": redirectURI,
		"state":        state,
		"href":         href,
	}
}

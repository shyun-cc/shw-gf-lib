package wxoa

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/shyun-cc/shw-gf-lib/redislock"
)

const jsapiTicketKeyPrefix = "wxoa:jsapi_ticket:"

var epJSAPITicket = Endpoint{"ticket.jsapi", http.MethodGet, "/cgi-bin/ticket/getticket", []string{"type"}, false}

// JSConfig 是可直接传给 wx.config 的签名字段，不包含服务端凭证。
type JSConfig struct {
	AppID     string `json:"appId"`
	Timestamp int64  `json:"timestamp"`
	NonceStr  string `json:"nonceStr"`
	Signature string `json:"signature"`
}

// GetJSAPITicket 获取公众号 JS-SDK ticket，按 AppID 在默认 Redis 分组缓存。
// 使用 redislock 去重，最多等待 12s；不在抢锁失败时直接请求微信。
// Redis 不可用返回 InfraError；写缓存失败仍返回本次 ticket。
func (s *WxOAService) GetJSAPITicket(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	key := jsapiTicketKeyPrefix + s.AppID
	lock := redislock.Create("wxoa:jsapi_ticket:"+s.AppID, 10)
	for {
		if err := ctx.Err(); err != nil {
			return "", &InfraError{Op: "ticket", Err: err}
		}
		cached, err := g.Redis().Get(ctx, key)
		if err != nil {
			return "", &InfraError{Op: "ticket", Err: errors.New("jsapi_ticket 缓存读取失败")}
		}
		if cached.String() != "" {
			return cached.String(), nil
		}
		if lock.Get(ctx) {
			// 请求使用 8s 上限，短于锁租期；释放使用独立、有界 context。
			defer func() {
				releaseCtx, done := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
				defer done()
				lock.Release(releaseCtx)
			}()
			cached, err = g.Redis().Get(ctx, key)
			if err != nil {
				return "", &InfraError{Op: "ticket", Err: errors.New("jsapi_ticket 缓存读取失败")}
			}
			if cached.String() != "" {
				return cached.String(), nil
			}
			fetchCtx, done := context.WithTimeout(ctx, 8*time.Second)
			defer done()
			return s.fetchJSAPITicket(fetchCtx, key)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", &InfraError{Op: "ticket", Err: ctx.Err()}
		case <-timer.C:
		}
	}
}

func (s *WxOAService) fetchJSAPITicket(ctx context.Context, key string) (string, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return "", safeJSError(err, s.Secret)
	}
	base := s.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, epJSAPITicket.Method, base+epJSAPITicket.Path+"?"+encodeQuery(map[string]string{"access_token": token, "type": "jsapi"}), nil)
	if err != nil {
		return "", &InfraError{Op: "http", Err: errors.New("jsapi_ticket 请求构造失败")}
	}
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", safeJSError(&InfraError{Op: "http", Err: err}, token, s.Secret)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &InfraError{Op: "http", Err: fmt.Errorf("jsapi_ticket HTTP 状态 %d", resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", safeJSError(&InfraError{Op: "http", Err: err}, token, s.Secret)
	}
	var result struct {
		Errcode   int    `json:"errcode"`
		Errmsg    string `json:"errmsg"`
		Ticket    string `json:"ticket"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", &InfraError{Op: "decode", Err: errors.New("jsapi_ticket 响应非有效 JSON")}
	}
	if result.Errcode != 0 {
		return "", safeJSError(&BizError{Errcode: result.Errcode, Errmsg: result.Errmsg}, token, s.Secret, result.Ticket)
	}
	if result.Ticket == "" || result.ExpiresIn <= 0 {
		return "", &InfraError{Op: "ticket", Err: errors.New("jsapi_ticket 响应缺少 ticket 或有效 expires_in")}
	}
	// 按服务端有效期提前刷新：至多 200s，短有效期提前约 10%（至少 1s）。
	margin := min(int64(200), max(int64(1), result.ExpiresIn/10))
	ttl := min(int64(7000), result.ExpiresIn-margin)
	if ttl > 0 {
		if _, err := g.Redis().Do(ctx, "SET", key, result.Ticket, "EX", ttl); err != nil {
			g.Log().Warning(ctx, "wxoa jsapi_ticket 缓存回写失败")
		}
	}
	return result.Ticket, nil
}

// safeJSError 防止 net/url 错误或上游消息把 secret/access_token 带给调用方。
func safeJSError(err error, secrets ...string) error {
	var biz *BizError
	if errors.As(err, &biz) {
		message := biz.Errmsg
		for _, secret := range secrets {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "[REDACTED]")
			}
		}
		return &BizError{Errcode: biz.Errcode, Errmsg: message}
	}
	op := "ticket"
	var infra *InfraError
	if errors.As(err, &infra) {
		op = infra.Op
	}
	if errors.Is(err, context.Canceled) {
		return &InfraError{Op: op, Err: context.Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &InfraError{Op: op, Err: context.DeadlineExceeded}
	}
	return &InfraError{Op: op, Err: errors.New("JS-SDK 凭证请求失败")}
}

// SignJSURL 为完整 HTTP(S) 网页 URL 生成 JS-SDK 配置；去除 # 及后续部分。
// 校验 URL 后直接签原字符串，不重排、解码或重新编码查询参数，不访问该 URL。
// 调用方负责签名域名许可、鉴权及浏览器实际 URL 的选择。
func (s *WxOAService) SignJSURL(ctx context.Context, rawURL string) (*JSConfig, error) {
	pageURL, _, _ := strings.Cut(rawURL, "#")
	parsed, err := url.Parse(pageURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || strings.ContainsAny(pageURL, "\r\n\t ") {
		return nil, &InfraError{Op: "sign", Err: errors.New("签名 URL 必须是完整 HTTP(S) 网页地址")}
	}
	ticket, err := s.GetJSAPITicket(ctx)
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, &InfraError{Op: "sign", Err: errors.New("签名随机串生成失败")}
	}
	config := &JSConfig{AppID: s.AppID, Timestamp: time.Now().Unix(), NonceStr: hex.EncodeToString(nonce[:])}
	config.Signature = signJSURL(ticket, config.NonceStr, config.Timestamp, pageURL)
	return config, nil
}

func signJSURL(ticket, nonce string, timestamp int64, pageURL string) string {
	data := "jsapi_ticket=" + ticket + "&noncestr=" + nonce + "&timestamp=" + strconv.FormatInt(timestamp, 10) + "&url=" + pageURL
	sum := sha1.Sum([]byte(data)) // 微信 JS-SDK 协议指定 SHA-1。
	return hex.EncodeToString(sum[:])
}

package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AppType 双应用类型：通讯录同步应用（仅 corpID）/ 自建应用（corpID+agentID+secret）
type AppType int

const (
	AppContact AppType = iota // 通讯录同步应用
	AppAgent                  // 自建应用
)

// Credential 应用凭证
type Credential struct {
	CorpID  string
	AgentID int    // 自建应用必填
	Secret  string // 自建应用必填；通讯录应用用通讯录 secret
	AppType AppType
}

// Client 企微 API 客户端。HTTPClient/BaseURL/Now/Sleep 可注入。
type Client struct {
	Creds      Credential
	HTTPClient *http.Client
	BaseURL    string // 默认 https://qyapi.weixin.qq.com

	tokenMgr *TokenManager
}

const defaultQyAPI = "https://qyapi.weixin.qq.com"

// NewClient 构造客户端（自带 TokenManager，Redis 缓存）
func NewClient(creds Credential) *Client {
	c := &Client{Creds: creds}
	c.tokenMgr = NewTokenManager(creds)
	return c
}

// SetBaseURL 注入 API 基地址（测试取证用；同步注入 token manager）
func (c *Client) SetBaseURL(u string) {
	c.BaseURL = u
	if c.tokenMgr != nil {
		c.tokenMgr.SetBaseURL(u, c.HTTPClient)
	}
}

// errcode 判定：42001/40014 token 失效须刷新重试
func isTokenExpired(errcode int) bool {
	return errcode == 42001 || errcode == 40014
}

// Get GET 请求（自动带 access_token；token 失效恰一次刷新重试）
func (c *Client) Get(ctx context.Context, path string, query map[string]string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, path, query, nil)
}

// Post POST JSON 请求（自动带 access_token；token 失效恰一次刷新重试）
func (c *Client) Post(ctx context.Context, path string, body any, query map[string]string) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, &InfraError{Op: "encode", Err: fmt.Errorf("请求体编码失败: %w", err)}
	}
	return c.do(ctx, http.MethodPost, path, query, raw)
}

// GetUserinfoByCode OAuth code 换成员身份信息（userid / user_ticket）
func (c *Client) GetUserinfoByCode(ctx context.Context, code string) (map[string]any, error) {
	return c.invoke(ctx, epAuthGetUserinfo, map[string]string{"code": code}, nil)
}

// GetUserDetailByTicket user_ticket 换成员敏感信息（手机号、真实姓名等）
func (c *Client) GetUserDetailByTicket(ctx context.Context, userTicket string) (map[string]any, error) {
	return c.invoke(ctx, epAuthGetUserDetail, nil, map[string]string{"user_ticket": userTicket})
}

// do 统一请求：首查缓存 token；errcode 42001/40014 时强制刷新后恰重试一次
func (c *Client) do(ctx context.Context, method, path string, query map[string]string, body []byte) (map[string]any, error) {
	return c.doCT(ctx, method, path, query, body, "application/json")
}

// doCT 带自定义 Content-Type 的统一请求（multipart 上传等非 JSON body 复用同一重试链）
func (c *Client) doCT(ctx context.Context, method, path string, query map[string]string, body []byte, contentType string) (map[string]any, error) {
	resp, err := c.doOnce(ctx, method, path, query, body, contentType, false)
	if err != nil {
		return nil, err
	}
	if code := errcodeOf(resp); isTokenExpired(code) {
		// 强制刷新（清缓存）后重试一次
		c.tokenMgr.Invalidate(ctx)
		resp, err = c.doOnce(ctx, method, path, query, body, contentType, true)
		if err != nil {
			return nil, err
		}
	}
	if code := errcodeOf(resp); code != 0 {
		errmsg, _ := resp["errmsg"].(string)
		return resp, &BizError{Errcode: code, Errmsg: errmsg}
	}
	return resp, nil
}

func (c *Client) doOnce(ctx context.Context, method, path string, query map[string]string, body []byte, contentType string, forceRefresh bool) (map[string]any, error) {
	token, err := c.tokenMgr.Token(ctx, forceRefresh)
	if err != nil {
		return nil, &InfraError{Op: "token", Err: fmt.Errorf("获取 access_token 失败: %w", err)}
	}

	base := c.BaseURL
	if base == "" {
		base = defaultQyAPI
	}
	target := base + path + "?access_token=" + token
	for k, v := range query {
		target += "&" + k + "=" + v
	}

	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	if body != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	httpResp, err := client.Do(req)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, &InfraError{Op: "decode", Err: fmt.Errorf("响应非 JSON: %w", err)}
	}
	return decoded, nil
}

func errcodeOf(resp map[string]any) int {
	if code, ok := resp["errcode"].(float64); ok {
		return int(code)
	}
	return 0
}

// Now/Sleep 占位（保持与其它 client 一致的注入位）
var _ = time.Now

// newJSONRequest / httpDefaultDo：token.go 用的轻量 HTTP 辅助（带 BaseURL 注入不支持——
// gettoken 走默认域名；测试经 TokenManager 的注入点覆盖）
func newJSONRequest(ctx context.Context, method, target string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func doJSON(client *http.Client, req *http.Request) (map[string]any, error) {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, &InfraError{Op: "decode", Err: fmt.Errorf("响应非 JSON: %w", err)}
	}
	return decoded, nil
}

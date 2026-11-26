// Package kd100 快递100 开放平台自主复刻客户端（对应 PHP Kd100Client），零厂商 SDK。
//
// 双签名规则：
//   - 查询/订阅类：sign = UPPER(MD5(paramJSON + key + customer))
//   - 下单类：    sign = UPPER(MD5(paramJSON + t + key + secret))，t 为秒级时间戳
//
// 表单 POST（application/x-www-form-urlencoded）：
//   - 查询类 fields：customer / sign / param
//   - 下单类 fields：key / sign / t / param
//
// 重试：HTTP 异常或响应非合法 JSON 时重试至多 3 次（含首次）、间隔 2s（可注入）；
// 合法 JSON 但业务 result=false 不重试、原样返回由上层判断。
package kd100

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shyun-cc/shw-gf-lib/express"
)

const (
	baseURL       = "https://poll.kuaidi100.com"
	maxRetries    = 3
	retryInterval = 2 * time.Second
)

// Client 快递100 客户端。HTTPClient/BaseURL/Now/Sleep 均可注入（测试取证用）。
type Client struct {
	Key        string // 企业授权码
	Secret     string // 后台密钥（下单类签名）
	Customer   string // 客户 ID（查询类签名）
	HTTPClient *http.Client
	BaseURL    string              // 默认 https://poll.kuaidi100.com
	Now        func() time.Time    // 时间戳注入
	Sleep      func(time.Duration) // 重试间隔注入（默认 time.Sleep）
}

// 编译期锁定 express.Provider 9 方法接口
var _ express.Provider = (*Client)(nil)

// New 构造客户端
func New(key, secret, customer string) *Client {
	return &Client{Key: key, Secret: secret, Customer: customer}
}

// ---- 追踪域 ----

// Track 实时查询物流轨迹
func (c *Client) Track(ctx context.Context, com, num string) (map[string]any, error) {
	return c.pollRequest(ctx, "/poll/query.do", map[string]any{
		"com":      com,
		"num":      num,
		"resultv2": "4",
	})
}

// Subscribe 订阅物流推送
func (c *Client) Subscribe(ctx context.Context, param map[string]any) (map[string]any, error) {
	return c.pollRequest(ctx, "/poll", param)
}

// HandleTrackingCallback 物流回调验签（查询类签名规则）
func (c *Client) HandleTrackingCallback(param, sign string) error {
	return c.verifyCallback(param, sign, "poll", "")
}

// ---- 下单域 ----

// CreateOrder 寄件下单
func (c *Client) CreateOrder(ctx context.Context, param map[string]any) (map[string]any, error) {
	return c.orderRequest(ctx, "/order/borderapi.do", param)
}

// CancelOrder 取消下单
func (c *Client) CancelOrder(ctx context.Context, param map[string]any) (map[string]any, error) {
	return c.orderRequest(ctx, "/order/cancelorder.do", param)
}

// ModifyOrder 修改订单
func (c *Client) ModifyOrder(ctx context.Context, param map[string]any) (map[string]any, error) {
	return c.orderRequest(ctx, "/order/modifyorder.do", param)
}

// GetOrderDetail 查询订单详情
func (c *Client) GetOrderDetail(ctx context.Context, param map[string]any) (map[string]any, error) {
	return c.orderRequest(ctx, "/order/orderinfo.do", param)
}

// QueryPrice 价格查询
func (c *Client) QueryPrice(ctx context.Context, param map[string]any) (map[string]any, error) {
	return c.orderRequest(ctx, "/order/price.do", param)
}

// HandleOrderCallback 订单回调验签（下单类签名规则）
func (c *Client) HandleOrderCallback(param, sign, t string) error {
	return c.verifyCallback(param, sign, "order", t)
}

// ---- 内部：签名 ----

// signPoll 查询/订阅类签名：UPPER(MD5(paramJSON + key + customer))
func (c *Client) signPoll(paramJSON string) string {
	return strings.ToUpper(md5hex(paramJSON + c.Key + c.Customer))
}

// signOrder 下单类签名：UPPER(MD5(paramJSON + t + key + secret))
func (c *Client) signOrder(paramJSON, t string) string {
	return strings.ToUpper(md5hex(paramJSON + t + c.Key + c.Secret))
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// ---- 内部：请求 ----

func (c *Client) pollRequest(ctx context.Context, path string, param map[string]any) (map[string]any, error) {
	paramJSON, err := json.Marshal(param)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"customer": {c.Customer},
		"sign":     {c.signPoll(string(paramJSON))},
		"param":    {string(paramJSON)},
	}
	return c.sendWithRetry(ctx, path, form)
}

func (c *Client) orderRequest(ctx context.Context, path string, param map[string]any) (map[string]any, error) {
	paramJSON, err := json.Marshal(param)
	if err != nil {
		return nil, err
	}
	t := c.timestamp()
	form := url.Values{
		"key":   {c.Key},
		"sign":  {c.signOrder(string(paramJSON), t)},
		"t":     {t},
		"param": {string(paramJSON)},
	}
	return c.sendWithRetry(ctx, path, form)
}

// sendWithRetry 带重试的表单 POST：HTTP 异常或非合法 JSON 重试至多 3 次；
// 合法 JSON 立即返回（result=false 交上层判断）
func (c *Client) sendWithRetry(ctx context.Context, path string, form url.Values) (map[string]any, error) {
	base := c.BaseURL
	if base == "" {
		base = baseURL
	}
	target := base + path
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	var lastErr string
	for attempt := 1; attempt <= maxRetries; attempt++ {
		payload, err := c.postOnce(ctx, httpClient, target, form)
		if err == nil {
			return payload, nil
		}
		lastErr = err.Error()

		if attempt < maxRetries {
			sleep(retryInterval)
		}
	}
	return nil, fmt.Errorf("快递100 API 请求失败（%s 尝试 %d 次后仍失败）: %s", path, maxRetries, lastErr)
}

func (c *Client) postOnce(ctx context.Context, httpClient *http.Client, target string, form url.Values) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求异常: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("响应非合法 JSON: %.200s", string(body))
	}
	return payload, nil
}

// verifyCallback 回调验签：重算签名并恒时比较
func (c *Client) verifyCallback(param, sign, mode, t string) error {
	var expected string
	if mode == "order" {
		if t == "" {
			t = strconv.FormatInt(time.Now().Unix(), 10)
		}
		expected = c.signOrder(param, t)
	} else {
		expected = c.signPoll(param)
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(strings.ToUpper(sign))) != 1 {
		return fmt.Errorf("快递100 回调签名验证失败")
	}
	return nil
}

func (c *Client) timestamp() string {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	return strconv.FormatInt(now.Unix(), 10)
}

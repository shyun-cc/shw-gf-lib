// Package tencent 腾讯地图 WebService API 自主复刻客户端，零厂商 SDK。
//
// SK 签名两步分离（实测语义）：
//  1. 签名用原始未编码值——参数按名升序、k=v 原始值拼 querystring、
//     签名串 = path?querystring + sk、md5 转小写；
//  2. HTTP 请求时参数正常 urlencode 编码发送，sig 追加到 query。
//
// 成功判定 status == 0（数值全等）；status ≠ 0、HTTP 异常或响应非 JSON
// 时重试至多 3 次（间隔可注入）；全部失败返回明确错误。
package tencent

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://apis.map.qq.com"
	maxRetries     = 3
	statusOK       = 0
)

// Client 腾讯地图客户端。HTTPClient/BaseURL/Sleep 可注入（测试取证用）。
type Client struct {
	Key        string
	SK         string // 为空则不签名（兼容无 SK 的 key）
	HTTPClient *http.Client
	BaseURL    string
	Sleep      func(time.Duration) // 重试间隔注入（默认 time.Sleep）
}

// New 构造客户端
func New(key, sk string) *Client {
	return &Client{Key: key, SK: sk}
}

// Get 发起 GET 请求：自动补 key、SK 非空时追加签名 sig；
// status==0 返回完整响应 map（含 status/message/result 原始字段）。
func (c *Client) Get(ctx context.Context, path string, query map[string]string) (map[string]any, error) {
	if query == nil {
		query = make(map[string]string)
	}
	if _, ok := query["key"]; !ok {
		query["key"] = c.Key
	}
	if c.SK != "" {
		query["sig"] = c.sign(path, query)
	}

	base := c.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	target := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
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
		payload, err := c.getOnce(ctx, httpClient, target, query)
		if err == nil {
			return payload, nil
		}
		lastErr = err.Error()
		if attempt < maxRetries {
			sleep(2 * time.Second)
		}
	}
	return nil, fmt.Errorf("腾讯地图 API 请求失败（%s 尝试 %d 次后仍失败）: %s", path, maxRetries, lastErr)
}

func (c *Client) getOnce(ctx context.Context, httpClient *http.Client, target string, query map[string]string) (map[string]any, error) {
	q := url.Values{}
	for k, v := range query {
		q.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求异常: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("响应非合法 JSON: %.200s", string(body))
	}
	status, ok := payload["status"].(float64)
	if !ok || int(status) != statusOK {
		return nil, fmt.Errorf("腾讯 API 返回错误 status=%v message=%v", payload["status"], payload["message"])
	}
	return payload, nil
}

// sign SK 签名：原始未编码值拼串（参数升序），md5 小写。
// 签名计算与请求编码是两步分离的——签名用原始值，发送才 urlencode。
func (c *Client) sign(path string, query map[string]string) string {
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+query[k])
	}
	raw := path + "?" + strings.Join(pairs, "&") + c.SK
	h := md5.Sum([]byte(raw))
	return hex.EncodeToString(h[:])
}

// FormatStatus 调试辅助：status 字段的字符串形态
func FormatStatus(payload map[string]any) string {
	if v, ok := payload["status"].(float64); ok {
		return strconv.Itoa(int(v))
	}
	return "unknown"
}

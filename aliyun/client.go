// Package aliyun 阿里云 OpenAPI 自主复刻客户端（V3 签名），零厂商 SDK 依赖。
//
// ACS3-HMAC-SHA256 签名与 hyperf-lib PHP 侧逐字段对齐：
//
//	CanonicalRequest = METHOD \n CanonicalURI \n CanonicalQueryString \n
//	                   CanonicalHeaders \n (空行) \n SignedHeaders \n HashedPayload
//	StringToSign     = "ACS3-HMAC-SHA256\n" + hex(sha256(CanonicalRequest))
//	Signature        = hex(hmacSha256(AccessKeySecret, StringToSign))
//	Authorization    = ACS3-HMAC-SHA256 Credential={AK},SignedHeaders={hdrs},Signature={sig}
//
// 双链差异（对齐 PHP AliYunSmsService/AliYunStsService）：
//   - Sms 链（dysmsapi，RPC 风格）：POST 业务参数在 URL query、body 空、成功判定宽容（Code 空或 "OK"）
//   - Sts 链（sts.aliyuncs.com，ROA 风格）：POST 参数在 JSON body、成功判定严格（响应不得带 Code）
package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Credentials 静态 AK 凭证（STS 临时凭证见 Sts.AssumeRole 返回的 StsCredentials）
type Credentials struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string // 可空；非空时注入 x-acs-security-token 头
	RegionID        string // 默认 cn-hangzhou
}

// Client 阿里云 OpenAPI 客户端。HTTPClient/BaseURL/Now/Nonce 均可注入（测试取证用）
type Client struct {
	Creds Credentials

	HTTPClient    *http.Client
	BaseURL       string           // 默认 https://{host}；httptest 时整包覆盖
	Now           func() time.Time // 默认 time.Now（UTC 化在内部完成）
	Nonce         func() string    // 默认 32 位 hex 随机
	nowOverride   string           // 测试注入固定 x-acs-date（优先于 Now）
	nonceOverride string           // 测试注入固定 nonce
}

// NewClient 构造默认客户端
func NewClient(creds Credentials) *Client {
	return &Client{Creds: creds}
}

// SetFixedDateNonce 注入固定 date 与 nonce（签名向量取证用；生产勿用）
func (c *Client) SetFixedDateNonce(date, nonce string) {
	c.nowOverride = date
	c.nonceOverride = nonce
}

// Request 一次 OpenAPI 请求描述
type Request struct {
	Method  string     // GET/POST
	Host    string     // API 域名，如 dysmsapi.aliyuncs.com
	Path    string     // 如 "/"；空视为 "/"
	Action  string     // x-acs-action
	Version string     // x-acs-version
	Query   url.Values // URL query（Sms 链业务参数在此）
	Body    []byte     // JSON body（Sts 链业务参数在此）；nil 表示空 body
}

// Response 统一响应（对齐 PHP AliYunResponseStruct）
type Response struct {
	Success      bool
	StatusCode   int
	RequestID    string
	Body         map[string]any
	ErrorCode    string
	ErrorMessage string
}

// Do 签名并发送请求，解析 JSON 响应。
// 成功判定：HTTP 2xx 且 ErrorCode 为空或 "OK"（RPC 链宽容语义；Sts 严格判定由调用方再校验 ErrorCode 必须为空）
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	method := strings.ToUpper(req.Method)
	path := req.Path
	if path == "" {
		path = "/"
	}
	body := req.Body
	hashedPayload := sha256.Sum256(nilOrEmpty(body))

	date := c.dateStr()
	nonce := c.nonceStr()

	headers := map[string]string{
		"host":                  req.Host,
		"x-acs-action":          req.Action,
		"x-acs-version":         req.Version,
		"x-acs-date":            date,
		"x-acs-signature-nonce": nonce,
		"x-acs-content-sha256":  hex.EncodeToString(hashedPayload[:]),
	}
	if c.Creds.SecurityToken != "" {
		headers["x-acs-security-token"] = c.Creds.SecurityToken
	}
	if body != nil {
		headers["content-type"] = "application/json"
	}

	canonicalRequest := buildCanonicalRequest(method, path, req.Query, headers)
	stringToSign := "ACS3-HMAC-SHA256\n" + hex.EncodeToString(sha256Sum([]byte(canonicalRequest)))
	signature := hex.EncodeToString(hmacSha256([]byte(c.Creds.AccessKeySecret), []byte(stringToSign)))
	signedHeaders := signedHeaderList(headers)
	authorization := fmt.Sprintf("ACS3-HMAC-SHA256 Credential=%s,SignedHeaders=%s,Signature=%s",
		c.Creds.AccessKeyID, signedHeaders, signature)

	// 组装实际 HTTP 请求
	base := c.BaseURL
	if base == "" {
		base = "https://" + req.Host
	}
	u := base + path
	if len(req.Query) > 0 {
		u += "?" + req.Query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, u, bodyReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	httpReq.Header.Set("Authorization", authorization)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("阿里云API请求失败: %w", err)
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	resp := &Response{StatusCode: httpResp.StatusCode}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &resp.Body)
	}
	if resp.Body != nil {
		resp.RequestID, _ = resp.Body["RequestId"].(string)
		resp.ErrorCode, _ = resp.Body["Code"].(string)
		resp.ErrorMessage, _ = resp.Body["Message"].(string)
	}
	resp.Success = httpResp.StatusCode >= 200 && httpResp.StatusCode < 300 &&
		(resp.ErrorCode == "" || resp.ErrorCode == "OK")
	return resp, nil
}

// buildCanonicalRequest 七段拼装（与 PHP buildCanonical* 逐字段对齐）
func buildCanonicalRequest(method, path string, query url.Values, headers map[string]string) string {
	var b strings.Builder
	b.WriteString(method)
	b.WriteString("\n")
	b.WriteString(path)
	b.WriteString("\n")
	b.WriteString(canonicalQueryString(query))
	b.WriteString("\n")
	b.WriteString(canonicalHeaders(headers))
	b.WriteString("\n") // CanonicalHeaders 各行自带 \n，此为规范要求的空行
	b.WriteString(signedHeaderList(headers))
	b.WriteString("\n")
	b.WriteString(headers["x-acs-content-sha256"])
	return b.String()
}

// canonicalQueryString key 字典序，k=v 均 rawurl 编码（对齐 PHP rawurlencode）
func canonicalQueryString(query url.Values) string {
	if len(query) == 0 {
		return ""
	}
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range query[k] {
			parts = append(parts, urlEsc(k)+"="+urlEsc(v))
		}
	}
	return strings.Join(parts, "&")
}

// canonicalHeaders 参与签名的头（host/content-type/x-acs-*）小写 key:value\n 逐行，字典序
func canonicalHeaders(headers map[string]string) string {
	keys := signedHeaderKeys(headers)
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(":")
		b.WriteString(strings.TrimSpace(headers[k]))
		b.WriteString("\n")
	}
	return b.String()
}

// signedHeaderList 参与签名的头名分号列表（与 headers 收集顺序无关，已排序）
func signedHeaderList(headers map[string]string) string {
	keys := signedHeaderKeys(headers)
	sort.Strings(keys)
	return strings.Join(keys, ";")
}

func signedHeaderKeys(headers map[string]string) []string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-acs-") || lk == "host" || lk == "content-type" {
			keys = append(keys, lk)
		}
	}
	return keys
}

// urlEsc 对齐 PHP rawurlencode（Go url.QueryEscape 把空格编为 +，须替换为 %20，
// 且 PHP 不转义 ~，Go 转义 ~ 需还原）
func urlEsc(s string) string {
	escaped := url.QueryEscape(s)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

func hmacSha256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func nilOrEmpty(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

func bodyReader(b []byte) io.Reader {
	if b == nil {
		return nil
	}
	return strings.NewReader(string(b))
}

func (c *Client) dateStr() string {
	if c.nowOverride != "" {
		return c.nowOverride
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	return now.UTC().Format("2006-01-02T15:04:05Z")
}

func (c *Client) nonceStr() string {
	if c.nonceOverride != "" {
		return c.nonceOverride
	}
	if c.Nonce != nil {
		return c.Nonce()
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

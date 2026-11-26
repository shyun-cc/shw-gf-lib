// Package aliyun 测试：ACS3-HMAC-SHA256 签名固定向量 + 双链差异（httptest 捕获断言）。
package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// 固定向量：测试内独立重算 Authorization 与客户端输出比对
const (
	fixedAK    = "testAccessKeyId"
	fixedSK    = "testAccessKeySecret"
	fixedDate  = "2026-08-18T12:00:00Z"
	fixedNonce = "0123456789abcdef0123456789abcdef"
)

// independentAuthorization 测试内独立实现的 ACS3 签名（有意不复用被测代码路径）。
// query 值要求已 rawurl 编码；headers 为小写全量签名头。
func independentAuthorization(method, path string, query []string, headers map[string]string) string {
	sorted := append([]string(nil), query...)
	sort.Strings(sorted)
	cq := strings.Join(sorted, "&")

	hkeys := make([]string, 0, len(headers))
	for k := range headers {
		hkeys = append(hkeys, k)
	}
	sort.Strings(hkeys)
	var ch strings.Builder
	var shParts []string
	for _, k := range hkeys {
		ch.WriteString(k)
		ch.WriteString(":")
		ch.WriteString(headers[k])
		ch.WriteString("\n")
		shParts = append(shParts, k)
	}
	sh := strings.Join(shParts, ";")

	cr := method + "\n" + path + "\n" + cq + "\n" + ch.String() + "\n" + sh + "\n" + headers["x-acs-content-sha256"]
	crHash := sha256.Sum256([]byte(cr))
	sts := "ACS3-HMAC-SHA256\n" + hex.EncodeToString(crHash[:])
	mac := hmac.New(sha256.New, []byte(fixedSK))
	mac.Write([]byte(sts))
	sig := hex.EncodeToString(mac.Sum(nil))
	return "ACS3-HMAC-SHA256 Credential=" + fixedAK + ",SignedHeaders=" + sh + ",Signature=" + sig
}

// captured 捕获到的一次请求
type captured struct {
	method string
	path   string
	host   string // r.Host（Go 服务端 Header 里不含 Host）
	query  url.Values
	body   string
	header http.Header
}

// captureServer 起一个 httptest server，捕获请求并按需要返回响应
func captureServer(t *testing.T, cap **captured, status int, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*cap = &captured{method: r.Method, path: r.URL.Path, host: r.Host, query: r.URL.Query(), body: string(body), header: r.Header.Clone()}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fixedClient(base string) *Client {
	c := NewClient(Credentials{AccessKeyID: fixedAK, AccessKeySecret: fixedSK, RegionID: "cn-hangzhou"})
	c.BaseURL = base
	c.SetFixedDateNonce(fixedDate, fixedNonce)
	return c
}

// TestSignature_IndependentRecompute SendSms 请求的 Authorization 可由测试内独立重算验证
func TestSignature_IndependentRecompute(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 200, `{"Code":"OK","BizId":"b1","RequestId":"r1"}`)

	sms := NewSms(fixedClient(srv.URL))
	if _, err := sms.SendSms(context.Background(), "10800000000", "签名A", "T001", map[string]any{"code": "1234"}); err != nil {
		t.Fatalf("SendSms 失败: %v", err)
	}
	if cap == nil {
		t.Fatal("未捕获请求")
	}

	// 从捕获请求重建 canonical 输入（注意：Go 服务端 r.Header 不含 Host，取 r.Host）
	var queryParts []string
	for k, vs := range cap.query {
		for _, v := range vs {
			queryParts = append(queryParts, urlEsc(k)+"="+urlEsc(v))
		}
	}
	headers := map[string]string{
		"host":                  "dysmsapi.aliyuncs.com", // 客户端签名输入（wire Host 因 httptest 转发不同，签名按 PHP 语义用 API 域名）,
		"x-acs-action":          cap.header.Get("X-Acs-Action"),
		"x-acs-version":         cap.header.Get("X-Acs-Version"),
		"x-acs-date":            cap.header.Get("X-Acs-Date"),
		"x-acs-signature-nonce": cap.header.Get("X-Acs-Signature-Nonce"),
		"x-acs-content-sha256":  cap.header.Get("X-Acs-Content-Sha256"),
	}
	want := independentAuthorization("POST", "/", queryParts, headers)
	if got := cap.header.Get("Authorization"); got != want {
		t.Fatalf("Authorization 不符:\n got=%s\nwant=%s", got, want)
	}
}

// TestSmsChain_QueryNotBody Sms 链：POST 业务参数在 query、body 为空、Code=OK 成功
func TestSmsChain_QueryNotBody(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 200, `{"Code":"OK","BizId":"b123","RequestId":"r9"}`)

	sms := NewSms(fixedClient(srv.URL))
	res, err := sms.SendSms(context.Background(), "10811112222", "测试签名", "SMS_001", nil)
	if err != nil {
		t.Fatalf("SendSms 失败: %v", err)
	}
	if !res.Success || res.MessageID != "b123" {
		t.Fatalf("应成功且 MessageID=b123: %+v", res)
	}
	if cap.method != "POST" {
		t.Fatalf("应 POST，实际 %s", cap.method)
	}
	if cap.body != "" {
		t.Fatalf("Sms 链 body 应为空，实际 %q", cap.body)
	}
	if cap.query.Get("PhoneNumbers") != "10811112222" || cap.query.Get("TemplateCode") != "SMS_001" || cap.query.Get("SignName") != "测试签名" {
		t.Fatalf("query 业务参数不符: %v", cap.query)
	}
	if cap.query.Get("TemplateParam") != "" {
		t.Fatal("nil 模板参数不应传 TemplateParam")
	}
}

// TestSmsChain_BusinessErrorCodeFail 业务错误码判失败（宽容判定的边界）
func TestSmsChain_BusinessErrorCodeFail(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 200, `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"触发分钟级流控","RequestId":"r2"}`)

	sms := NewSms(fixedClient(srv.URL))
	res, err := sms.SendSms(context.Background(), "10800000000", "s", "T", nil)
	if err != nil {
		t.Fatalf("HTTP 层不应报错: %v", err)
	}
	if res.Success {
		t.Fatal("业务错误码应判失败")
	}
	if res.ErrorCode != "isv.BUSINESS_LIMIT_CONTROL" || res.ErrorMessage != "触发分钟级流控" {
		t.Fatalf("错误信息不符: %+v", res)
	}
}

// TestStsChain_BodyNotQuery Sts 链：参数在 JSON body、严格判定、凭证四字段
func TestStsChain_BodyNotQuery(t *testing.T) {
	var cap *captured
	reply := `{"RequestId":"r3","Credentials":{"AccessKeyId":"STS.ak","AccessKeySecret":"STS.sk","SecurityToken":"STS.token","Expiration":"2026-08-18T13:00:00Z"}}`
	srv := captureServer(t, &cap, 200, reply)

	sts := NewSts(fixedClient(srv.URL))
	res, err := sts.AssumeRole(context.Background(), "acs:ram::123:role/test", "session1", nil, 1800)
	if err != nil {
		t.Fatalf("AssumeRole 失败: %v", err)
	}
	if !res.Success {
		t.Fatalf("应成功: %+v", res)
	}
	if res.Credentials != (StsCredentials{AccessKeyID: "STS.ak", AccessKeySecret: "STS.sk", SecurityToken: "STS.token", Expiration: "2026-08-18T13:00:00Z"}) {
		t.Fatalf("凭证四字段不符: %+v", res.Credentials)
	}

	if cap.method != "POST" || len(cap.query) != 0 {
		t.Fatalf("Sts 链应 POST 且 query 空: %s %v", cap.method, cap.query)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(cap.body), &body); err != nil {
		t.Fatalf("body 应为 JSON: %v (%s)", err, cap.body)
	}
	if body["RoleArn"] != "acs:ram::123:role/test" || body["RoleSessionName"] != "session1" {
		t.Fatalf("body 参数不符: %v", body)
	}
	if cap.header.Get("Content-Type") != "application/json" {
		t.Fatalf("应带 application/json: %s", cap.header.Get("Content-Type"))
	}
}

// TestStsChain_StrictCodeFail Sts 严格判定：响应带 Code 即失败（即使 2xx）
func TestStsChain_StrictCodeFail(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 200, `{"Code":"NoPermission","Message":"无权限","RequestId":"r4"}`)

	sts := NewSts(fixedClient(srv.URL))
	res, err := sts.AssumeRole(context.Background(), "arn", "s", nil, 0)
	if err != nil {
		t.Fatalf("HTTP 层不应报错: %v", err)
	}
	if res.Success {
		t.Fatal("Sts 链带 Code 应严格判失败")
	}
}

// TestCanonicalQueryString 编码细节：空格 %20（非 +）、字典序
func TestCanonicalQueryString(t *testing.T) {
	q := url.Values{}
	q.Set("b", "x y")
	q.Set("a", "1")
	got := canonicalQueryString(q)
	if got != "a=1&b=x%20y" {
		t.Fatalf("canonical query 不符: %q", got)
	}
	if canonicalQueryString(nil) != "" {
		t.Fatal("空 query 应返回空串")
	}
}

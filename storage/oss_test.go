package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// newTestOss 四参构造（v0.8.0 起 region 由端点推导：oss-cn-hangzhou → cn-hangzhou，向量不变）
func newTestOss() *OssStorage {
	o := NewOss("AK_TEST", "SK_TEST", "bucket1", "https://oss-cn-hangzhou.aliyuncs.com")
	o.Now = func() time.Time { return fixedTime }
	return o
}

// TestOss_PresignIndependentRecompute V4 预签名 URL 独立重算
func TestOss_PresignIndependentRecompute(t *testing.T) {
	o := newTestOss()
	u, _ := o.UploadURL(context.Background(), "dir/a b.png", 600)

	if !strings.Contains(u, "x-oss-signature=") {
		t.Fatalf("URL 缺签名: %s", u)
	}
	// 独立重算：按 URL 里现成参数重建
	sig := u[strings.Index(u, "x-oss-signature=")+len("x-oss-signature="):]
	params := map[string]string{
		"x-oss-additional-headers": "host",
		"x-oss-credential":         "AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request",
		"x-oss-date":               "20260818T120000Z",
		"x-oss-expires":            "600",
		"x-oss-signature-version":  "OSS4-HMAC-SHA256",
	}
	cq := canonicalQuery(params)
	cr := strings.Join([]string{"PUT", "/bucket1/" + uriEncode("dir/a b.png"), cq, "host:bucket1.oss-cn-hangzhou.aliyuncs.com", "", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sts := strings.Join([]string{"OSS4-HMAC-SHA256", "20260818T120000Z", "20260818/cn-hangzhou/oss/aliyun_v4_request", sha256Hex([]byte(cr))}, "\n")

	// 独立四层派生
	key := "aliyun_v4" + "SK_TEST"
	h := func(k, d []byte) []byte { m := hmac.New(sha256.New, k); m.Write(d); return m.Sum(nil) }
	k1 := h([]byte(key), []byte("20260818"))
	k2 := h(k1, []byte("cn-hangzhou"))
	k3 := h(k2, []byte("oss"))
	k4 := h(k3, []byte("aliyun_v4_request"))
	want := hex.EncodeToString(h(k4, []byte(sts)))

	if sig != want {
		t.Fatalf("签名不符:\n got=%s\nwant=%s", sig, want)
	}
	// UriEncode 豁免斜杠：路径斜杠原样
	if !strings.Contains(u, "/dir/a%20b.png?") {
		t.Fatalf("路径编码不符（斜杠应豁免、空格%%20）: %s", u)
	}
}

// TestOss_UploadTicketPolicy PostObject policy 签名独立重算 + 表单字段集
func TestOss_UploadTicketPolicy(t *testing.T) {
	o := newTestOss()
	ticket, err := o.UploadTicket(context.Background(), "uploads", "x.png", 600, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"key", "policy", "x-oss-signature-version", "x-oss-credential", "x-oss-date", "x-oss-signature", "success_action_status", "x-oss-content-type"} {
		if _, ok := ticket.FormData[field]; !ok {
			t.Fatalf("表单缺字段 %s: %v", field, ticket.FormData)
		}
	}
	if ticket.Method != "POST" || ticket.ContentType != "image/png" {
		t.Fatalf("method/contentType 不符: %s %s", ticket.Method, ticket.ContentType)
	}
	// 未配 AccessDomain：UploadURL 兜底 bucket.endpoint 形态
	if ticket.UploadURL != "https://bucket1.oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("UploadURL 兜底形态不符: %s", ticket.UploadURL)
	}

	// policy 签名独立重算
	policyB64 := ticket.FormData["policy"]
	policyJSON, err := base64.StdEncoding.DecodeString(policyB64)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(policyJSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["expiration"] != "2026-08-18T12:10:00.000Z" {
		t.Fatalf("expiration 不符: %v", decoded["expiration"])
	}
	conds, _ := decoded["conditions"].([]any)
	if len(conds) < 8 { // 7 固定 + 1 content-length-range
		t.Fatalf("conditions 应含固定 7 + 大小约束: %v", conds)
	}

	h := func(k, d []byte) []byte { m := hmac.New(sha256.New, k); m.Write(d); return m.Sum(nil) }
	k1 := h([]byte("aliyun_v4SK_TEST"), []byte("20260818"))
	k2 := h(k1, []byte("cn-hangzhou"))
	k3 := h(k2, []byte("oss"))
	k4 := h(k3, []byte("aliyun_v4_request"))
	want := hex.EncodeToString(h(k4, []byte(policyB64)))
	if ticket.FormData["x-oss-signature"] != want {
		t.Fatalf("policy 签名不符:\n got=%s\nwant=%s", ticket.FormData["x-oss-signature"], want)
	}
}

// TestOss_UploadURLAccessDomain 上传链：配 AccessDomain 后预签名 host 切 CNAME 域（完整 host 不拼 bucket），独立重算验证
func TestOss_UploadURLAccessDomain(t *testing.T) {
	o := newTestOss()
	o.AccessDomain = "https://cdn.example.com"
	u, _ := o.UploadURL(context.Background(), "dir/a b.png", 600)

	// 完整 host 直入签名与 URL，不拼 bucket 前缀（对齐官方 SDK buildURLV4 的 CNAME 分支）
	if !strings.HasPrefix(u, "https://cdn.example.com/dir/a%20b.png?") {
		t.Fatalf("UploadURL host 应切 cdn.example.com（不拼 bucket）: %s", u)
	}
	// 独立重算（仅 host 换 CNAME 域，region 仍由公网 Endpoint 推导）
	sig := u[strings.Index(u, "x-oss-signature=")+len("x-oss-signature="):]
	params := map[string]string{
		"x-oss-additional-headers": "host",
		"x-oss-credential":         "AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request",
		"x-oss-date":               "20260818T120000Z",
		"x-oss-expires":            "600",
		"x-oss-signature-version":  "OSS4-HMAC-SHA256",
	}
	cq := canonicalQuery(params)
	cr := strings.Join([]string{"PUT", "/bucket1/" + uriEncode("dir/a b.png"), cq, "host:cdn.example.com", "", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sts := strings.Join([]string{"OSS4-HMAC-SHA256", "20260818T120000Z", "20260818/cn-hangzhou/oss/aliyun_v4_request", sha256Hex([]byte(cr))}, "\n")
	h := func(k, d []byte) []byte { m := hmac.New(sha256.New, k); m.Write(d); return m.Sum(nil) }
	k1 := h([]byte("aliyun_v4SK_TEST"), []byte("20260818"))
	k2 := h(k1, []byte("cn-hangzhou"))
	k3 := h(k2, []byte("oss"))
	k4 := h(k3, []byte("aliyun_v4_request"))
	want := hex.EncodeToString(h(k4, []byte(sts)))
	if sig != want {
		t.Fatalf("CNAME host 签名不符:\n got=%s\nwant=%s", sig, want)
	}
}

// TestOss_ReadChain 读取链优先级：AccelerateDomain > AccessDomain > bucket.endpoint（含 PublicBase）
func TestOss_ReadChain(t *testing.T) {
	o := newTestOss()

	// 全未配：兜底 bucket.endpoint，与历史形态逐字节一致
	if base := o.PublicBase(); base != "https://bucket1.oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("未配域名 PublicBase 应兜底: %s", base)
	}
	u, _ := o.AccessURL(context.Background(), "k.png", 300, "")
	if !strings.HasPrefix(u, "https://bucket1.oss-cn-hangzhou.aliyuncs.com/k.png?") {
		t.Fatalf("未配域名 AccessURL 应兜底: %s", u)
	}

	// 只配 Access：读取链吃 Access（第二优先生效）
	o.AccessDomain = "cdn.example.com"
	if base := o.PublicBase(); base != "https://cdn.example.com" {
		t.Fatalf("配 Access 后 PublicBase 不符: %s", base)
	}

	// 再配 Accelerate：读取链最高优先（尾斜杠剥掉）；上传链不受影响仍走 Access
	o.AccelerateDomain = "https://acc.example.com/"
	if base := o.PublicBase(); base != "https://acc.example.com" {
		t.Fatalf("配 Accelerate 后 PublicBase 不符（尾斜杠应剥）: %s", base)
	}
	ru, _ := o.AccessURL(context.Background(), "k.png", 300, "")
	if !strings.HasPrefix(ru, "https://acc.example.com/k.png?") {
		t.Fatalf("配 Accelerate 后 AccessURL host 不符: %s", ru)
	}
	pu, _ := o.UploadURL(context.Background(), "k.png", 300)
	if !strings.HasPrefix(pu, "https://cdn.example.com/k.png?") {
		t.Fatalf("上传链不受 Accelerate 影响（仍 Access）: %s", pu)
	}
	ticket, _ := o.UploadTicket(context.Background(), "uploads", "x.png", 600, 0, 0)
	if ticket.UploadURL != "https://cdn.example.com" {
		t.Fatalf("ticket.UploadURL 应走上传链: %s", ticket.UploadURL)
	}
}

// TestOss_ProtocolSource 协议唯一事实源：裸域名与带前缀 Endpoint 产物一致；Protocol 覆盖一切前缀且不扰动签名
func TestOss_ProtocolSource(t *testing.T) {
	withScheme := NewOss("AK", "SK", "b1", "https://oss-cn-hangzhou.aliyuncs.com")
	bare := NewOss("AK", "SK", "b1", "oss-cn-hangzhou.aliyuncs.com/")
	withScheme.Now = func() time.Time { return fixedTime }
	bare.Now = func() time.Time { return fixedTime }
	u1, _ := withScheme.UploadURL(context.Background(), "k.txt", 60)
	u2, _ := bare.UploadURL(context.Background(), "k.txt", 60)
	if u1 != u2 {
		t.Fatalf("裸域名与带前缀 Endpoint 产物应一致:\n %s\n %s", u1, u2)
	}

	// Endpoint 自带 http 前缀也被 Protocol 覆盖（默认 https）
	httpEp := NewOss("AK", "SK", "b1", "http://oss-cn-hangzhou.aliyuncs.com")
	httpEp.Now = func() time.Time { return fixedTime }
	u3, _ := httpEp.UploadURL(context.Background(), "k.txt", 60)
	if !strings.HasPrefix(u3, "https://") {
		t.Fatalf("协议应以 Protocol 为准（默认 https）: %s", u3)
	}
	// 切 http 只换前缀——host 不含协议，签名应逐字节不变
	httpEp.Protocol = "http"
	u4, _ := httpEp.UploadURL(context.Background(), "k.txt", 60)
	if u4 != "http://"+strings.TrimPrefix(u3, "https://") {
		t.Fatalf("协议切换仅换前缀，签名不应变:\n %s\n %s", u3, u4)
	}
}

// TestOss_InternalView 内网视图：未配 InternalEndpoint 时操作线炸出配漏（不静默回落公网）、签名线产物与原实例完全一致
func TestOss_InternalView(t *testing.T) {
	o := newTestOss()
	view := o.Internal()

	// 未配 InternalEndpoint：操作线各方法报错且明确指向配漏
	ctx := context.Background()
	if err := view.Put(ctx, "k", "image/png", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "InternalEndpoint") {
		t.Fatalf("视图 Put 未配 InternalEndpoint 应报错: %v", err)
	}
	if _, err := view.Head(ctx, "k"); err == nil || !strings.Contains(err.Error(), "InternalEndpoint") {
		t.Fatalf("视图 Head 未配 InternalEndpoint 应报错: %v", err)
	}
	if _, _, err := view.Get(ctx, "k"); err == nil || !strings.Contains(err.Error(), "InternalEndpoint") {
		t.Fatalf("视图 Get 未配 InternalEndpoint 应报错: %v", err)
	}
	if _, err := view.List(ctx, "p", "", 0); err == nil || !strings.Contains(err.Error(), "InternalEndpoint") {
		t.Fatalf("视图 List 未配 InternalEndpoint 应报错: %v", err)
	}
	if err := view.Delete(ctx, "k"); err == nil || !strings.Contains(err.Error(), "InternalEndpoint") {
		t.Fatalf("视图 Delete 未配 InternalEndpoint 应报错: %v", err)
	}

	// 配置后：签名线四方法产物与原实例完全一致（同参调用比较字符串相等——永不使用内网域名）
	o.InternalEndpoint = "bucket1.oss-cn-hangzhou-internal.aliyuncs.com"
	iv := o.Internal()
	tu, _ := o.UploadTicket(ctx, "uploads", "x.png", 600, 0, 0)
	vt, _ := iv.UploadTicket(ctx, "uploads", "x.png", 600, 0, 0)
	if tu.UploadURL != vt.UploadURL || tu.FormData["x-oss-signature"] != vt.FormData["x-oss-signature"] {
		t.Fatalf("视图 UploadTicket 产物应与原实例一致: %s vs %s", tu.UploadURL, vt.UploadURL)
	}
	uu, _ := o.UploadURL(ctx, "k.png", 60)
	vu, _ := iv.UploadURL(ctx, "k.png", 60)
	if uu != vu {
		t.Fatalf("视图 UploadURL 应与原实例一致:\n %s\n %s", uu, vu)
	}
	au, _ := o.AccessURL(ctx, "k.png", 60, "")
	va, _ := iv.AccessURL(ctx, "k.png", 60, "")
	if au != va {
		t.Fatalf("视图 AccessURL 应与原实例一致:\n %s\n %s", au, va)
	}
	if o.PublicBase() != iv.PublicBase() {
		t.Fatalf("视图 PublicBase 应与原实例一致: %s vs %s", o.PublicBase(), iv.PublicBase())
	}
	if strings.Contains(uu+au+tu.UploadURL, "internal") {
		t.Fatal("签名线产物不应含内网域名")
	}

	// 视图上再调 Internal() 幂等返回自身；原实例再调返回新视图互不干扰
	if iv.Internal() != iv {
		t.Fatal("视图上再调 Internal() 应幂等返回自身")
	}
}

// dialToLocalClient 把任意 host 的连接都拨到本地 addr（免 DNS 验证操作线请求实际形态）
func dialToLocalClient(addr string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
}

// TestOss_OperationsOverHTTP 操作线全链路（本地 server 收头签名请求）：Put 传 body/设 Content-Type、
// Get 404 语义与 meta、Head 存量方法、List query 形态与 XML 解析翻页、视图切内网 host
func TestOss_OperationsOverHTTP(t *testing.T) {
	var reqHost, reqTarget, lastAuth, lastCT, lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody, lastAuth, lastCT, reqHost = string(b), r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Host
		q := ""
		if r.URL.RawQuery != "" {
			q = "?" + r.URL.RawQuery
		}
		reqTarget = r.Method + " " + r.URL.Path + q

		switch {
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/missing":
			w.WriteHeader(http.StatusNotFound)
		case (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/a.png":
			w.Header().Set("Content-Length", "5")
			w.Header().Set("ETag", `"ABC123"`)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("hello"))
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <Name>bucket1</Name>
  <Prefix>dir/</Prefix>
  <MaxKeys>1000</MaxKeys>
  <IsTruncated>true</IsTruncated>
  <Contents><Key>dir/a</Key><LastModified>2026-08-18T12:00:00.000Z</LastModified><ETag>"T1"</ETag><Size>10</Size></Contents>
  <Contents><Key>dir/b</Key><LastModified>2026-08-18T12:00:01.000Z</LastModified><ETag>"T2"</ETag><Size>20</Size></Contents>
</ListBucketResult>`)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	o := newTestOss()
	o.Protocol = "http" // 本地明文 server（协议唯一事实源，正好顺带验证）
	o.HTTPClient = dialToLocalClient(srv.Listener.Addr().String())
	ctx := context.Background()

	// Put：body 直传 + Content-Type 头设置（不参与签名）+ 头签名 Authorization
	if err := o.Put(ctx, "dir/a.png", "image/png", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if lastBody != "hello" {
		t.Fatalf("Put body 未到达: %q", lastBody)
	}
	if lastCT != "image/png" {
		t.Fatalf("Put Content-Type 未设置: %q", lastCT)
	}
	if !strings.HasPrefix(lastAuth, "OSS4-HMAC-SHA256 Credential=AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request") {
		t.Fatalf("Put 应带头签名: %q", lastAuth)
	}
	if reqHost != "bucket1.oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("操作线默认 host 不符: %s", reqHost)
	}

	// Get：meta 从响应头（ETag 去引号）+ body 流
	meta, body, err := o.Get(ctx, "a.png")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(body)
	body.Close()
	if string(b) != "hello" || meta.ETag != "ABC123" || meta.ContentType != "image/png" || meta.Size != 5 {
		t.Fatalf("Get meta/body 不符: %+v %q", meta, b)
	}

	// Get 404：nil,nil,nil（与 Head 语义对齐）
	if m, rb, err := o.Get(ctx, "missing"); m != nil || rb != nil || err != nil {
		t.Fatalf("Get 404 应 nil,nil,nil: %v %v %v", m, rb, err)
	}

	// Head 存量方法仍走同一 opHost 骨架
	if m, err := o.Head(ctx, "a.png"); err != nil || m == nil || m.ETag != "ABC123" || m.Size != 5 {
		t.Fatalf("Head 不符: %+v %v", m, err)
	}

	// List：query 字典序形态（list-type=2 & max-keys & prefix=dir%2F——query 语境斜杠编码）+ XML 解析
	ls, err := o.List(ctx, "dir/", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls.Objects) != 2 {
		t.Fatalf("List 条目数不符: %+v", ls)
	}
	first := ls.Objects[0]
	if first.Key != "dir/a" || first.Size != 10 || first.ETag != "T1" ||
		!first.LastModified.Equal(time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("List 条目解析不符（ISO8601 毫秒应被 RFC3339 布局接受）: %+v", first)
	}
	if !ls.IsTruncated || ls.NextMarker != "dir/b" {
		t.Fatalf("IsTruncated/NextMarker（空 NextMarker 取末条 Key 兜底）不符: %+v", ls)
	}
	if reqTarget != "GET /?list-type=2&max-keys=1000&prefix=dir%2F" {
		t.Fatalf("List query 形态不符（字典序+双侧编码）: %s", reqTarget)
	}

	// 带 after 翻页：marker 进 query（仅 after 非空时带）；max<=0 或 >1000 收敛为 1000
	if _, err := o.List(ctx, "dir/", "dir/b", 2); err != nil {
		t.Fatal(err)
	}
	if reqTarget != "GET /?list-type=2&marker=dir%2Fb&max-keys=2&prefix=dir%2F" {
		t.Fatalf("marker/max-keys 形态不符: %s", reqTarget)
	}
	if _, err := o.List(ctx, "dir/", "", 2000); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reqTarget, "max-keys=1000") {
		t.Fatalf("max>1000 应取 1000: %s", reqTarget)
	}

	// Delete 幂等路径仍通
	if err := o.Delete(ctx, "dir/a.png"); err != nil {
		t.Fatal(err)
	}

	// 内网视图：操作线实际打到 InternalEndpoint host
	o.InternalEndpoint = "bucket1.oss-cn-hangzhou-internal.aliyuncs.com"
	if err := o.Internal().Put(ctx, "k", "", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if reqHost != "bucket1.oss-cn-hangzhou-internal.aliyuncs.com" {
		t.Fatalf("视图操作线应走 InternalEndpoint host: %s", reqHost)
	}
}

// TestOss_ExtraSignedHeadersVector 额外签名头固定向量：Put 的 Content-Type 与 Copy 的
// x-oss-copy-source 进签名头集（OSS V4：请求中存在的 Content-Type 必签、x-oss-* 头全签
// ——见官方文档「在Header中包含签名」）。断言 Authorization 的 AdditionalHeaders 含新头
// （字典序四头）+ 独立重算 Signature（测试内手写 canonical，含新头行），并核新头确实随请求发出
func TestOss_ExtraSignedHeadersVector(t *testing.T) {
	var lastAuth, lastCT, lastCopySrc string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth, lastCT, lastCopySrc = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get("x-oss-copy-source")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	o := newTestOss()
	o.Protocol = "http" // 本地明文 server（dialToLocalClient 免 DNS）
	o.HTTPClient = dialToLocalClient(srv.Listener.Addr().String())
	ctx := context.Background()

	// 独立重算：四层派生 + 手写 canonical（与基础三头向量仅差新头行）
	h := func(k, d []byte) []byte { m := hmac.New(sha256.New, k); m.Write(d); return m.Sum(nil) }
	k4 := h(h(h(h([]byte("aliyun_v4SK_TEST"), []byte("20260818")), []byte("cn-hangzhou")), []byte("oss")), []byte("aliyun_v4_request"))
	recompute := func(method, key, canonicalHeaders, signedHeaders string) string {
		cr := strings.Join([]string{method, "/bucket1/" + key, "", canonicalHeaders, signedHeaders, "UNSIGNED-PAYLOAD"}, "\n")
		sts := strings.Join([]string{"OSS4-HMAC-SHA256", "20260818T120000Z", "20260818/cn-hangzhou/oss/aliyun_v4_request", sha256Hex([]byte(cr))}, "\n")
		return hex.EncodeToString(h(k4, []byte(sts)))
	}

	// Put 带 Content-Type：四头字典序 content-type < host < x-oss-content-sha256 < x-oss-date
	if err := o.Put(ctx, "dir/a.png", "image/png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if lastCT != "image/png" {
		t.Fatalf("Put Content-Type 头未发出: %q", lastCT)
	}
	putSigned := "content-type;host;x-oss-content-sha256;x-oss-date"
	putHeaders := "content-type:image/png\nhost:bucket1.oss-cn-hangzhou.aliyuncs.com\nx-oss-content-sha256:UNSIGNED-PAYLOAD\nx-oss-date:20260818T120000Z\n"
	wantAuth := fmt.Sprintf("OSS4-HMAC-SHA256 Credential=AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request,AdditionalHeaders=%s,Signature=%s",
		putSigned, recompute(http.MethodPut, "dir/a.png", putHeaders, putSigned))
	if lastAuth != wantAuth {
		t.Fatalf("Put Authorization 不符:\n got=%s\nwant=%s", lastAuth, wantAuth)
	}

	// Copy：x-oss-copy-source 进签名头集（host < x-oss-content-sha256 < x-oss-copy-source < x-oss-date）
	if err := o.Copy(ctx, "src/a.png", "dst/b.png"); err != nil {
		t.Fatal(err)
	}
	if lastCopySrc != "/bucket1/src/a.png" {
		t.Fatalf("Copy x-oss-copy-source 头值不符: %q", lastCopySrc)
	}
	copySigned := "host;x-oss-content-sha256;x-oss-copy-source;x-oss-date"
	copyHeaders := "host:bucket1.oss-cn-hangzhou.aliyuncs.com\nx-oss-content-sha256:UNSIGNED-PAYLOAD\nx-oss-copy-source:/bucket1/src/a.png\nx-oss-date:20260818T120000Z\n"
	wantCopy := fmt.Sprintf("OSS4-HMAC-SHA256 Credential=AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request,AdditionalHeaders=%s,Signature=%s",
		copySigned, recompute(http.MethodPut, "dst/b.png", copyHeaders, copySigned))
	if lastAuth != wantCopy {
		t.Fatalf("Copy Authorization 不符:\n got=%s\nwant=%s", lastAuth, wantCopy)
	}
}

// urlOf 解析产物 URL 供域名矩阵断言取段（host=authority 段含端口、path=解码后路径）——
// 直白取段比对，不重算签名（签名正确性由固定向量测试守护）
func urlOf(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("URL 解析失败: %s: %v", rawURL, err)
	}
	return u
}

// TestDomainMatrixOss oss 域名优先级矩阵全组合（表驱动）：上传链 AccessDomain >
// bucket.endpoint 兜底、读取链 AccelerateDomain > AccessDomain > bucket.endpoint。
// 2×2 组合（两域名各配/不配）一次跑全；每行核上传链两产物（PUT 预签名 / POST 票据
// 目标）与读取链两产物（GET 预签名 / PublicBase）的 host 段。补缺点：仅配
// Accelerate 时上传链应走兜底；仅配 Access 时 AccessURL host 直断
func TestDomainMatrixOss(t *testing.T) {
	cases := []struct {
		name     string
		access   string // AccessDomain 配置（空=不配）
		accel    string // AccelerateDomain 配置（空=不配）
		wantUp   string // 上传链期望 host
		wantRead string // 读取链期望 host
	}{
		{"全不配-两链兜底bucket端点", "", "", "bucket1.oss-cn-hangzhou.aliyuncs.com", "bucket1.oss-cn-hangzhou.aliyuncs.com"},
		{"仅Access-两链同命中CNAME", "https://cdn.example.com", "", "cdn.example.com", "cdn.example.com"},
		{"Access加Accelerate-上传Access读取Accelerate", "https://cdn.example.com", "https://acc.example.com", "cdn.example.com", "acc.example.com"},
		{"仅Accelerate-上传链不受影响仍兜底", "", "https://acc.example.com", "bucket1.oss-cn-hangzhou.aliyuncs.com", "acc.example.com"},
	}
	ctx := context.Background()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := newTestOss()
			o.AccessDomain = c.access
			o.AccelerateDomain = c.accel

			// 上传链：PUT 预签名 URL 与 POST 票据表单目标，host 段都应为 wantUp
			up, err := o.UploadURL(ctx, "dir/k.png", 300)
			if err != nil {
				t.Fatal(err)
			}
			if got := urlOf(t, up).Host; got != c.wantUp {
				t.Errorf("UploadURL host = %q, want %q（url=%s）", got, c.wantUp, up)
			}
			ticket, err := o.UploadTicket(ctx, "uploads", "x.png", 600, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := urlOf(t, ticket.UploadURL).Host; got != c.wantUp {
				t.Errorf("UploadTicket.UploadURL host = %q, want %q（url=%s）", got, c.wantUp, ticket.UploadURL)
			}

			// 读取链：GET 预签名 URL 与公有直链前缀，host 段都应为 wantRead
			acc, err := o.AccessURL(ctx, "dir/k.png", 300, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := urlOf(t, acc).Host; got != c.wantRead {
				t.Errorf("AccessURL host = %q, want %q（url=%s）", got, c.wantRead, acc)
			}
			if got := urlOf(t, o.PublicBase()).Host; got != c.wantRead {
				t.Errorf("PublicBase host = %q, want %q", got, c.wantRead)
			}
		})
	}
}

// TestOss_AccessURLContentDisposition 下载文件名参数进签名（response-content-disposition）
func TestOss_AccessURLContentDisposition(t *testing.T) {
	o := newTestOss()
	o.AccessURL(context.Background(), "r/a.png", 300, "") // 空参回归：不 panic、URL 无该参数
	u, _ := o.AccessURL(context.Background(), "r/a.png", 300, `attachment; filename="report.png"`)
	if !strings.Contains(u, "response-content-disposition=") {
		t.Fatalf("URL 缺 response-content-disposition: %s", u)
	}

	// 独立重算：response-* 参数须与五参数一同进 CanonicalQuery（签的是参数全集）
	sig := u[strings.Index(u, "x-oss-signature=")+len("x-oss-signature="):]
	params := map[string]string{
		"response-content-disposition": `attachment; filename="report.png"`,
		"x-oss-additional-headers":     "host",
		"x-oss-credential":             "AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request",
		"x-oss-date":                   "20260818T120000Z",
		"x-oss-expires":                "300",
		"x-oss-signature-version":      "OSS4-HMAC-SHA256",
	}
	cq := canonicalQuery(params)
	cr := strings.Join([]string{"GET", "/bucket1/r/a.png", cq, "host:bucket1.oss-cn-hangzhou.aliyuncs.com", "", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sts := strings.Join([]string{"OSS4-HMAC-SHA256", "20260818T120000Z", "20260818/cn-hangzhou/oss/aliyun_v4_request", sha256Hex([]byte(cr))}, "\n")
	h := func(k, d []byte) []byte { m := hmac.New(sha256.New, k); m.Write(d); return m.Sum(nil) }
	k4 := h(h(h(h([]byte("aliyun_v4SK_TEST"), []byte("20260818")), []byte("cn-hangzhou")), []byte("oss")), []byte("aliyun_v4_request"))
	if want := hex.EncodeToString(h(k4, []byte(sts))); sig != want {
		t.Fatalf("含 response 参数的签名不符:\n got=%s\nwant=%s", sig, want)
	}
	// URL 里携带的 query 与签名时的 canonical query 同串（前端不可增删参数的根源）
	if !strings.Contains(u, cq+"&x-oss-signature=") {
		t.Fatalf("URL query 与 canonicalQuery 应同串: %s", u)
	}
}

// TestOss_V4CanonicalSpec OSS V4 官方规范符合性（Issue #79 回归守护）：
//  1. Authorization 字段名为 AdditionalHeaders（OSS V4 header 签名不认 SignedHeaders → 400 InvalidArgument）；
//  2. CanonicalURI 为 /{bucket}/{key}（虚拟主机风格下 OSS 服务端按带 bucket 路径验签 → 403 SignatureDoesNotMatch）；
//  3. key 在 URL 与 CanonicalURI 两处同用 uriEncode（斜杠豁免），请求行合法。
//
// 官方规范：https://help.aliyun.com/zh/oss/developer-reference/recommend-to-use-signature-version-4
func TestOss_V4CanonicalSpec(t *testing.T) {
	var lastAuth, lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth, lastPath = r.Header.Get("Authorization"), r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	o := newTestOss()
	o.Protocol = "http"
	o.HTTPClient = dialToLocalClient(srv.Listener.Addr().String())
	ctx := context.Background()

	if err := o.Put(ctx, "policy-processing/a b.pdf", "application/pdf", strings.NewReader("pdf")); err != nil {
		t.Fatal(err)
	}
	// 请求路径：bucket 只在 host 里（虚拟主机风格），路径仍是编码后的 key
	if lastPath != "/policy-processing/a%20b.pdf" {
		t.Fatalf("请求路径应为编码后 key（斜杠豁免）: %q", lastPath)
	}
	if strings.Contains(lastAuth, "SignedHeaders=") {
		t.Fatalf("Authorization 不得使用 SignedHeaders 字段: %q", lastAuth)
	}
	signed := "content-type;host;x-oss-content-sha256;x-oss-date"
	_, fields, _ := strings.Cut(lastAuth, " ")
	if !slices.Contains(strings.Split(fields, ","), "AdditionalHeaders="+signed) {
		t.Fatalf("Authorization 应使用 AdditionalHeaders 字段: %q", lastAuth)
	}

	// 独立重算：CanonicalURI 带 bucket 前缀
	h := func(k, d []byte) []byte { m := hmac.New(sha256.New, k); m.Write(d); return m.Sum(nil) }
	k4 := h(h(h(h([]byte("aliyun_v4SK_TEST"), []byte("20260818")), []byte("cn-hangzhou")), []byte("oss")), []byte("aliyun_v4_request"))
	canonicalHeaders := "content-type:application/pdf\nhost:bucket1.oss-cn-hangzhou.aliyuncs.com\n" +
		"x-oss-content-sha256:UNSIGNED-PAYLOAD\nx-oss-date:20260818T120000Z\n"
	cr := strings.Join([]string{
		http.MethodPut, "/bucket1/policy-processing/a%20b.pdf", "",
		canonicalHeaders, signed, "UNSIGNED-PAYLOAD",
	}, "\n")
	sts := strings.Join([]string{"OSS4-HMAC-SHA256", "20260818T120000Z", "20260818/cn-hangzhou/oss/aliyun_v4_request", sha256Hex([]byte(cr))}, "\n")
	want := fmt.Sprintf("OSS4-HMAC-SHA256 Credential=AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request,AdditionalHeaders=%s,Signature=%s",
		signed, hex.EncodeToString(h(k4, []byte(sts))))
	if lastAuth != want {
		t.Fatalf("Put Authorization 不符:\n got=%s\nwant=%s", lastAuth, want)
	}

	// List：无 object 时 CanonicalURI 为 /{bucket}/
	if _, err := o.List(ctx, "dir/", "", 0); err == nil {
		// 本地 server 返回空 body，XML 解码失败属预期；只取 Authorization 形态
		t.Log("list 无 XML body")
	}
	listSigned := "host;x-oss-content-sha256;x-oss-date"
	listHeaders := "host:bucket1.oss-cn-hangzhou.aliyuncs.com\nx-oss-content-sha256:UNSIGNED-PAYLOAD\nx-oss-date:20260818T120000Z\n"
	listCR := strings.Join([]string{
		http.MethodGet, "/bucket1/", "list-type=2&max-keys=1000&prefix=dir%2F",
		listHeaders, listSigned, "UNSIGNED-PAYLOAD",
	}, "\n")
	listSTS := strings.Join([]string{"OSS4-HMAC-SHA256", "20260818T120000Z", "20260818/cn-hangzhou/oss/aliyun_v4_request", sha256Hex([]byte(listCR))}, "\n")
	wantList := fmt.Sprintf("OSS4-HMAC-SHA256 Credential=AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request,AdditionalHeaders=%s,Signature=%s",
		listSigned, hex.EncodeToString(h(k4, []byte(listSTS))))
	if lastAuth != wantList {
		t.Fatalf("List Authorization 不符:\n got=%s\nwant=%s", lastAuth, wantList)
	}
}

// TestOss_PresignCanonicalURIBucket 预签名 URL 的 CanonicalURI 同样带 bucket 前缀
// （官方示例 /examplebucket/exampleobject）；URL 路径段仍只有 key。
func TestOss_PresignCanonicalURIBucket(t *testing.T) {
	o := newTestOss()
	u, _ := o.AccessURL(context.Background(), "dir/a b.png", 600, "")

	sig := u[strings.Index(u, "x-oss-signature=")+len("x-oss-signature="):]
	params := map[string]string{
		"x-oss-additional-headers": "host",
		"x-oss-credential":         "AK_TEST/20260818/cn-hangzhou/oss/aliyun_v4_request",
		"x-oss-date":               "20260818T120000Z",
		"x-oss-expires":            "600",
		"x-oss-signature-version":  "OSS4-HMAC-SHA256",
	}
	cq := canonicalQuery(params)
	cr := strings.Join([]string{
		"GET", "/bucket1/" + uriEncode("dir/a b.png"), cq,
		"host:bucket1.oss-cn-hangzhou.aliyuncs.com", "", "host", "UNSIGNED-PAYLOAD",
	}, "\n")
	sts := strings.Join([]string{"OSS4-HMAC-SHA256", "20260818T120000Z", "20260818/cn-hangzhou/oss/aliyun_v4_request", sha256Hex([]byte(cr))}, "\n")
	h := func(k, d []byte) []byte { m := hmac.New(sha256.New, k); m.Write(d); return m.Sum(nil) }
	k4 := h(h(h(h([]byte("aliyun_v4SK_TEST"), []byte("20260818")), []byte("cn-hangzhou")), []byte("oss")), []byte("aliyun_v4_request"))
	if want := hex.EncodeToString(h(k4, []byte(sts))); sig != want {
		t.Fatalf("预签名 CanonicalURI 应带 bucket 前缀:\n got=%s\nwant=%s", sig, want)
	}
	if !strings.Contains(u, "https://bucket1.oss-cn-hangzhou.aliyuncs.com/dir/a%20b.png?") {
		t.Fatalf("URL 路径段应只含编码后 key: %s", u)
	}
}

// TestOss_ReadSigningHost GET 一律桶域名签名，保留读取域名及完整路径/query。
func TestOss_ReadSigningHost(t *testing.T) {
	for _, domains := range []struct{ access, accelerate, wantHost string }{
		{"", "", "bucket1.oss-cn-hangzhou.aliyuncs.com"},
		{"https://access.example.com/", "", "access.example.com"},
		{"", "https://fast.example.com/", "fast.example.com"},
		{"access.example.com", "fast.example.com", "fast.example.com"},
	} {
		t.Run(domains.wantHost, func(t *testing.T) {
			o := newTestOss()
			o.AccessDomain, o.AccelerateDomain = domains.access, domains.accelerate
			o.Protocol = "http"
			o.InternalEndpoint = "bucket1.oss-cn-hangzhou-internal.aliyuncs.com"
			const key = "dir/中文 a+%?#.png"
			const disposition = `attachment; filename*=UTF-8''%E4%B8%AD.png`
			raw, err := o.AccessURL(context.Background(), key, 617, disposition)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != domains.wantHost || u.Scheme != "http" || u.Path != "/"+key || u.Fragment != "" {
				t.Fatalf("访问域名/路径/协议变化: %s", raw)
			}
			q := u.Query()
			if q.Get("response-content-disposition") != disposition || q.Get("x-oss-expires") != "617" || len(q) != 7 {
				t.Fatalf("完整签名参数丢失: %v", q)
			}
			sig := q.Get("x-oss-signature")
			q.Del("x-oss-signature")
			// 独立 HMAC 四层派生，CanonicalHeaders 的 host 不从最终 URL 推断。
			signHost := "bucket1.oss-cn-hangzhou.aliyuncs.com"
			params := map[string]string{}
			for k := range q {
				params[k] = q.Get(k)
			}
			cr := strings.Join([]string{"GET", "/bucket1/" + u.EscapedPath()[1:], canonicalQuery(params), "host:" + signHost, "", "host", "UNSIGNED-PAYLOAD"}, "\n")
			sum := sha256.Sum256([]byte(cr))
			sts := "OSS4-HMAC-SHA256\n20260818T120000Z\n20260818/cn-hangzhou/oss/aliyun_v4_request\n" + hex.EncodeToString(sum[:])
			h := func(k []byte, data string) []byte {
				m := hmac.New(sha256.New, k)
				m.Write([]byte(data))
				return m.Sum(nil)
			}
			k := h(h(h(h([]byte("aliyun_v4SK_TEST"), "20260818"), "cn-hangzhou"), "oss"), "aliyun_v4_request")
			if want := hex.EncodeToString(h(k, sts)); sig != want {
				t.Fatalf("签名不符: %s != %s", sig, want)
			}
			viewRaw, err := o.Internal().AccessURL(context.Background(), key, 617, disposition)
			if err != nil || viewRaw != raw {
				t.Fatalf("内网视图改变 GET 签名: %v", err)
			}
			// 同时清空读取域名后产生的桶直连 query 必须逐字节等同。
			direct := *o
			direct.AccessDomain, direct.AccelerateDomain = "", ""
			directRaw, _ := direct.AccessURL(context.Background(), key, 617, disposition)
			if urlOf(t, directRaw).RawQuery != u.RawQuery {
				t.Fatal("桶直连与 CDN query 不一致")
			}
		})
	}
}

func TestOss_ReadSigningHostOtherLinesUnchanged(t *testing.T) {
	o := newTestOss()
	o.AccessDomain, o.AccelerateDomain = "upload.example.com", "cdn.example.com"
	ctx := context.Background()
	put, err := o.UploadURL(ctx, "dir/a.png", 600)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := o.UploadTicket(ctx, "dir", "a.png", 600, 1, 1024)
	if err != nil {
		t.Fatal(err)
	}
	req, err := o.signedRequest(ctx, "GET", "dir/a.png", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if urlOf(t, put).Host != "upload.example.com" || ticket.UploadURL != "https://upload.example.com" || o.PublicBase() != "https://cdn.example.com" || req.URL.Host != "bucket1.oss-cn-hangzhou.aliyuncs.com" {
		t.Fatal("GET 桶域名签名改变上传/公有直链/操作线域名")
	}
}

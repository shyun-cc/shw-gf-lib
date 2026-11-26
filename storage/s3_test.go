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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hmacChain 独立重算用的 HMAC 链（不复用被测代码的签名函数）
func hmacChain(t *testing.T) func(k, d []byte) []byte {
	t.Helper()
	return func(k, d []byte) []byte {
		m := hmac.New(sha256.New, k)
		m.Write(d)
		return m.Sum(nil)
	}
}

// TestS3_PresignStructure SigV4 预签名六参数与独立重算（path-style 默认形态，
// region 由 endpoint 推导 us-east-1，四参构造）
func TestS3_PresignStructure(t *testing.T) {
	s := NewS3("AK", "SK", "bkt", "http://127.0.0.1:9000")
	s.Now = func() time.Time { return fixedTime }
	u, _ := s.UploadURL(context.Background(), "k1.txt", 300)

	for _, p := range []string{"X-Amz-Algorithm=AWS4-HMAC-SHA256", "X-Amz-Credential=AK%2F20260818%2Fus-east-1%2Fs3%2Faws4_request",
		"X-Amz-Date=20260818T120000Z", "X-Amz-Expires=300", "X-Amz-SignedHeaders=host", "X-Amz-Signature="} {
		if !strings.Contains(u, p) {
			t.Fatalf("URL 缺 %s: %s", p, u)
		}
	}

	// 独立重算签名（host=endpoint 裸 host 含非标端口；CanonicalURI=path-style /bucket/key）
	sig := u[strings.Index(u, "X-Amz-Signature=")+len("X-Amz-Signature="):]
	params := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    "AK/20260818/us-east-1/s3/aws4_request",
		"X-Amz-Date":          "20260818T120000Z",
		"X-Amz-Expires":       "300",
		"X-Amz-SignedHeaders": "host",
	}
	cq := canonicalQuery(params)
	cr := strings.Join([]string{"PUT", "/bkt/k1.txt", cq, "host:127.0.0.1:9000", "", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sts := strings.Join([]string{"AWS4-HMAC-SHA256", "20260818T120000Z", "20260818/us-east-1/s3/aws4_request", sha256Hex([]byte(cr))}, "\n")
	h := hmacChain(t)
	k1 := h([]byte("AWS4SK"), []byte("20260818"))
	k2 := h(k1, []byte("us-east-1"))
	k3 := h(k2, []byte("s3"))
	k4 := h(k3, []byte("aws4_request"))
	want := hex.EncodeToString(h(k4, []byte(sts)))
	if sig != want {
		t.Fatalf("SigV4 签名不符:\n got=%s\nwant=%s", sig, want)
	}
}

// TestS3_AddressVirtualHosted virtual-hosted 寻址向量：bucket 进 host、路径不含
// bucket、region 推导 cn-north-1、协议默认 https，独立重算签名
func TestS3_AddressVirtualHosted(t *testing.T) {
	s := NewS3("AK", "SK", "bkt", "https://s3.cn-north-1.amazonaws.com.cn")
	s.AddressingStyle = "virtual-hosted"
	s.Now = func() time.Time { return fixedTime }
	u, _ := s.UploadURL(context.Background(), "k1.txt", 300)

	if !strings.HasPrefix(u, "https://bkt.s3.cn-north-1.amazonaws.com.cn/k1.txt?") {
		t.Fatalf("vhost URL 形态错: %s", u)
	}

	// 独立重算：host=bucket.端点、CanonicalURI=/key（URL 与 CanonicalRequest 同形态）
	sig := u[strings.Index(u, "X-Amz-Signature=")+len("X-Amz-Signature="):]
	params := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    "AK/20260818/cn-north-1/s3/aws4_request",
		"X-Amz-Date":          "20260818T120000Z",
		"X-Amz-Expires":       "300",
		"X-Amz-SignedHeaders": "host",
	}
	cq := canonicalQuery(params)
	cr := strings.Join([]string{"PUT", "/k1.txt", cq, "host:bkt.s3.cn-north-1.amazonaws.com.cn", "", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sts := strings.Join([]string{"AWS4-HMAC-SHA256", "20260818T120000Z", "20260818/cn-north-1/s3/aws4_request", sha256Hex([]byte(cr))}, "\n")
	h := hmacChain(t)
	k1 := h([]byte("AWS4SK"), []byte("20260818"))
	k2 := h(k1, []byte("cn-north-1"))
	k3 := h(k2, []byte("s3"))
	k4 := h(k3, []byte("aws4_request"))
	want := hex.EncodeToString(h(k4, []byte(sts)))
	if sig != want {
		t.Fatalf("vhost SigV4 签名不符:\n got=%s\nwant=%s", sig, want)
	}
}

// TestS3_DomainChain 双链域名 host 断言：上传链 Access > Endpoint、读取链
// Accelerate > Access > Endpoint；CNAME 命中路径不拼 bucket；PublicBase 三态
func TestS3_DomainChain(t *testing.T) {
	ctx := context.Background()

	// 兜底态：未配置可选域名 → Endpoint path-style（协议从 http:// 前缀推导）
	base := NewS3("AK", "SK", "bkt", "http://127.0.0.1:9000")
	base.Now = func() time.Time { return fixedTime }
	if pb := base.PublicBase(); pb != "http://127.0.0.1:9000/bkt" {
		t.Fatalf("兜底 PublicBase=%s", pb)
	}

	// AccessDomain：上传链与读取链都命中，路径不拼 bucket（CNAME 已绑定桶）
	s := NewS3("AK", "SK", "bkt", "http://127.0.0.1:9000")
	s.AccessDomain = "https://cdn.example.com"
	s.Now = func() time.Time { return fixedTime }
	up, _ := s.UploadURL(ctx, "k1.txt", 300)
	if !strings.HasPrefix(up, "https://cdn.example.com/k1.txt?") {
		t.Fatalf("上传链未命中 AccessDomain: %s", up)
	}
	acc, _ := s.AccessURL(ctx, "k1.txt", 300, "")
	if !strings.HasPrefix(acc, "https://cdn.example.com/k1.txt?") {
		t.Fatalf("读取链未命中 AccessDomain: %s", acc)
	}
	if pb := s.PublicBase(); pb != "https://cdn.example.com" {
		t.Fatalf("AccessDomain PublicBase=%s", pb)
	}

	// CNAME 命中时签名 host 同步切换（独立重算：host=cdn.example.com、CanonicalURI=/k1.txt）
	sig := up[strings.Index(up, "X-Amz-Signature=")+len("X-Amz-Signature="):]
	params := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    "AK/20260818/us-east-1/s3/aws4_request",
		"X-Amz-Date":          "20260818T120000Z",
		"X-Amz-Expires":       "300",
		"X-Amz-SignedHeaders": "host",
	}
	cq := canonicalQuery(params)
	cr := strings.Join([]string{"PUT", "/k1.txt", cq, "host:cdn.example.com", "", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sts := strings.Join([]string{"AWS4-HMAC-SHA256", "20260818T120000Z", "20260818/us-east-1/s3/aws4_request", sha256Hex([]byte(cr))}, "\n")
	h := hmacChain(t)
	k4 := h(h(h(h([]byte("AWS4SK"), []byte("20260818")), []byte("us-east-1")), []byte("s3")), []byte("aws4_request"))
	if want := hex.EncodeToString(h(k4, []byte(sts))); sig != want {
		t.Fatalf("CNAME 签名不符:\n got=%s\nwant=%s", sig, want)
	}

	// AccelerateDomain：仅读取线最高优先，上传线仍走 AccessDomain
	s2 := NewS3("AK", "SK", "bkt", "http://127.0.0.1:9000")
	s2.AccessDomain = "https://cdn.example.com"
	s2.AccelerateDomain = "https://fast.example.com"
	s2.Now = func() time.Time { return fixedTime }
	if up2, _ := s2.UploadURL(ctx, "k1.txt", 300); !strings.HasPrefix(up2, "https://cdn.example.com/k1.txt?") {
		t.Fatalf("上传链不应被 AccelerateDomain 抢占: %s", up2)
	}
	if acc2, _ := s2.AccessURL(ctx, "k1.txt", 300, ""); !strings.HasPrefix(acc2, "https://fast.example.com/k1.txt?") {
		t.Fatalf("读取链 AccelerateDomain 未最高优先: %s", acc2)
	}
	if pb := s2.PublicBase(); pb != "https://fast.example.com" {
		t.Fatalf("AccelerateDomain PublicBase=%s", pb)
	}
}

// TestS3_UploadTicket POST policy 票据向量：policy 解码校验、四层派生重算签名、
// FormData 字段齐全、key 带 directory、表单目标桶根
func TestS3_UploadTicket(t *testing.T) {
	ctx := context.Background()
	s := NewS3("AK", "SK", "bkt", "http://127.0.0.1:9000")
	s.Now = func() time.Time { return fixedTime }

	tkt, err := s.UploadTicket(ctx, "temp", "a.png", 600, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if tkt.Method != http.MethodPost {
		t.Fatalf("Method=%s 应为 POST", tkt.Method)
	}
	if tkt.ContentType != "image/png" {
		t.Fatalf("ContentType=%s", tkt.ContentType)
	}
	// 表单目标：path-style 桶根（host/{bucket}）
	if tkt.UploadURL != "http://127.0.0.1:9000/bkt" {
		t.Fatalf("UploadURL=%s", tkt.UploadURL)
	}

	fd := tkt.FormData
	for _, f := range []string{"key", "policy", "x-amz-algorithm", "x-amz-credential", "x-amz-date", "x-amz-signature"} {
		if fd[f] == "" {
			t.Fatalf("FormData 缺 %s", f)
		}
	}
	// directory 缺陷修复：key 必须带 directory 前缀
	if fd["key"] != "temp/a.png" {
		t.Fatalf("key 丢 directory: %s", fd["key"])
	}
	if fd["x-amz-credential"] != "AK/20260818/us-east-1/s3/aws4_request" {
		t.Fatalf("x-amz-credential=%s", fd["x-amz-credential"])
	}
	if fd["x-amz-date"] != "20260818T120000Z" {
		t.Fatalf("x-amz-date=%s", fd["x-amz-date"])
	}

	// policy base64 解码校验：expiration 与最小条件集
	raw, err := base64.StdEncoding.DecodeString(fd["policy"])
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Expiration string `json:"expiration"`
		Conditions []any  `json:"conditions"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Expiration != "2026-08-18T12:10:00.000Z" {
		t.Fatalf("expiration=%s", p.Expiration)
	}
	var gotBucket, gotAlgo, gotCred, gotDate, gotExactKey, gotLenRange bool
	for _, c := range p.Conditions {
		switch v := c.(type) {
		case map[string]any:
			for k, val := range v {
				switch k {
				case "bucket":
					gotBucket = val == "bkt"
				case "x-amz-algorithm":
					gotAlgo = val == "AWS4-HMAC-SHA256"
				case "x-amz-credential":
					gotCred = val == "AK/20260818/us-east-1/s3/aws4_request"
				case "x-amz-date":
					gotDate = val == "20260818T120000Z"
				}
			}
		case []any:
			if v[0] == "eq" && v[1] == "$key" && v[2] == "temp/a.png" {
				gotExactKey = true
			}
			if v[0] == "content-length-range" && v[1] == float64(1) && v[2] == float64(100) {
				gotLenRange = true
			}
		}
	}
	for name, ok := range map[string]bool{"bucket": gotBucket, "x-amz-algorithm": gotAlgo,
		"x-amz-credential": gotCred, "x-amz-date": gotDate, "eq-key": gotExactKey,
		"content-length-range": gotLenRange} {
		if !ok {
			t.Fatalf("policy conditions 缺 %s: %s", name, raw)
		}
	}

	// 四层派生独立重算 x-amz-signature（StringToSign=base64(policy JSON)）
	h := hmacChain(t)
	k4 := h(h(h(h([]byte("AWS4SK"), []byte("20260818")), []byte("us-east-1")), []byte("s3")), []byte("aws4_request"))
	if want := hex.EncodeToString(h(k4, []byte(fd["policy"]))); fd["x-amz-signature"] != want {
		t.Fatalf("票据签名不符:\n got=%s\nwant=%s", fd["x-amz-signature"], want)
	}

	// min/max 全 0：不带 content-length-range 条件
	tkt0, _ := s.UploadTicket(ctx, "temp", "a.png", 600, 0, 0)
	raw0, _ := base64.StdEncoding.DecodeString(tkt0.FormData["policy"])
	if strings.Contains(string(raw0), "content-length-range") {
		t.Fatalf("min/max 全 0 不应带 content-length-range: %s", raw0)
	}
	// min>0 且 max<=0：max 取 5GB 兜底
	tkt5, _ := s.UploadTicket(ctx, "temp", "a.png", 600, 1, 0)
	raw5, _ := base64.StdEncoding.DecodeString(tkt5.FormData["policy"])
	if !strings.Contains(string(raw5), `"content-length-range",1,5368709120`) {
		t.Fatalf("max 兜底 5GB 缺失: %s", raw5)
	}
}

// TestS3_InternalView 内网视图：未配置报错、签名线产物一致、幂等、操作线切内网 host
func TestS3_InternalView(t *testing.T) {
	ctx := context.Background()
	s := NewS3("AK", "SK", "bkt", "http://127.0.0.1:9000")
	s.Now = func() time.Time { return fixedTime }
	iv := s.Internal()

	// 未配置 InternalEndpoint：操作线报错不发请求
	if _, err := iv.Head(ctx, "k.txt"); err == nil || !strings.Contains(err.Error(), "InternalEndpoint") {
		t.Fatalf("未配置 InternalEndpoint 应报错, got %v", err)
	}
	if _, err := iv.List(ctx, "p", "", 0); err == nil || !strings.Contains(err.Error(), "InternalEndpoint") {
		t.Fatalf("List 未配置 InternalEndpoint 应报错, got %v", err)
	}

	// 签名线四方法产物与原实例完全一致
	u1, _ := s.UploadURL(ctx, "k.txt", 300)
	u2, _ := iv.UploadURL(ctx, "k.txt", 300)
	if u1 != u2 {
		t.Fatalf("视图签名线不一致:\n s=%s\niv=%s", u1, u2)
	}
	a1, _ := s.AccessURL(ctx, "k.txt", 300, "")
	a2, _ := iv.AccessURL(ctx, "k.txt", 300, "")
	if a1 != a2 {
		t.Fatalf("视图 AccessURL 不一致:\n s=%s\niv=%s", a1, a2)
	}
	if s.PublicBase() != iv.PublicBase() {
		t.Fatalf("视图 PublicBase 不一致: %s vs %s", s.PublicBase(), iv.PublicBase())
	}
	tk1, _ := s.UploadTicket(ctx, "temp", "a.png", 600, 0, 0)
	tk2, _ := iv.UploadTicket(ctx, "temp", "a.png", 600, 0, 0)
	if tk1.UploadURL != tk2.UploadURL || len(tk1.FormData) != len(tk2.FormData) {
		t.Fatal("视图票据目标/字段数不一致")
	}
	for k, v := range tk1.FormData {
		if tk2.FormData[k] != v {
			t.Fatalf("视图票据字段 %s 不一致", k)
		}
	}

	// 幂等：视图上再调 Internal() 行为不变（仍报未配置）
	iv2 := iv.Internal()
	if _, err := iv2.Head(ctx, "k.txt"); err == nil || !strings.Contains(err.Error(), "InternalEndpoint") {
		t.Fatalf("Internal() 幂等后行为应不变, got %v", err)
	}

	// 配置内网端点后：视图操作线请求切到内网 host，原实例不受影响
	s.InternalEndpoint = "http://10.0.0.5:9000"
	iv3 := s.Internal().(*S3Storage)
	req, err := iv3.signedRequest(ctx, http.MethodHead, "k.txt", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.String(); got != "http://10.0.0.5:9000/bkt/k.txt" {
		t.Fatalf("视图操作线 host 未切内网: %s", got)
	}
	req2, err := s.signedRequest(ctx, http.MethodHead, "k.txt", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := req2.URL.String(); got != "http://127.0.0.1:9000/bkt/k.txt" {
		t.Fatalf("原实例操作线被视图污染: %s", got)
	}
}

// TestS3_ListQuery ListObjectsV2 参数组装三态 + query 进签名与 URL 同串
func TestS3_ListQuery(t *testing.T) {
	// max<=0 或 >1000 取 1000；prefix 空不带；after 空不带 start-after
	q := listQuery("", "", 0)
	if q["max-keys"] != "1000" || q["list-type"] != "2" {
		t.Fatalf("max 修正错: %v", q)
	}
	if _, ok := q["prefix"]; ok {
		t.Fatal("prefix 空不应带")
	}
	if _, ok := q["start-after"]; ok {
		t.Fatal("after 空不应带 start-after")
	}
	q = listQuery("", "k3", 5000)
	if q["max-keys"] != "1000" || q["start-after"] != "k3" {
		t.Fatalf("max>1000 应取 1000: %v", q)
	}
	q = listQuery("temp/", "", 100)
	if q["max-keys"] != "100" || q["prefix"] != "temp/" {
		t.Fatalf("正常态参数错: %v", q)
	}

	// query 进 CanonicalRequest 用 canonicalQuery（字典序 + 值内斜杠编码），URL 拼接同串
	s := NewS3("AK", "SK", "bkt", "http://127.0.0.1:9000")
	s.Now = func() time.Time { return fixedTime }
	req, err := s.signedRequest(context.Background(), http.MethodGet, "", nil, listQuery("temp/", "k2", 500), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "http://127.0.0.1:9000/bkt?list-type=2&max-keys=500&prefix=temp%2F&start-after=k2"
	if got := req.URL.String(); got != want {
		t.Fatalf("List URL query 不符:\n got=%s\nwant=%s", got, want)
	}
	if req.Header.Get("Authorization") == "" {
		t.Fatal("List 请求缺 Authorization 头签名")
	}
}

// TestS3_Operations 操作线全流程（httptest）：Put 请求头与响应处理、Get 元数据与
// 404 语义、Head 404、List XML 解析与 NextMarker
func TestS3_Operations(t *testing.T) {
	ctx := context.Background()
	var gotAuth, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/bkt/k.txt":
			gotCT = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/bkt/k.txt":
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("ETag", `"abc123"`)
			_, _ = w.Write([]byte("hello"))
		case r.URL.Path == "/bkt/miss.txt":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/bkt":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
<IsTruncated>true</IsTruncated>
<Contents><Key>k1</Key><Size>3</Size><ETag>&quot;e1&quot;</ETag><LastModified>2026-08-18T12:00:00.000Z</LastModified></Contents>
<Contents><Key>k2</Key><Size>5</Size><ETag>"e2"</ETag><LastModified>2026-08-18T13:00:00.000Z</LastModified></Contents>
</ListBucketResult>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	s := NewS3("AK", "SK", "bkt", srv.URL)

	// Put：Content-Type 头设置；头签名存在
	if err := s.Put(ctx, "k.txt", "text/plain", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if gotCT != "text/plain" {
		t.Fatalf("Put Content-Type=%s", gotCT)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("Put 缺头签名: %q", gotAuth)
	}

	// Get：meta 取响应头、body 流式返回、调用方关闭
	meta, body, err := s.Get(ctx, "k.txt")
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil || body == nil {
		t.Fatal("Get 应返回 meta 与 body")
	}
	if meta.Size != 5 || meta.ETag != "abc123" || meta.ContentType != "text/plain" {
		t.Fatalf("Get meta 不符: %+v", meta)
	}
	buf := make([]byte, 5)
	// 读满 5 字节时底层 Read 可能同时返回 (5, EOF)（io.Reader 契约允许），用 ReadFull
	if _, err := io.ReadFull(body, buf); err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if string(buf) != "hello" {
		t.Fatalf("Get body=%s", buf)
	}

	// Get/Head 404：nil,nil,nil 与 nil,nil 语义
	m2, b2, err := s.Get(ctx, "miss.txt")
	if err != nil || m2 != nil || b2 != nil {
		t.Fatalf("Get 404 应返回 nil,nil,nil: %v %v %v", m2, b2, err)
	}
	hm, err := s.Head(ctx, "miss.txt")
	if err != nil || hm != nil {
		t.Fatalf("Head 404 应返回 nil,nil: %v %v", hm, err)
	}
	ok, err := s.Exists(ctx, "miss.txt")
	if err != nil || ok {
		t.Fatalf("Exists miss 应 false: %v %v", ok, err)
	}

	// List：XML 解析（Key/Size/ETag/LastModified）+ IsTruncated + NextMarker=最后一条 Key
	listing, err := s.List(ctx, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Objects) != 2 {
		t.Fatalf("List 应解析 2 条: %+v", listing.Objects)
	}
	o1 := listing.Objects[0]
	if o1.Key != "k1" || o1.Size != 3 || o1.ETag != "e1" {
		t.Fatalf("首条不符: %+v", o1)
	}
	if !o1.LastModified.Equal(time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("LastModified 解析错: %v", o1.LastModified)
	}
	if !listing.IsTruncated || listing.NextMarker != "k2" {
		t.Fatalf("翻页标记错: truncated=%v marker=%s", listing.IsTruncated, listing.NextMarker)
	}
}

// TestS3_CopySignedHeaderVector Copy 的 x-amz-copy-source 进签名固定向量（AWS SigV4
// 文档要求签请求中全部 x-amz-* 头；MinIO 按声明列表验签，签上兼容）：断言
// SignedHeaders 四头字典序 + 独立重算 Signature（测试内手写 canonical，含新头行）；
// Put 的 Content-Type 保持不签（AWS SigV4 允许，已过真 MinIO 实测——与 OSS 侧差异
// 见 oss.go Put 注释）
func TestS3_CopySignedHeaderVector(t *testing.T) {
	var lastAuth, lastCopySrc, lastHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth, lastCopySrc, lastHost = r.Header.Get("Authorization"), r.Header.Get("x-amz-copy-source"), r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewS3("AK", "SK", "bkt", srv.URL)
	s.Now = func() time.Time { return fixedTime }
	if err := s.Copy(context.Background(), "src.txt", "dst.txt"); err != nil {
		t.Fatal(err)
	}
	if lastCopySrc != "/bkt/src.txt" {
		t.Fatalf("x-amz-copy-source 头值不符: %q", lastCopySrc)
	}

	// SignedHeaders 四头字典序 host < x-amz-content-sha256 < x-amz-copy-source < x-amz-date
	signedHeaders := "host;x-amz-content-sha256;x-amz-copy-source;x-amz-date"
	canonicalHeaders := "host:" + lastHost + "\nx-amz-content-sha256:UNSIGNED-PAYLOAD\nx-amz-copy-source:/bkt/src.txt\nx-amz-date:20260818T120000Z\n"
	cr := strings.Join([]string{"PUT", "/bkt/dst.txt", "", canonicalHeaders, signedHeaders, "UNSIGNED-PAYLOAD"}, "\n")
	sts := strings.Join([]string{"AWS4-HMAC-SHA256", "20260818T120000Z", "20260818/us-east-1/s3/aws4_request", sha256Hex([]byte(cr))}, "\n")
	h := hmacChain(t)
	k4 := h(h(h(h([]byte("AWS4SK"), []byte("20260818")), []byte("us-east-1")), []byte("s3")), []byte("aws4_request"))
	want := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=AK/20260818/us-east-1/s3/aws4_request,SignedHeaders=%s,Signature=%s",
		signedHeaders, hex.EncodeToString(h(k4, []byte(sts))))
	if lastAuth != want {
		t.Fatalf("Copy Authorization 不符:\n got=%s\nwant=%s", lastAuth, want)
	}
}

// TestDomainMatrixS3 s3 域名优先级矩阵全组合（表驱动，path-style 端点）：上传链
// AccessDomain > Endpoint 兜底、读取链 AccelerateDomain > AccessDomain > Endpoint 兜底。
// 2×2 组合（两域名各配/不配）一次跑全；断言 host 段 + 路径形态（兜底 path-style
// 路径拼 /bucket，CNAME 命中不拼——桶已绑在域名上）。补缺点：仅配 Accelerate 时
// 上传链走兜底；配 AccessDomain 时票据表单目标切 CNAME；兜底态 AccessURL 直断
func TestDomainMatrixS3(t *testing.T) {
	const fallback = "127.0.0.1:9000" // path-style 兜底 host（endpoint 裸 host，非标端口保留）
	cases := []struct {
		name      string
		access    string
		accel     string
		wantUp    string // 上传链期望 host
		wantUpP   string // 上传链期望路径（兜底拼 /bkt，CNAME 不拼）
		wantRead  string // 读取链期望 host
		wantReadP string // 读取链期望路径
	}{
		{"全不配-两链兜底endpoint", "", "", fallback, "/bkt/k1.txt", fallback, "/bkt/k1.txt"},
		{"仅Access-两链同命中CNAME", "https://cdn.example.com", "", "cdn.example.com", "/k1.txt", "cdn.example.com", "/k1.txt"},
		{"Access加Accelerate-上传Access读取Accelerate", "https://cdn.example.com", "https://fast.example.com", "cdn.example.com", "/k1.txt", "fast.example.com", "/k1.txt"},
		{"仅Accelerate-上传链不受影响仍兜底", "", "https://fast.example.com", fallback, "/bkt/k1.txt", "fast.example.com", "/k1.txt"},
	}
	ctx := context.Background()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewS3("AK", "SK", "bkt", "http://"+fallback)
			s.Now = func() time.Time { return fixedTime }
			s.AccessDomain = c.access
			s.AccelerateDomain = c.accel

			// 上传链：PUT 预签名——host 段 + 路径形态（http://host[:port]/...，host 取 authority 段）
			up, err := s.UploadURL(ctx, "k1.txt", 300)
			if err != nil {
				t.Fatal(err)
			}
			u := urlOf(t, up)
			if u.Host != c.wantUp {
				t.Errorf("UploadURL host = %q, want %q（url=%s）", u.Host, c.wantUp, up)
			}
			if u.Path != c.wantUpP {
				t.Errorf("UploadURL path = %q, want %q（url=%s）", u.Path, c.wantUpP, up)
			}

			// 上传链：POST 票据表单目标（桶根）——host 段同上传链
			tkt, err := s.UploadTicket(ctx, "temp", "a.png", 600, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := urlOf(t, tkt.UploadURL).Host; got != c.wantUp {
				t.Errorf("UploadTicket.UploadURL host = %q, want %q（url=%s）", got, c.wantUp, tkt.UploadURL)
			}

			// 读取链：GET 预签名——host 段 + 路径形态同上规则
			acc, err := s.AccessURL(ctx, "k1.txt", 300, "")
			if err != nil {
				t.Fatal(err)
			}
			au := urlOf(t, acc)
			if au.Host != c.wantRead {
				t.Errorf("AccessURL host = %q, want %q（url=%s）", au.Host, c.wantRead, acc)
			}
			if au.Path != c.wantReadP {
				t.Errorf("AccessURL path = %q, want %q（url=%s）", au.Path, c.wantReadP, acc)
			}
			if got := urlOf(t, s.PublicBase()).Host; got != c.wantRead {
				t.Errorf("PublicBase host = %q, want %q", got, c.wantRead)
			}
		})
	}
}

// TestS3_AccessURLContentDisposition 下载文件名参数进签名（SigV4 query 参数全集）
func TestS3_AccessURLContentDisposition(t *testing.T) {
	s := NewS3("AK", "SK", "bkt", "http://127.0.0.1:9000")
	s.Now = func() time.Time { return fixedTime }
	u, _ := s.AccessURL(context.Background(), "k.txt", 300, `attachment; filename="k.txt"`)
	if !strings.Contains(u, "response-content-disposition=") {
		t.Fatalf("URL 缺 response-content-disposition: %s", u)
	}
	sig := u[strings.Index(u, "X-Amz-Signature=")+len("X-Amz-Signature="):]
	params := map[string]string{
		"X-Amz-Algorithm":              "AWS4-HMAC-SHA256",
		"X-Amz-Credential":             "AK/20260818/us-east-1/s3/aws4_request",
		"X-Amz-Date":                   "20260818T120000Z",
		"X-Amz-Expires":                "300",
		"X-Amz-SignedHeaders":          "host",
		"response-content-disposition": `attachment; filename="k.txt"`,
	}
	cq := canonicalQuery(params)
	cr := strings.Join([]string{"GET", "/bkt/k.txt", cq, "host:127.0.0.1:9000", "", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sts := strings.Join([]string{"AWS4-HMAC-SHA256", "20260818T120000Z", "20260818/us-east-1/s3/aws4_request", sha256Hex([]byte(cr))}, "\n")
	h := func(k, d []byte) []byte { m := hmac.New(sha256.New, k); m.Write(d); return m.Sum(nil) }
	k4 := h(h(h(h([]byte("AWS4SK"), []byte("20260818")), []byte("us-east-1")), []byte("s3")), []byte("aws4_request"))
	if want := hex.EncodeToString(h(k4, []byte(sts))); sig != want {
		t.Fatalf("含 response 参数的签名不符:\n got=%s\nwant=%s", sig, want)
	}
}

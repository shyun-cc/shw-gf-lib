package storage

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// S3Storage S3 兼容 adapter（AWS SigV4，零厂商 SDK；MinIO/OSS-S3 兼容端点均可）。
// 域名三线模型：上传链（AccessDomain > Endpoint 兜底）、读取链（AccelerateDomain >
// AccessDomain > Endpoint 兜底）、操作线（Endpoint 默认 / InternalEndpoint 内网视图），
// 寻址双形态由 AddressingStyle 贯穿 URL 构造与 CanonicalRequest。
type S3Storage struct {
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	// Endpoint 服务商标准端点（如 http://127.0.0.1:9000、s3.cn-north-1.amazonaws.com.cn）：
	// region 推导源 + 三线兜底域名。必须填服务商标准端点——CNAME/CDN 域名走
	// AccessDomain/AccelerateDomain，混填此处会推导出错误 region（SignatureDoesNotMatch 难排查）
	Endpoint string
	// AccessDomain 可选：CNAME/CDN 直达域名（上传链①/读取链②；域名已绑定桶，路径不拼 bucket）
	AccessDomain string
	// AccelerateDomain 可选：读取最高优先域名（读取链①，只进读取线不进上传线）
	AccelerateDomain string
	// Protocol 可选：唯一协议事实源。默认 https；未配置时 endpoint 带 http:// 前缀则推导为
	// http（存量 MinIO http 端点兼容），显式配置后以配置为准
	Protocol string
	// InternalEndpoint 可选：内网端点（仅操作线 Internal 视图使用，签名线不受影响）
	InternalEndpoint string
	// AddressingStyle 寻址形态：""或"path-style"（默认，bucket 在路径，MinIO 原生）；
	// "virtual-hosted"（bucket 进 host，腾讯 COS 等需要）。URL 构造与 CanonicalRequest
	// 必须同形态切换——寻址进签名，两处不一致即 SignatureDoesNotMatch
	AddressingStyle string

	// Now 时间注入（签名固定向量取证）
	Now func() time.Time

	HTTPClient *http.Client

	// useInternal 内网视图标志（Internal() 置位：操作线走 InternalEndpoint，签名线不受影响）
	useInternal bool
}

// NewS3 构造（region 不再显式传入，一律 deriveRegion(endpoint) 推导 + us-east-1 兜底）
func NewS3(ak, sk, bucket, endpoint string) *S3Storage {
	return &S3Storage{AccessKeyID: ak, SecretAccessKey: sk, Bucket: bucket, Endpoint: endpoint}
}

// region 从 Endpoint 推导（deriveRegion：标准端点地域段可推导，其余 us-east-1 兜底）
func (s *S3Storage) region() string { return deriveRegion(s.Endpoint) }

func (s *S3Storage) scope(dateStr string) string {
	return dateStr + "/" + s.region() + "/s3/aws4_request"
}

// virtualHosted 是否 virtual-hosted 寻址（其余取值一律按 path-style 默认处理）
func (s *S3Storage) virtualHosted() bool {
	return s.AddressingStyle == "virtual-hosted"
}

// protocol 唯一协议事实源：显式 Protocol 优先；未配置时 endpoint 带 http:// 前缀推导为
// http，否则默认 https
func (s *S3Storage) protocol() string {
	if s.Protocol != "" {
		return s.Protocol
	}
	if strings.HasPrefix(s.Endpoint, "http://") {
		return "http"
	}
	return "https"
}

// ---- 寻址体系（URL 与 CanonicalRequest 共用同一产物）----

// s3Address 寻址产物：host 与 canonicalPath 同时进 URL 构造和 CanonicalRequest
// URI 段——寻址进签名，两处不一致即 SignatureDoesNotMatch；scheme 记录该链域名
// 自带的协议前缀（无前缀为空，urlOf 里按 Protocol > 链前缀 > 默认 统一）
type s3Address struct {
	host          string // 含非标端口（MinIO :9000 属签名 host 的一部分）
	canonicalPath string // path-style：/{bucket}[/{key}]；virtual-hosted/CNAME：/{key}（桶根 /）
	scheme        string // 该链域名自带协议前缀（http/https），无则空
}

// hostOnly 域名裸 host：剥协议前缀/尾斜杠/路径，保留端口（非标端口须保留进签名）
func hostOnly(endpoint string) string {
	host := normalizeDomain(endpoint)
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	return host
}

// schemeOf 域名自带协议前缀（http:// 或 https://），无前缀返回空
func schemeOf(domain string) string {
	switch {
	case strings.HasPrefix(domain, "http://"):
		return "http"
	case strings.HasPrefix(domain, "https://"):
		return "https"
	}
	return ""
}

// cnameAddress CNAME 直达域名寻址：域名已绑定桶，等同 virtual-hosted 形态——host 为
// 域名本体、路径不拼 bucket（key 为空时即桶根 /）
func cnameAddress(cname, key string) s3Address {
	return s3Address{host: hostOnly(cname), canonicalPath: "/" + key, scheme: schemeOf(cname)}
}

// expandAddress Endpoint 形态端点按寻址形态展开（InternalEndpoint 同用此展开）：
// path-style 默认 bucket 在路径；virtual-hosted bucket 进 host。key 为空表示桶根
// （List / POST 表单目标）：path-style 即 /{bucket}，virtual-hosted 即 /
func (s *S3Storage) expandAddress(endpoint, key string) s3Address {
	host := hostOnly(endpoint)
	scheme := schemeOf(endpoint)
	if s.virtualHosted() {
		return s3Address{host: s.Bucket + "." + host, canonicalPath: "/" + key, scheme: scheme}
	}
	if key == "" {
		return s3Address{host: host, canonicalPath: "/" + s.Bucket, scheme: scheme}
	}
	return s3Address{host: host, canonicalPath: "/" + s.Bucket + "/" + key, scheme: scheme}
}

// endpointAddress 默认链寻址：Endpoint 按寻址形态展开
func (s *S3Storage) endpointAddress(key string) s3Address {
	return s.expandAddress(s.Endpoint, key)
}

// uploadAddress 上传链寻址（票据表单目标 / PUT 预签名 host）：AccessDomain > Endpoint 兜底
func (s *S3Storage) uploadAddress(key string) s3Address {
	if hostOnly(s.AccessDomain) != "" {
		return cnameAddress(s.AccessDomain, key)
	}
	return s.endpointAddress(key)
}

// readAddress 读取链寻址（AccessURL / PublicBase）：AccelerateDomain > AccessDomain >
// Endpoint 兜底
func (s *S3Storage) readAddress(key string) s3Address {
	for _, d := range []string{s.AccelerateDomain, s.AccessDomain} {
		if hostOnly(d) != "" {
			return cnameAddress(d, key)
		}
	}
	return s.endpointAddress(key)
}

// opAddress 操作线寻址（视图感知）：Internal 视图开启时走 InternalEndpoint（未配置即
// error 不发请求），否则默认 Endpoint；两者均按 AddressingStyle 展开
func (s *S3Storage) opAddress(key string) (s3Address, error) {
	if s.useInternal {
		if hostOnly(s.InternalEndpoint) == "" {
			return s3Address{}, fmt.Errorf("s3: InternalEndpoint 未配置")
		}
		return s.expandAddress(s.InternalEndpoint, key), nil
	}
	return s.endpointAddress(key), nil
}

// urlOf 完整 URL：协议（Protocol 显式配置即唯一事实源，否则用链域名自带前缀，再否则
// endpoint 前缀推导 / 默认 https）+ 寻址产物 + 可选 query（query 串与 CanonicalRequest
// 用同一 canonicalQuery 产物）
func (s *S3Storage) urlOf(a s3Address, query string) string {
	scheme := s.Protocol
	if scheme == "" {
		scheme = a.scheme
	}
	if scheme == "" {
		scheme = s.protocol()
	}
	u := scheme + "://" + a.host + a.canonicalPath
	if query != "" {
		u += "?" + query
	}
	return u
}

// ---- 签名线 ----

// buildPresignedURL SigV4 预签名（UNSIGNED-PAYLOAD；host 单头）。host 与 CanonicalURI
// 由 addr 注入（PUT 预签名走上传链、GET 走读取链，链选在调用方）；extraQuery 额外
// query 参数并进签名（response-content-disposition 等——签的是参数全集）
func (s *S3Storage) buildPresignedURL(method, key string, expireSeconds int, addr s3Address, extraQuery map[string]string) string {
	now := nowUTC(s.Now)
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")

	params := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    s.AccessKeyID + "/" + s.scope(dateStr),
		"X-Amz-Date":          dateTimeStr,
		"X-Amz-Expires":       fmt.Sprintf("%d", expireSeconds),
		"X-Amz-SignedHeaders": "host",
	}
	for k, v := range extraQuery {
		params[k] = v
	}
	cq := canonicalQuery(params)

	// canonical headers：每行以 \n 结束，整体后再一个空行（join 的空串元素）
	canonicalRequest := strings.Join([]string{
		method,
		addr.canonicalPath,
		cq,
		"host:" + addr.host,
		"",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		dateTimeStr,
		s.scope(dateStr),
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey("AWS4", s.SecretAccessKey, dateStr, s.region(), "s3", "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	return s.urlOf(addr, cq+"&X-Amz-Signature="+signature)
}

// UploadURL PUT 预签名（上传链：AccessDomain > Endpoint 兜底）
func (s *S3Storage) UploadURL(_ context.Context, key string, expireSeconds int) (string, error) {
	return s.buildPresignedURL("PUT", key, expireSeconds, s.uploadAddress(key), nil), nil
}

// AccessURL GET 预签名（读取链：AccelerateDomain > AccessDomain > Endpoint 兜底；
// contentDisposition 非空时作为 response-content-disposition 进签名 query——见接口注释）
func (s *S3Storage) AccessURL(_ context.Context, key string, expireSeconds int, contentDisposition string) (string, error) {
	var extra map[string]string
	if contentDisposition != "" {
		extra = map[string]string{"response-content-disposition": contentDisposition}
	}
	return s.buildPresignedURL("GET", key, expireSeconds, s.readAddress(key), extra), nil
}

// UploadTicket SigV4 POST policy 表单直传票据，完整 key 使用 eq 固定，允许同 key 重传。
func (s *S3Storage) UploadTicket(_ context.Context, directory, filename string, expireSeconds int, minSize, maxSize int64) (*UploadTicket, error) {
	now := nowUTC(s.Now)
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")
	key, err := uploadTicketKey(directory, filename)
	if err != nil {
		return nil, err
	}
	credential := s.AccessKeyID + "/" + s.scope(dateStr)

	// 最小条件集：success_action_status / x-amz-content-type 条件不加（那是 OSS 形态）
	conditions := []any{
		map[string]string{"bucket": s.Bucket},
		map[string]string{"x-amz-algorithm": "AWS4-HMAC-SHA256"},
		map[string]string{"x-amz-credential": credential},
		map[string]string{"x-amz-date": dateTimeStr},
		[]string{"eq", "$key", key},
	}
	if minSize > 0 || maxSize > 0 {
		max := maxSize
		if max <= 0 {
			max = 5 * 1024 * 1024 * 1024 // min>0 且 max<=0 时 5GB 兜底（同 oss 版逻辑）
		}
		conditions = append(conditions, []any{"content-length-range", minSize, max})
	}

	expiration := now.Add(time.Duration(expireSeconds) * time.Second).Format("2006-01-02T15:04:05.000Z")
	policyJSON, err := json.Marshal(map[string]any{"expiration": expiration, "conditions": conditions})
	if err != nil {
		return nil, err
	}
	stringToSign := base64.StdEncoding.EncodeToString(policyJSON)

	// POST policy 签名同四层派生：AWS4+SK → date → region → s3 → aws4_request（region=deriveRegion）
	signingKey := deriveSigningKey("AWS4", s.SecretAccessKey, dateStr, s.region(), "s3", "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// POST 表单目标在桶根：path-style 即 host/{bucket}，virtual-hosted/CNAME 即域名根
	return &UploadTicket{
		Filename:    filename,
		UploadURL:   s.urlOf(s.uploadAddress(""), ""),
		Method:      http.MethodPost,
		ContentType: mimeTypeOf(filename),
		FormData: map[string]string{
			"key":              key,
			"policy":           stringToSign,
			"x-amz-algorithm":  "AWS4-HMAC-SHA256",
			"x-amz-credential": credential,
			"x-amz-date":       dateTimeStr,
			"x-amz-signature":  signature,
		},
	}, nil
}

// PublicBase 公有直链前缀（读取链产物：AccelerateDomain > AccessDomain > Endpoint 兜底；
// attachment 直链拼接 PublicBase()+"/"+key，故三形态统一无尾斜杠）
func (s *S3Storage) PublicBase() string {
	return strings.TrimSuffix(s.urlOf(s.readAddress(""), ""), "/")
}

// ---- 操作线（头签名；视图感知 host：Internal 视图走 InternalEndpoint）----

// Head 元数据（不存在返回 nil,nil）
func (s *S3Storage) Head(ctx context.Context, key string) (*ObjectMeta, error) {
	req, err := s.signedRequest(ctx, http.MethodHead, key, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("s3 head %d", resp.StatusCode)
	}
	return &ObjectMeta{
		Size:        resp.ContentLength,
		ETag:        strings.Trim(resp.Header.Get("ETag"), `"`),
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// Copy 服务端拷贝（x-amz-copy-source 进签名——AWS SigV4 文档要求签请求中全部 x-amz-*
// 头；MinIO 按声明列表验签，签上兼容不影响验签通过）
func (s *S3Storage) Copy(ctx context.Context, srcKey, dstKey string) error {
	req, err := s.signedRequest(ctx, http.MethodPut, dstKey, nil, nil,
		map[string]string{"x-amz-copy-source": "/" + s.Bucket + "/" + srcKey})
	if err != nil {
		return err
	}
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 copy %d: %s", resp.StatusCode, body)
	}
	return nil
}

// Exists 存在性
func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	meta, err := s.Head(ctx, key)
	if err != nil {
		return false, err
	}
	return meta != nil, nil
}

// Delete 删除（幂等：404/204 都算成功）
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	req, err := s.signedRequest(ctx, http.MethodDelete, key, nil, nil, nil)
	if err != nil {
		return err
	}
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("s3 delete %d: %s", resp.StatusCode, body)
}

// Put 服务端流式上传（头签名 PUT；UNSIGNED-PAYLOAD；Content-Type 头设置但不参与
// 签名——AWS SigV4 允许只签 host 与 x-amz-* 头，已过真 MinIO 实测；与 OSS 侧不同：
// OSS V4 强制请求中存在的 Content-Type 必签，见 oss.go Put 注释，故两侧处理有差异）
func (s *S3Storage) Put(ctx context.Context, key, contentType string, body io.Reader) error {
	req, err := s.signedRequest(ctx, http.MethodPut, key, body, nil, nil)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 put %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// Get 服务端读取（404 返回 nil,nil,nil；meta 取响应头；body 由调用方关闭）
func (s *S3Storage) Get(ctx context.Context, key string) (*ObjectMeta, io.ReadCloser, error) {
	req, err := s.signedRequest(ctx, http.MethodGet, key, nil, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, nil, fmt.Errorf("s3 get %d: %s", resp.StatusCode, respBody)
	}
	return &ObjectMeta{
		Size:        resp.ContentLength,
		ETag:        strings.Trim(resp.Header.Get("ETag"), `"`),
		ContentType: resp.Header.Get("Content-Type"),
	}, resp.Body, nil
}

// listQuery ListObjectsV2 参数组装（start-after 仅 after 非空时带；max<=0 或 >1000 取 1000）
func listQuery(prefix, after string, max int) map[string]string {
	if max <= 0 || max > 1000 {
		max = 1000
	}
	q := map[string]string{"list-type": "2", "max-keys": fmt.Sprintf("%d", max)}
	if prefix != "" {
		q["prefix"] = prefix
	}
	if after != "" {
		q["start-after"] = after
	}
	return q
}

// List ListObjectsV2 前缀分页（after 翻页用 start-after 按字典序续列；NextMarker=
// IsTruncated 时最后一条 Key，传回下一轮 after）
func (s *S3Storage) List(ctx context.Context, prefix, after string, max int) (*ObjectListing, error) {
	req, err := s.signedRequest(ctx, http.MethodGet, "", nil, listQuery(prefix, after, max), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("s3 list %d: %s", resp.StatusCode, body)
	}

	var lr struct {
		Contents []struct {
			Key          string `xml:"Key"`
			Size         int64  `xml:"Size"`
			ETag         string `xml:"ETag"`
			LastModified string `xml:"LastModified"`
		} `xml:"Contents"`
		IsTruncated bool `xml:"IsTruncated"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, err
	}

	listing := &ObjectListing{IsTruncated: lr.IsTruncated}
	for _, c := range lr.Contents {
		// 与 oss 侧行为对齐：解析失败显式报错，不静默零值（LastModified 坏值多为
		// 响应被网关改写等异常，静默零值会把异常吞成错误数据）
		lm, err := time.Parse(time.RFC3339, c.LastModified)
		if err != nil {
			return nil, fmt.Errorf("s3 list lastModified %q: %w", c.LastModified, err)
		}
		listing.Objects = append(listing.Objects, ObjectSummary{
			Key:          c.Key,
			Size:         c.Size,
			ETag:         strings.Trim(c.ETag, `"`),
			LastModified: lm,
		})
	}
	if lr.IsTruncated && len(listing.Objects) > 0 {
		listing.NextMarker = listing.Objects[len(listing.Objects)-1].Key
	}
	return listing, nil
}

// Internal 内网操作视图：操作线走 InternalEndpoint（未配置即 error 不发请求），签名线
// 四方法（UploadTicket/UploadURL/AccessURL/PublicBase）与原实例产物完全一致；幂等
// 返回自身
func (s *S3Storage) Internal() Storage {
	if s.useInternal {
		return s
	}
	v := *s // 浅拷贝（字段全为 string/func/指针/bool，无共享可变状态）
	v.useInternal = true
	return &v
}

// signedRequest 操作线头签名请求（UNSIGNED-PAYLOAD；基础签名头集 host/x-amz-content-sha256/
// x-amz-date）。query 进 CanonicalRequest 用 canonicalQuery（字典序 + 值内斜杠编码），
// URL 拼接用同一串；host 与 CanonicalURI 来自视图感知的 opAddress。
// extraHeaders 额外签名头（头名转小写、值原样）：AWS SigV4 要求签请求中全部 x-amz-*
// 头——Copy 的 x-amz-copy-source 经此并入；nil 时签名头集保持基础三头，行为与产物逐字节不变
func (s *S3Storage) signedRequest(ctx context.Context, method, key string, body io.Reader, query map[string]string, extraHeaders map[string]string) (*http.Request, error) {
	now := nowUTC(s.Now)
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")
	addr, err := s.opAddress(key)
	if err != nil {
		return nil, err
	}

	cq := ""
	if len(query) > 0 {
		cq = canonicalQuery(query)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.urlOf(addr, cq), body)
	if err != nil {
		return nil, err
	}
	req.Host = addr.host
	req.Header.Set("X-Amz-Date", dateTimeStr)
	req.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")

	// 签名头集动态构造：基础三头 + 额外头并集，sort.Strings 字典序后同时产出
	// canonical headers（每头一行 name:value\n，name 小写、值原样）与 SignedHeaders
	// （分号连接同序）——两处同集同序，必然严格一致；无额外头时与旧三头手拼串逐字节相同
	headers := map[string]string{
		"host":                 addr.host,
		"x-amz-content-sha256": "UNSIGNED-PAYLOAD",
		"x-amz-date":           dateTimeStr,
	}
	for k, v := range extraHeaders {
		lk := strings.ToLower(k)
		headers[lk] = v
		req.Header.Set(lk, v)
	}
	signedHeaders := make([]string, 0, len(headers))
	for k := range headers {
		signedHeaders = append(signedHeaders, k)
	}
	sort.Strings(signedHeaders)
	var ch strings.Builder
	for _, k := range signedHeaders {
		ch.WriteString(k)
		ch.WriteString(":")
		ch.WriteString(headers[k])
		ch.WriteString("\n")
	}
	canonicalHeaders := ch.String()
	canonicalRequest := strings.Join([]string{
		method, addr.canonicalPath, cq, canonicalHeaders,
		strings.Join(signedHeaders, ";"), "UNSIGNED-PAYLOAD",
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", dateTimeStr, s.scope(dateStr), sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := deriveSigningKey("AWS4", s.SecretAccessKey, dateStr, s.region(), "s3", "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s,SignedHeaders=%s,Signature=%s",
		s.AccessKeyID+"/"+s.scope(dateStr), strings.Join(signedHeaders, ";"), signature))
	return req, nil
}

func (s *S3Storage) do(req *http.Request) (*http.Response, error) {
	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

var _ Storage = (*S3Storage)(nil)

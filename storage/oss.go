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
	"strconv"
	"strings"
	"time"
)

// OssStorage 阿里云 OSS adapter（OSS4-HMAC-SHA256 V4 签名，自主复刻零厂商 SDK）
type OssStorage struct {
	AccessKeyID     string
	AccessKeySecret string
	Bucket          string
	// Endpoint 服务商标准端点（如 https://oss-cn-hangzhou.aliyuncs.com）：上传链/读取链的
	// 兜底 host（bucket.endpoint 形态）、操作线默认 host、签名 region 的唯一推导源——
	// 配了 CNAME/CDN 域名也不覆盖它（保证 region 永远可推导，误配 CNAME 以文档契约封堵）
	Endpoint string
	// AccessDomain 可选：CNAME 直达/CDN 域名（上传链第一优先、读取链第二优先）。
	// 为完整 host 不拼 bucket 前缀——CNAME 形态，对齐官方 SDK buildURLV4 的 CNAME 分支
	AccessDomain string
	// AccelerateDomain 可选：读取链最高优先域名（CDN 等）。完整 host 不拼 bucket，与 AccessDomain 同形态
	AccelerateDomain string
	// Protocol 产物 URL 协议（空默认 https）。唯一协议事实源：Endpoint/域名自带的协议前缀一律剥掉，以此为准
	Protocol string
	// InternalEndpoint 可选：内网操作端点（完整 host，仅 Internal() 视图的操作线使用；不参与 region 推导）
	InternalEndpoint string
	// Now 时间注入（签名固定向量取证）
	Now func() time.Time

	HTTPClient *http.Client

	// useInternal 内网视图标志（仅 Internal() 产物为 true）：操作线七方法经 opHost 切内网端点，签名线不受影响
	useInternal bool
}

// NewOss 构造（v0.8.0 BREAKING：region 参数移除，改由 Endpoint 推导——deriveRegion 见共享层，推导不出 us-east-1 兜底）
func NewOss(ak, sk, bucket, endpoint string) *OssStorage {
	return &OssStorage{AccessKeyID: ak, AccessKeySecret: sk, Bucket: bucket, Endpoint: endpoint}
}

// ---- 域名链（对齐 PHP hyperf-lib access_domain/accelerate_domain 双链设计）----

// host Endpoint 兜底形态：bucket.endpoint（剥协议前缀/尾斜杠）
func (o *OssStorage) host() string {
	return o.Bucket + "." + normalizeDomain(o.Endpoint)
}

// region 签名 region：一律从公网 Endpoint 推导（InternalEndpoint 不参与推导——内网与公网同地域，
// 且 CNAME/CDN 不覆盖 Endpoint，保证推导源永远是标准形态）
func (o *OssStorage) region() string {
	return deriveRegion(o.Endpoint)
}

// protocol 协议前缀（空默认 https，唯一协议事实源）
func (o *OssStorage) protocol() string {
	if o.Protocol == "" {
		return "https"
	}
	return o.Protocol
}

// uploadHost 上传链（UploadTicket 的 UploadURL、UploadURL 的 PUT 预签名 host）：
// AccessDomain > Endpoint 兜底（bucket.endpoint）。AccessDomain 为完整 host 不拼 bucket——CNAME 形态
func (o *OssStorage) uploadHost() string {
	return resolveHost([]string{normalizeDomain(o.AccessDomain)}, o.host())
}

// readHost 读取链访问域名（AccessURL URL host、PublicBase）：AccelerateDomain > AccessDomain > bucket.endpoint。
// 与 PHP AbstractAdapter 同构；PublicBase 随读取链——配了域名后 attachment 公有直链自动获得 CDN（D7）
func (o *OssStorage) readHost() string {
	return resolveHost([]string{normalizeDomain(o.AccelerateDomain), normalizeDomain(o.AccessDomain)}, o.host())
}

// opHost 操作线 host（Put/Get/Head/Copy/Exists/Delete/List 统一入口）：
// 视图标志开启时返回 normalizeDomain(InternalEndpoint)，未配置即 error——用户明确写了内网，
// 配漏必须炸出来，不静默回落公网；非视图时固定 bucket.endpoint（管理面不吃 CNAME/CDN，对齐 D1）
func (o *OssStorage) opHost() (string, error) {
	if o.useInternal {
		h := normalizeDomain(o.InternalEndpoint)
		if h == "" {
			return "", fmt.Errorf("oss: InternalEndpoint 未配置，内网操作视图不可用")
		}
		return h, nil
	}
	return o.host(), nil
}

// ---- 签名线（签名结构固定：CanonicalRequest 七段 / additional-headers=host / UNSIGNED-PAYLOAD，仅 host 与 region 来源变）----

func (o *OssStorage) scope(dateStr string) string {
	return dateStr + "/" + o.region() + "/oss/aliyun_v4_request"
}

// presignParams V4 预签名五参数（按字典序恰好有序）
func (o *OssStorage) presignParams(dateTimeStr, dateStr string, expires int) map[string]string {
	return map[string]string{
		"x-oss-additional-headers": "host",
		"x-oss-credential":         o.AccessKeyID + "/" + o.scope(dateStr),
		"x-oss-date":               dateTimeStr,
		"x-oss-expires":            fmt.Sprintf("%d", expires),
		"x-oss-signature-version":  "OSS4-HMAC-SHA256",
	}
}

// buildV4SignedUrl 预签名 URL（UNSIGNED-PAYLOAD；signHost 单头签名；urlHost 用于最终访问域名；
// extraQuery 额外 query 参数并进签名——response-content-disposition 等，签的是参数全集）
func (o *OssStorage) buildV4SignedUrl(method, key string, expireSeconds int, signHost, urlHost string, extraQuery map[string]string) string {
	now := nowUTC(o.Now)
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")

	params := o.presignParams(dateTimeStr, dateStr, expireSeconds)
	for k, v := range extraQuery {
		params[k] = v
	}
	cq := canonicalQuery(params)

	encodedKey := uriEncode(key)
	// CanonicalURI 带 bucket 前缀：OSS V4 服务端一律按 /{bucket}/{key} 验签，
	// 与请求实际用虚拟主机风格还是 CNAME 域名无关（官方示例 /examplebucket/exampleobject）。
	// URL 路径段仍只有 key——bucket 在 host 里（Issue #79）
	canonicalRequest := strings.Join([]string{
		method,
		"/" + o.Bucket + "/" + encodedKey,
		cq,
		"host:" + signHost,
		"",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	stringToSign := strings.Join([]string{
		"OSS4-HMAC-SHA256",
		dateTimeStr,
		o.scope(dateStr),
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey("aliyun_v4", o.AccessKeySecret, dateStr, o.region(), "oss", "aliyun_v4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	return o.protocol() + "://" + urlHost + "/" + encodedKey + "?" + cq + "&x-oss-signature=" + signature
}

// UploadURL PUT 预签名（上传链：AccessDomain > bucket.endpoint）
func (o *OssStorage) UploadURL(_ context.Context, key string, expireSeconds int) (string, error) {
	return o.buildV4SignedUrl("PUT", key, expireSeconds, o.uploadHost(), o.uploadHost(), nil), nil
}

// AccessURL GET 预签名（读取链：AccelerateDomain > AccessDomain > bucket.endpoint；
// GET 一律按公网 bucket.endpoint 签名，最终访问域名仍按读取链选择；
// contentDisposition 非空时作为 response-content-disposition 进签名 query——见接口注释）
func (o *OssStorage) AccessURL(_ context.Context, key string, expireSeconds int, contentDisposition string) (string, error) {
	var extra map[string]string
	if contentDisposition != "" {
		extra = map[string]string{"response-content-disposition": contentDisposition}
	}
	return o.buildV4SignedUrl("GET", key, expireSeconds, o.host(), o.readHost(), extra), nil
}

// UploadTicket PostObject V4 上传票据（UploadURL 走上传链）
func (o *OssStorage) UploadTicket(_ context.Context, directory, filename string, expireSeconds int, minSize, maxSize int64) (*UploadTicket, error) {
	now := nowUTC(o.Now)
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")
	key, err := uploadTicketKey(directory, filename)
	if err != nil {
		return nil, err
	}
	credential := o.AccessKeyID + "/" + o.scope(dateStr)
	mimeType := mimeTypeOf(filename)

	conditions := []any{
		map[string]string{"bucket": o.Bucket},
		map[string]string{"x-oss-signature-version": "OSS4-HMAC-SHA256"},
		map[string]string{"x-oss-credential": credential},
		map[string]string{"x-oss-date": dateTimeStr},
		[]string{"eq", "$key", key},
		[]string{"eq", "$success_action_status", "200"},
		[]string{"eq", "$x-oss-content-type", mimeType},
	}
	if maxSize > 0 || minSize > 0 {
		max := maxSize
		if max <= 0 {
			max = 5 * 1024 * 1024 * 1024
		}
		conditions = append(conditions, []any{"content-length-range", minSize, max})
	}

	expiration := now.Add(time.Duration(expireSeconds) * time.Second).Format("2006-01-02T15:04:05.000Z")
	policyJSON, err := json.Marshal(map[string]any{"expiration": expiration, "conditions": conditions})
	if err != nil {
		return nil, err
	}
	stringToSign := base64.StdEncoding.EncodeToString(policyJSON)

	signingKey := deriveSigningKey("aliyun_v4", o.AccessKeySecret, dateStr, o.region(), "oss", "aliyun_v4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	ticket := &UploadTicket{
		Filename:    filename,
		UploadURL:   o.protocol() + "://" + o.uploadHost(),
		Method:      http.MethodPost,
		ContentType: mimeType,
		FormData: map[string]string{
			"key":                     key,
			"policy":                  stringToSign,
			"x-oss-signature-version": "OSS4-HMAC-SHA256",
			"x-oss-credential":        credential,
			"x-oss-date":              dateTimeStr,
			"x-oss-signature":         signature,
			"success_action_status":   "200",
			"x-oss-content-type":      mimeType,
		},
	}
	return ticket, nil
}

// ---- 操作线（七方法统一经 opHost 取 host，Internal() 视图切内网端点）----

// Put 服务端流式上传（头签名 PUT：body 直接作请求体 UNSIGNED-PAYLOAD；
// Content-Type 非空时进签名头集——OSS V4 规定请求中存在的 Content-Type 必须参与
// 签名，见官方文档「在Header中包含签名」，canonical headers 字典序
// content-type < host < x-oss-content-sha256 < x-oss-date）
func (o *OssStorage) Put(ctx context.Context, key, contentType string, body io.Reader) error {
	var extra map[string]string
	if contentType != "" {
		extra = map[string]string{"content-type": contentType}
	}
	req, err := o.signedRequest(ctx, http.MethodPut, key, body, nil, extra)
	if err != nil {
		return err
	}
	resp, err := o.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("oss put %d: %s", resp.StatusCode, b)
	}
	return nil
}

// Get 服务端读取（头签名 GET；404 返回 nil,nil,nil 与 Head 语义对齐；meta 从响应头取，body 由调用方关闭）
func (o *OssStorage) Get(ctx context.Context, key string) (*ObjectMeta, io.ReadCloser, error) {
	req, err := o.signedRequest(ctx, http.MethodGet, key, nil, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := o.do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, nil, fmt.Errorf("oss get %d: %s", resp.StatusCode, b)
	}
	meta := &ObjectMeta{
		Size:        resp.ContentLength,
		ETag:        strings.Trim(resp.Header.Get("ETag"), `"`),
		ContentType: resp.Header.Get("Content-Type"),
	}
	return meta, resp.Body, nil
}

// Head 元数据（不存在返回 nil,nil）
func (o *OssStorage) Head(ctx context.Context, key string) (*ObjectMeta, error) {
	req, err := o.signedRequest(ctx, http.MethodHead, key, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oss head %d", resp.StatusCode)
	}
	return &ObjectMeta{
		Size:        resp.ContentLength,
		ETag:        strings.Trim(resp.Header.Get("ETag"), `"`),
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// Copy 拷贝（x-oss-copy-source 进签名——OSS V4 规定全部 x-oss-* 头参与签名，
// 见官方文档「在Header中包含签名」）
func (o *OssStorage) Copy(ctx context.Context, srcKey, dstKey string) error {
	req, err := o.signedRequest(ctx, http.MethodPut, dstKey, nil, nil,
		map[string]string{"x-oss-copy-source": "/" + o.Bucket + "/" + srcKey})
	if err != nil {
		return err
	}
	resp, err := o.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("oss copy %d: %s", resp.StatusCode, body)
	}
	return nil
}

// Exists 存在性
func (o *OssStorage) Exists(ctx context.Context, key string) (bool, error) {
	meta, err := o.Head(ctx, key)
	if err != nil {
		return false, err
	}
	return meta != nil, nil
}

// Delete 删除
func (o *OssStorage) Delete(ctx context.Context, key string) error {
	req, err := o.signedRequest(ctx, http.MethodDelete, key, nil, nil, nil)
	if err != nil {
		return err
	}
	resp, err := o.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 404/204 都算成功（幂等）
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("oss delete %d: %s", resp.StatusCode, body)
}

// listBucketResultXML GetBucket V2 响应（列举最小集：Contents/IsTruncated/NextMarker）
type listBucketResultXML struct {
	IsTruncated bool   `xml:"IsTruncated"`
	NextMarker  string `xml:"NextMarker"`
	Contents    []struct {
		Key          string `xml:"Key"`
		Size         int64  `xml:"Size"`
		ETag         string `xml:"ETag"`
		LastModified string `xml:"LastModified"`
	} `xml:"Contents"`
}

// List 前缀分页列举（GetBucket V2：头签名 GET /?list-type=2；marker 仅 after 非空时带；
// max<=0 或 >1000 取 1000——OSS 单页上限）。NextMarker 空且 IsTruncated 时取末条 Key 续页
func (o *OssStorage) List(ctx context.Context, prefix, after string, max int) (*ObjectListing, error) {
	if max <= 0 || max > 1000 {
		max = 1000
	}
	query := map[string]string{
		"list-type": "2",
		"max-keys":  strconv.Itoa(max),
	}
	if prefix != "" {
		query["prefix"] = prefix
	}
	if after != "" {
		query["marker"] = after
	}

	req, err := o.signedRequest(ctx, http.MethodGet, "", nil, query, nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("oss list %d: %s", resp.StatusCode, b)
	}

	var result listBucketResultXML
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	listing := &ObjectListing{IsTruncated: result.IsTruncated, NextMarker: result.NextMarker}
	if listing.NextMarker == "" && listing.IsTruncated && len(result.Contents) > 0 {
		listing.NextMarker = result.Contents[len(result.Contents)-1].Key
	}
	for _, c := range result.Contents {
		// OSS 返回 ISO8601 带毫秒（.000Z）——RFC3339 布局无小数秒位但 Parse 自动接受
		lm, err := time.Parse(time.RFC3339, c.LastModified)
		if err != nil {
			return nil, fmt.Errorf("oss list lastModified %q: %w", c.LastModified, err)
		}
		listing.Objects = append(listing.Objects, ObjectSummary{
			Key:          c.Key,
			Size:         c.Size,
			ETag:         strings.Trim(c.ETag, `"`),
			LastModified: lm,
		})
	}
	return listing, nil
}

// Internal 内网操作视图：浅拷贝 + 视图标志——操作线七方法经 opHost 切到 InternalEndpoint
// （未配置即 error 不发请求，不静默回落公网）；签名线四方法（UploadTicket/UploadURL/AccessURL/
// PublicBase）走域名链不经 opHost，产物与原实例完全一致（永不使用内网域名）；再调 Internal() 幂等返回自身
func (o *OssStorage) Internal() Storage {
	if o.useInternal {
		return o
	}
	view := *o
	view.useInternal = true
	return &view
}

// signedRequest 头签名版请求（Put/Get/Head/Copy/Delete/List 操作线共用骨架）。
// query 进 CanonicalRequest 的 query 段（canonicalQuery 字典序、双侧编码——斜杠不豁免），
// URL 实际拼接用同一串，两处必然一致；host 经 opHost（视图感知）。
// extraHeaders 额外签名头（头名转小写、值原样）：OSS V4 规定请求中存在的 Content-Type
// 与全部 x-oss-* 头必须参与签名（官方文档「在Header中包含签名」）——Put 的 Content-Type、
// Copy 的 x-oss-copy-source 经此并入；nil 时签名头集保持基础三头，行为与产物逐字节不变
func (o *OssStorage) signedRequest(ctx context.Context, method, key string, body io.Reader, query map[string]string, extraHeaders map[string]string) (*http.Request, error) {
	now := nowUTC(o.Now)
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")
	host, err := o.opHost()
	if err != nil {
		return nil, err
	}
	cq := ossCanonicalQuery(query)
	encodedKey := uriEncode(key)
	target := o.protocol() + "://" + host + "/" + encodedKey
	if cq != "" {
		target += "?" + cq
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-oss-date", dateTimeStr)
	req.Header.Set("x-oss-content-sha256", "UNSIGNED-PAYLOAD")

	// CanonicalRequest：additional-headers host + x-oss-content-sha256（按需）。
	// 签名头集动态构造：基础三头 + 额外头并集，sort.Strings 字典序后同时产出
	// canonical headers（每头一行 name:value\n，name 小写、值原样）与 AdditionalHeaders
	// （分号连接同序）——两处同集同序，必然严格一致
	headers := map[string]string{
		"host":                 host,
		"x-oss-content-sha256": "UNSIGNED-PAYLOAD",
		"x-oss-date":           dateTimeStr,
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
	// CanonicalURI：/{bucket}/{key}（key 为空的 List 即 /{bucket}/）——虚拟主机风格下
	// OSS 服务端仍按带 bucket 的路径验签，签 /{key} 会 403 SignatureDoesNotMatch（Issue #79）
	canonicalRequest := strings.Join([]string{
		method, "/" + o.Bucket + "/" + encodedKey, cq, canonicalHeaders,
		strings.Join(signedHeaders, ";"), "UNSIGNED-PAYLOAD",
	}, "\n")

	stringToSign := strings.Join([]string{
		"OSS4-HMAC-SHA256", dateTimeStr, o.scope(dateStr), sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := deriveSigningKey("aliyun_v4", o.AccessKeySecret, dateStr, o.region(), "oss", "aliyun_v4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Authorization 的签名头列表字段名为 AdditionalHeaders：OSS V4 header 签名只认该字段，
	// 用 AWS 风格的 SignedHeaders 会被判为未知参数 → 400 InvalidArgument（Issue #79）
	req.Header.Set("Authorization", fmt.Sprintf(
		"OSS4-HMAC-SHA256 Credential=%s,AdditionalHeaders=%s,Signature=%s",
		o.AccessKeyID+"/"+o.scope(dateStr), strings.Join(signedHeaders, ";"), signature))
	return req, nil
}

// PublicBase 公有直链前缀（读取链产物：Accelerate > Access > bucket.endpoint——D7，
// 未配置可选域名时与历史形态逐字节一致）
func (o *OssStorage) PublicBase() string { return o.protocol() + "://" + o.readHost() }

func (o *OssStorage) do(req *http.Request) (*http.Response, error) {
	client := o.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

// mimeTypeOf 简表（附件场景常用）
func mimeTypeOf(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".png"):
		return "image/png"
	case strings.HasSuffix(filename, ".jpg"), strings.HasSuffix(filename, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(filename, ".gif"):
		return "image/gif"
	case strings.HasSuffix(filename, ".webp"):
		return "image/webp"
	case strings.HasSuffix(filename, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(filename, ".mov"):
		return "video/quicktime"
	case strings.HasSuffix(filename, ".m4v"):
		return "video/x-m4v"
	case strings.HasSuffix(filename, ".webm"):
		return "video/webm"
	case strings.HasSuffix(filename, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(filename, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(filename, ".zip"):
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

var _ Storage = (*OssStorage)(nil)

// ossCanonicalQuery OSS V4 空值子资源仅写名称（versioning），区别于 AWS 的 name=。
func ossCanonicalQuery(params map[string]string) string {
	parts := strings.Split(canonicalQuery(params), "&")
	for i := range parts {
		parts[i] = strings.TrimSuffix(parts[i], "=")
	}
	return strings.Join(parts, "&")
}

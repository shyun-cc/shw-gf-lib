// Package storage 对象存储抽象：接口 + oss/s3 双 adapter 自主复刻（零厂商 SDK）。
//
// 接口三线：签名线（UploadTicket/UploadURL/AccessURL/PublicBase 前端直传直链）+
// 操作线（Put/Get/Head/Copy/Exists/Delete/List 服务端传输与元数据）+
// 视图（Internal 内网操作视图，操作线走 InternalEndpoint）。
package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ObjectMeta 对象元数据
type ObjectMeta struct {
	Size        int64
	ETag        string // 非分片上传时为内容 md5 hex
	ContentType string
}

// UploadTicket POST policy 表单直传票据
type UploadTicket struct {
	Filename    string
	UploadURL   string // 表单提交目标（host）
	Method      string // POST
	FormData    map[string]string
	ContentType string
}

// ObjectSummary 列举条目
type ObjectSummary struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
}

// ObjectListing 列举结果（前缀分页）
type ObjectListing struct {
	Objects     []ObjectSummary
	IsTruncated bool
	NextMarker  string // IsTruncated 时翻页用（传回 List 的 after）
}

// Storage 对象存储接口（oss/s3 adapter 实现）
type Storage interface {
	// UploadTicket 生成固定完整 key 的 PostObject 直传票据，允许有效期内同 key 重传。
	// filename 不得为空，directory/filename 不允许 ${...} 模板或无效 UTF-8。
	UploadTicket(ctx context.Context, directory, filename string, expireSeconds int, minSize, maxSize int64) (*UploadTicket, error)
	// UploadURL 生成 PUT 预签名 URL（前端直传）
	UploadURL(ctx context.Context, key string, expireSeconds int) (string, error)
	// AccessURL 生成 GET 访问 URL（预签名或公有读直链）。contentDisposition 非空时
	// 作为 response-content-disposition 进预签名 query（服务端下发 Content-Disposition
	// 响应头，如下载文件名）；值为完整透传（RFC 5987 编码等拼装职责在调用方），
	// 空串行为与无参数一致
	AccessURL(ctx context.Context, key string, expireSeconds int, contentDisposition string) (string, error)
	// Head 元数据（不存在返回 nil,nil）
	Head(ctx context.Context, key string) (*ObjectMeta, error)
	// Copy 拷贝对象
	Copy(ctx context.Context, srcKey, dstKey string) error
	// Exists 存在性
	Exists(ctx context.Context, key string) (bool, error)
	// Delete 删除（幂等）
	Delete(ctx context.Context, key string) error
	// List 前缀分页列举（after 为翻页 marker，对应 ObjectListing.NextMarker）
	List(ctx context.Context, prefix, after string, max int) (*ObjectListing, error)
	// Put 服务端流式上传
	Put(ctx context.Context, key, contentType string, body io.Reader) error
	// Get 服务端读取（内容流 + 元数据；body 由调用方关闭）
	Get(ctx context.Context, key string) (*ObjectMeta, io.ReadCloser, error)
	// PublicBase 公有读直链前缀（endpoint 拼 bucket；attachment 公有目录直链用）
	PublicBase() string
	// Internal 内网操作视图（操作线走 InternalEndpoint；签名线不受影响）
	Internal() Storage
}

// ---- 共享签名骨架 ----

// hmacSHA256 HMAC-SHA256（返回原始字节）
func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// sha256Hex hex(sha256(data))
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// uriEncode OSS/S3 V4 规范：rawurlencode 但豁免斜杠
func uriEncode(s string) string {
	escaped := url.QueryEscape(s)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "%2F", "/")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

// canonicalQuery 参数字典序 k=v 双侧编码（query 语境：斜杠不豁免——credential 等
// 值内斜杠必须编码；路径斜杠豁免仅在拼 CanonicalRequest 的 URI 段）
func canonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, uriEncodeQuery(k)+"="+uriEncodeQuery(params[k]))
	}
	return strings.Join(parts, "&")
}

// uriEncodeQuery query 语境编码（斜杠编码为 %2F、空格 %20、豁免 ~）
func uriEncodeQuery(s string) string {
	escaped := url.QueryEscape(s)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

// deriveSigningKey 四层派生：prefix+SK → date → region → service → request
func deriveSigningKey(prefix, secret, dateStr, region, service, request string) []byte {
	dateKey := hmacSHA256([]byte(prefix+secret), []byte(dateStr))
	regionKey := hmacSHA256(dateKey, []byte(region))
	serviceKey := hmacSHA256(regionKey, []byte(service))
	return hmacSHA256(serviceKey, []byte(request))
}

// nowUTC 注入时间（nil 用当前）
func nowUTC(now func() time.Time) time.Time {
	if now != nil {
		return now().UTC()
	}
	return time.Now().UTC()
}

// ---- 共享域名骨架 ----

// deriveRegion 从标准端点推导签名 region；推导不出一律 us-east-1 兜底。
// 识别形态（host 先剥协议前缀/路径/端口）：
//   - oss-{region}[-internal].aliyuncs.com → region
//   - s3.{region}.* / s3-{region}.* / cos.{region}.* → region
//   - s3.amazonaws.com 与其余（IP/端口/未知域名）→ us-east-1
func deriveRegion(endpoint string) string {
	host := normalizeDomain(endpoint)
	// 剥路径与端口，只留域名
	if i := strings.IndexAny(host, "/:"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(host)

	// s3.amazonaws.com 全局端点（先于 s3.{region}.* 匹配，否则 region 误取 amazonaws）
	if host == "s3.amazonaws.com" {
		return "us-east-1"
	}

	segments := strings.Split(host, ".")

	// oss-{region}[-internal].aliyuncs.com → region
	if strings.HasPrefix(host, "oss-") && strings.HasSuffix(host, ".aliyuncs.com") {
		region := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSuffix(host, ".aliyuncs.com"), "-internal"), "oss-")
		if region != "" {
			return region
		}
		return "us-east-1"
	}

	// s3.{region}.* / cos.{region}.* → region 在第二段
	if len(segments) >= 3 && (segments[0] == "s3" || segments[0] == "cos") && segments[1] != "" {
		return segments[1]
	}

	// s3-{region}.* → region 为首段剥 s3- 前缀
	if strings.HasPrefix(host, "s3-") && len(segments) >= 2 && len(segments[0]) > len("s3-") {
		return segments[0][len("s3-"):]
	}

	// 其余（IP/端口/未知域名）兜底
	return "us-east-1"
}

// normalizeDomain 剥协议前缀与尾随斜杠（domain 或 https://domain/ 都收）
func normalizeDomain(domain string) string {
	d := strings.TrimPrefix(strings.TrimPrefix(domain, "https://"), "http://")
	return strings.TrimSuffix(d, "/")
}

// resolveHost 域名链解析：优先级列表逐个取非空，全空回退 defaultHost
func resolveHost(preferred []string, defaultHost string) string {
	for _, h := range preferred {
		if h != "" {
			return h
		}
	}
	return defaultHost
}

// uploadTicketKey 固定实际对象名，禁止服务端展开 ${filename} 等客户端可替换模板。
func uploadTicketKey(directory, filename string) (string, error) {
	key := strings.TrimSuffix(directory, "/") + "/" + filename
	if filename == "" || !utf8.ValidString(key) || strings.Contains(key, "${") {
		return "", errors.New("storage: upload ticket requires a literal key and non-empty filename")
	}
	return key, nil
}

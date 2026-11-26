//go:build integration

// storage S3 adapter 真 MinIO 集成（change: add-storage-package / storage-domain-unify）。
//
// 环境要求：MINIO_ENDPOINT/MINIO_AK/MINIO_SK/MINIO_BUCKET（缺失 Skip）；
// 如 docker run -d -p 9000:9000 minio/minio server /data 后用 http://127.0.0.1:9000。
// 可选 MINIO_INTERNAL_ENDPOINT：同桶另一可达地址（如 http://localhost:10002，与
// MINIO_ENDPOINT 同实例不同 host 字符串），驱动双端点内网视图测试，未设置时该测试 Skip。
//
// 桶不存在时自动创建（已存在则复用）；测试自建的桶结束时清空对象并删桶归还环境，
// 只清理本测试写入的前缀对象，绝不触碰实例内其他桶。
package tests

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/storage"
)

func newMinIOStorage(t *testing.T) *storage.S3Storage {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("未设置 MINIO_ENDPOINT（如 http://127.0.0.1:9000），跳过 storage MinIO 集成")
	}
	ak := os.Getenv("MINIO_AK")
	if ak == "" {
		ak = "minioadmin"
	}
	sk := os.Getenv("MINIO_SK")
	if sk == "" {
		sk = "minioadmin"
	}
	bucket := os.Getenv("MINIO_BUCKET")
	if bucket == "" {
		bucket = "gf-lib-test"
	}
	return storage.NewS3(ak, sk, bucket, endpoint)
}

// ---- 测试辅助：桶管理与手写 SigV4 桶根请求 ----

// hostOf 域名裸 host（剥协议前缀/尾斜杠/路径，保留端口），与主库 hostOnly 同构：
// 进签名的 host 含非标端口（MinIO :9000/:10002 属签名的一部分）
func hostOf(endpoint string) string {
	h := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	h = strings.TrimSuffix(h, "/")
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	return h
}

// hmacSHA256Bytes HMAC-SHA256（测试内独立重算签名用）
func hmacSHA256Bytes(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// sha256HexStr hex(sha256(data))
func sha256HexStr(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// sigV4BucketRoot 对桶根路径发手写 SigV4 头签名请求（建桶 PUT / 删桶 DELETE）。
// storage 包不暴露桶管理 API，此处按主库 signedRequest 同构算法实现：三头签名
// （host/x-amz-content-sha256/x-amz-date）+ UNSIGNED-PAYLOAD + path-style 桶根寻址；
// region 固定 us-east-1——MinIO 默认 region，且 IP:port 端点 deriveRegion 兜底值相同
func sigV4BucketRoot(method, endpoint, ak, sk, bucket string) (*http.Request, error) {
	host := hostOf(endpoint)
	now := time.Now().UTC()
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")

	canonicalRequest := strings.Join([]string{
		method,
		"/" + bucket,
		"",
		"host:" + host + "\nx-amz-content-sha256:UNSIGNED-PAYLOAD\nx-amz-date:" + dateTimeStr + "\n",
		"host;x-amz-content-sha256;x-amz-date",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	scope := dateStr + "/us-east-1/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", dateTimeStr, scope, sha256HexStr([]byte(canonicalRequest)),
	}, "\n")
	// 四层派生 SigningKey：AWS4+SK → date → region → s3 → aws4_request
	signingKey := hmacSHA256Bytes([]byte("AWS4"+sk), []byte(dateStr))
	for _, part := range []string{"us-east-1", "s3", "aws4_request"} {
		signingKey = hmacSHA256Bytes(signingKey, []byte(part))
	}
	signature := hex.EncodeToString(hmacSHA256Bytes(signingKey, []byte(stringToSign)))

	req, err := http.NewRequest(method, strings.TrimSuffix(endpoint, "/")+"/"+bucket, nil)
	if err != nil {
		return nil, err
	}
	req.Host = host
	req.Header.Set("X-Amz-Date", dateTimeStr)
	req.Header.Set("x-amz-content-sha256", "UNSIGNED-PAYLOAD")
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=host;x-amz-content-sha256;x-amz-date,Signature=%s",
		ak, scope, signature))
	return req, nil
}

// minIOBucketReady 确保 MINIO_BUCKET 存在（缺则建，已存在 409 复用），并注册 t.Cleanup：
// 先分页列尽 prefixes 前缀下对象逐个删除，桶为本测试新建时再删桶归还环境。
// 只清理本测试写入的前缀对象与自建桶，绝不触碰实例内其他桶/其他对象
func minIOBucketReady(t *testing.T, s *storage.S3Storage, prefixes ...string) {
	t.Helper()
	ctx := context.Background()

	putReq, err := sigV4BucketRoot(http.MethodPut, s.Endpoint, s.AccessKeyID, s.SecretAccessKey, s.Bucket)
	if err != nil {
		t.Fatalf("构造建桶请求失败: %v", err)
	}
	resp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("建桶请求失败（endpoint %s）: %v", s.Endpoint, err)
	}
	defer resp.Body.Close()
	created := resp.StatusCode == http.StatusOK
	if !created && resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("建桶应 200（新建）/409（已存在），实际 %d: %s", resp.StatusCode, body)
	}

	t.Cleanup(func() {
		// 清空本测试写入的前缀对象（分页列尽逐个删，防漏）；单前缀清理失败只跳过该前缀，
		// 仍继续尝试删桶（return 会连带跳过删桶，导致测试桶残留）
		for _, prefix := range prefixes {
			after := ""
			for i := 0; i < 100; i++ {
				listing, err := s.List(ctx, prefix, after, 1000)
				if err != nil {
					t.Logf("清理前缀 %s 列举失败: %v", prefix, err)
					break
				}
				for _, obj := range listing.Objects {
					if err := s.Delete(ctx, obj.Key); err != nil {
						t.Logf("清理对象 %s 失败: %v", obj.Key, err)
					}
				}
				if !listing.IsTruncated || len(listing.Objects) == 0 {
					break
				}
				after = listing.NextMarker
			}
		}
		// 仅删本测试新建的桶（复用的桶内可能有外部对象，不能删）
		if !created {
			return
		}
		delReq, err := sigV4BucketRoot(http.MethodDelete, s.Endpoint, s.AccessKeyID, s.SecretAccessKey, s.Bucket)
		if err != nil {
			t.Logf("构造删桶请求失败: %v", err)
			return
		}
		delResp, err := http.DefaultClient.Do(delReq)
		if err != nil {
			t.Logf("删桶请求失败: %v", err)
			return
		}
		delResp.Body.Close()
		if delResp.StatusCode != http.StatusNoContent {
			t.Logf("删桶应 204，实际 %d（桶内可能仍有残留对象）", delResp.StatusCode)
		}
	})
}

// ---- 既有用例（NewS3 已改四参，region 由 endpoint 推导）----

// TestMinIO_PresignUploadHeadDelete 预签名 PUT 上传 → head size/ETag → delete → exists false
func TestMinIO_PresignUploadHeadDelete(t *testing.T) {
	s := newMinIOStorage(t)
	minIOBucketReady(t, s, "it/")
	ctx := context.Background()
	key := fmt.Sprintf("it/%d.txt", time.Now().UnixNano())

	content := []byte("hello gf-lib storage integration 测试内容")
	contentMD5 := md5.Sum(content)
	wantETag := hex.EncodeToString(contentMD5[:])

	// ① PUT 预签名上传
	putURL, err := s.UploadURL(ctx, key, 300)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("预签名 PUT 请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("预签名 PUT 应 200，实际 %d", resp.StatusCode)
	}

	// ② head：size 与 md5
	meta, err := s.Head(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("head 应命中")
	}
	if meta.Size != int64(len(content)) {
		t.Fatalf("size 应 %d，实际 %d", len(content), meta.Size)
	}
	if meta.ETag != wantETag {
		t.Fatalf("ETag 应为内容 md5 %s，实际 %s", wantETag, meta.ETag)
	}

	// ③ exists true
	if ok, _ := s.Exists(ctx, key); !ok {
		t.Fatal("exists 应 true")
	}

	// ④ GET 预签名读回
	getURL, _ := s.AccessURL(ctx, key, 300, "")
	getResp, err := http.Get(getURL)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET 预签名应 200，实际 %d", getResp.StatusCode)
	}

	// ④' response-content-disposition 进签名的 URL 过真 MinIO 验签且响应头生效（下载文件名）
	cdURL, _ := s.AccessURL(ctx, key, 300, `attachment; filename="report.txt"`)
	cdResp, err := http.Get(cdURL)
	if err != nil {
		t.Fatal(err)
	}
	defer cdResp.Body.Close()
	if cdResp.StatusCode != http.StatusOK {
		t.Fatalf("带 contentDisposition 的预签名应 200（参数须过验签），实际 %d", cdResp.StatusCode)
	}
	if got := cdResp.Header.Get("Content-Disposition"); got != `attachment; filename="report.txt"` {
		t.Fatalf("响应应下发 Content-Disposition，实际 %q", got)
	}

	// ⑤ delete 后 exists false
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, key); ok {
		t.Fatal("delete 后 exists 应 false")
	}
}

// ---- task 3.1：POST policy 票据全链 + 操作线全链 ----

// postTicketForm 按票据 FormData 拼 multipart 表单并 POST 到 UploadURL。
// SigV4 POST policy 要求 file 字段必须在表单最后（其余字段顺序无关）；overrideKey
// 非空时替换表单 key 字段值（负例：违反完整 key eq 条件重放）
func postTicketForm(ctx context.Context, tk *storage.UploadTicket, filename, contentType string, content []byte, overrideKey string) (*http.Response, error) {
	formData := tk.FormData
	if overrideKey != "" {
		formData = maps.Clone(tk.FormData)
		formData["key"] = overrideKey
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range formData {
		fw, err := w.CreateFormField(k)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write([]byte(v)); err != nil {
			return nil, err
		}
	}
	// file 字段最后（policy 校验要求）
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	h.Set("Content-Type", contentType)
	fw, err := w.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(content); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, tk.Method, tk.UploadURL, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return http.DefaultClient.Do(req)
}

// TestStorageUploadTicket POST policy 票据直传：正例上传 2xx 且内容可回读；
// 负例同票据换违反完整 key eq 的 key 重放被 403 拒绝（MinIO：AccessDenied Policy Condition failed）
func TestStorageUploadTicket(t *testing.T) {
	s := newMinIOStorage(t)
	dir := fmt.Sprintf("ticket/%d", time.Now().UnixNano())
	minIOBucketReady(t, s, dir+"/")
	ctx := context.Background()
	content := []byte("MinIO POST policy 表单直传内容：gf-lib storage 集成")

	t.Run("正例_上传成功且内容可回读", func(t *testing.T) {
		tk, err := s.UploadTicket(ctx, dir, "hello.txt", 300, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if tk.Method != http.MethodPost {
			t.Fatalf("票据 Method 应 POST，实际 %s", tk.Method)
		}
		key := dir + "/hello.txt"
		if tk.FormData["key"] != key {
			t.Fatalf("表单 key 应 %q（带 directory 前缀），实际 %q", key, tk.FormData["key"])
		}

		resp, err := postTicketForm(ctx, tk, "hello.txt", "text/plain", content, "")
		if err != nil {
			t.Fatalf("票据 POST 请求失败: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("票据 POST 应 2xx，实际 %d: %s", resp.StatusCode, body)
		}

		// Get 回读内容一致
		meta, body, err := s.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if meta == nil {
			t.Fatalf("对象 %s 应存在", key)
		}
		defer body.Close()
		got, _ := io.ReadAll(body)
		if !bytes.Equal(got, content) {
			t.Fatalf("回读内容不一致: want %q got %q", content, got)
		}
		if meta.Size != int64(len(content)) {
			t.Fatalf("回读 size 应 %d，实际 %d", len(content), meta.Size)
		}
	})

	t.Run("负例_同目录其他key被403拒绝", func(t *testing.T) {
		tk, err := s.UploadTicket(ctx, dir, "evil.txt", 300, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		evilKey := dir + "/target.txt" // 同目录已有对象，不能被另一张票据改 key 覆盖
		if err := s.Put(ctx, evilKey, "text/plain", bytes.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		resp, err := postTicketForm(ctx, tk, "evil.txt", "text/plain", []byte("attempted overwrite of another object"), evilKey)
		if err != nil {
			t.Fatalf("负例 POST 请求失败: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("违规 key 重放应 403，实际 %d: %s", resp.StatusCode, body)
		}
		meta, existing, err := s.Get(ctx, evilKey)
		if err != nil || meta == nil {
			t.Fatal("target object missing after rejected key mutation")
		}
		defer existing.Close()
		got, err := io.ReadAll(existing)
		if err != nil || !bytes.Equal(got, content) {
			t.Fatal("target object changed after rejected key mutation")
		}
	})
	t.Run("同一票据同key重传取最后内容", func(t *testing.T) {
		ticket, err := s.UploadTicket(ctx, dir, "replay.txt", 300, 1, 1024)
		if err != nil {
			t.Fatal(err)
		}
		last := []byte("second version")
		for _, data := range [][]byte{[]byte("first version"), last} {
			response, err := postTicketForm(ctx, ticket, "replay.txt", "text/plain", data, "")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				t.Fatalf("same-key POST status %d", response.StatusCode)
			}
		}
		meta, body, err := s.Get(ctx, dir+"/replay.txt")
		if err != nil || meta == nil {
			t.Fatal("replayed object missing")
		}
		defer body.Close()
		got, err := io.ReadAll(body)
		if err != nil || !bytes.Equal(got, last) {
			t.Fatal("replay did not retain last content")
		}
	})
	t.Run("原大小和过期限制", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			expiry int
			max    int64
		}{{"oversized", 300, 1}, {"expired", -60, 1024}} {
			ticket, err := s.UploadTicket(ctx, dir, tc.name+".txt", tc.expiry, 0, tc.max)
			if err != nil {
				t.Fatal(err)
			}
			response, err := postTicketForm(ctx, ticket, tc.name+".txt", "text/plain", []byte("content"), "")
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				t.Fatalf("%s constraint ignored", tc.name)
			}
		}
	})
}

// TestStorageOperationChain 操作线全链：Put（4 对象同前缀）→ Get 回读 → List 分页
// （max=2 首页 IsTruncated/NextMarker、循环翻页取尽、after 跳过）→ Head 校验 Size/ETag →
// Copy → 目标端 Head 存在 → Delete → Exists false
func TestStorageOperationChain(t *testing.T) {
	s := newMinIOStorage(t)
	prefix := fmt.Sprintf("chain/%d/", time.Now().UnixNano())
	minIOBucketReady(t, s, prefix)
	ctx := context.Background()

	// 4 个对象（3+ 满足全链要求），内容互不相同；key 字典序 = 文件名字典序
	names := []string{"a.txt", "b.txt", "c.txt", "d.txt"}
	contents := map[string][]byte{}
	etags := map[string]string{}
	for _, name := range names {
		content := []byte(fmt.Sprintf("gf-lib 操作线全链内容 %s @%d", name, time.Now().UnixNano()))
		contents[name] = content
		sum := md5.Sum(content)
		etags[name] = hex.EncodeToString(sum[:])
		if err := s.Put(ctx, prefix+name, "text/plain", bytes.NewReader(content)); err != nil {
			t.Fatalf("Put %s 失败: %v", prefix+name, err)
		}
	}

	// Get 回读内容一致
	for _, name := range names {
		key := prefix + name
		meta, body, err := s.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get %s 失败: %v", key, err)
		}
		if meta == nil {
			t.Fatalf("Get %s 应命中", key)
		}
		got, _ := io.ReadAll(body)
		body.Close()
		if !bytes.Equal(got, contents[name]) {
			t.Fatalf("Get %s 回读内容不一致", key)
		}
	}

	fullKeys := make([]string, 0, len(names))
	for _, name := range names {
		fullKeys = append(fullKeys, prefix+name)
	}

	// List 首页 max=2：恰好 2 条且后面还有 → IsTruncated 必真，NextMarker=第二条 key
	page1, err := s.List(ctx, prefix, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Objects) != 2 || !page1.IsTruncated {
		t.Fatalf("首页应 2 条且 IsTruncated=true，实际 %d 条 truncated=%v", len(page1.Objects), page1.IsTruncated)
	}
	if page1.NextMarker != fullKeys[1] {
		t.Fatalf("首页 NextMarker 应 %s，实际 %s", fullKeys[1], page1.NextMarker)
	}

	// 循环翻页取尽（不硬断言「恰好取尽页」的 truncated 边界行为，聚合结果才是契约）：
	// after=NextMarker 逐页续列直至 !IsTruncated，聚合应得全部 4 个 key
	var paged []string
	after := ""
	for i := 0; ; i++ {
		if i > 10 {
			t.Fatal("翻页超过 10 轮仍未取尽，疑似死循环")
		}
		listing, err := s.List(ctx, prefix, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, obj := range listing.Objects {
			paged = append(paged, obj.Key)
		}
		if !listing.IsTruncated {
			break
		}
		after = listing.NextMarker
	}
	if !slices.Equal(paged, fullKeys) {
		t.Fatalf("翻页聚合应 %v，实际 %v", fullKeys, paged)
	}

	// after 跳过正确（start-after 语义：只列 key 字典序严格在 after 之后的对象）
	afterListing, err := s.List(ctx, prefix, fullKeys[1], 1000)
	if err != nil {
		t.Fatal(err)
	}
	gotAfter := make([]string, 0, len(afterListing.Objects))
	for _, obj := range afterListing.Objects {
		gotAfter = append(gotAfter, obj.Key)
	}
	if !slices.Equal(gotAfter, fullKeys[2:]) {
		t.Fatalf("after=%s 应返回 %v，实际 %v", fullKeys[1], fullKeys[2:], gotAfter)
	}

	// Head 校验 Size / ETag（= 内容 md5 hex）/ ContentType / LastModified
	for _, name := range names {
		key := prefix + name
		meta, err := s.Head(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if meta == nil {
			t.Fatalf("Head %s 应命中", key)
		}
		if meta.Size != int64(len(contents[name])) {
			t.Fatalf("Head %s size 应 %d，实际 %d", key, len(contents[name]), meta.Size)
		}
		if meta.ETag != etags[name] {
			t.Fatalf("Head %s ETag 应内容 md5 %s，实际 %s", key, etags[name], meta.ETag)
		}
		if meta.ContentType != "text/plain" {
			t.Fatalf("Head %s ContentType 应 text/plain，实际 %s", key, meta.ContentType)
		}
	}

	// Copy：目标端 Head 存在且元数据与源一致（服务端拷贝保留 ETag）
	srcKey, dstKey := fullKeys[0], prefix+"copy-of-a.txt"
	if err := s.Copy(ctx, srcKey, dstKey); err != nil {
		t.Fatalf("Copy 失败: %v", err)
	}
	dstMeta, err := s.Head(ctx, dstKey)
	if err != nil {
		t.Fatal(err)
	}
	if dstMeta == nil {
		t.Fatal("Copy 目标对象应存在")
	}
	if dstMeta.ETag != etags["a.txt"] || dstMeta.Size != int64(len(contents["a.txt"])) {
		t.Fatalf("Copy 目标元数据应与源一致（size %d etag %s），实际 size %d etag %s",
			len(contents["a.txt"]), etags["a.txt"], dstMeta.Size, dstMeta.ETag)
	}

	// Delete → 源 Exists false，目标不受影响
	if err := s.Delete(ctx, srcKey); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, srcKey); ok {
		t.Fatal("Delete 后 Exists 应 false")
	}
	if ok, _ := s.Exists(ctx, dstKey); !ok {
		t.Fatal("Copy 目标对象不应受源删除影响")
	}
}

// ---- task 3.2：双端点内网视图 ----

// hostRecordTransport 记录途经请求 host 的 RoundTripper（断言内网视图操作线实际打点，
// 不依赖抓包）
type hostRecordTransport struct {
	mu    sync.Mutex
	hosts []string
	base  http.RoundTripper
}

func (r *hostRecordTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.hosts = append(r.hosts, req.Host)
	r.mu.Unlock()
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// take 快照并清空已记录 host（分批断言用）
func (r *hostRecordTransport) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	hosts := append([]string(nil), r.hosts...)
	r.hosts = nil
	return hosts
}

// assertAllHosts 断言本批记录的 host 全部为 want 且至少一条
func assertAllHosts(t *testing.T, hosts []string, want string) {
	t.Helper()
	if len(hosts) == 0 {
		t.Fatalf("应至少发出一条请求（期望 host %s）", want)
	}
	for _, h := range hosts {
		if h != want {
			t.Fatalf("请求 host 应全部为 %s，实际出现 %s（全部: %v）", want, h, hosts)
		}
	}
}

// TestStorageInternalViewWithoutEndpoint 未配置 InternalEndpoint 的实例，其内网视图操作线
// 应返回「未配置」错误且不发请求（signedRequest 在 opAddress 阶段即报错，无需真 MinIO）
func TestStorageInternalViewWithoutEndpoint(t *testing.T) {
	s := storage.NewS3("ak", "sk", "any-bucket", "http://127.0.0.1:9000")
	view := s.Internal()
	ctx := context.Background()

	// 操作线七方法全部应返回未配置错误
	checkErr := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "InternalEndpoint 未配置") {
			t.Fatalf("%s 应返回「InternalEndpoint 未配置」错误，实际 %v", what, err)
		}
	}
	checkErr("视图 Put", func() error {
		return view.Put(ctx, "k", "text/plain", strings.NewReader("x"))
	}())
	checkErr("视图 Get", func() error {
		_, _, err := view.Get(ctx, "k")
		return err
	}())
	checkErr("视图 Head", func() error {
		_, err := view.Head(ctx, "k")
		return err
	}())
	checkErr("视图 Exists", func() error {
		_, err := view.Exists(ctx, "k")
		return err
	}())
	checkErr("视图 Delete", func() error { return view.Delete(ctx, "k") }())
	checkErr("视图 Copy", func() error { return view.Copy(ctx, "a", "b") }())
	checkErr("视图 List", func() error {
		_, err := view.List(ctx, "", "", 10)
		return err
	}())
}

// TestStorageInternalView 双端点内网视图：同桶配 Endpoint/MINIO_INTERNAL_ENDPOINT 两个可达
// 地址（同实例不同 host 字符串）。断言：① 视图操作线（Put→Get 回读）成功且请求 host 全部
// 为内网端点（注入记录 host 的 RoundTripper 取证）；② 原实例操作线打默认端点、写入对象
// 经视图可见（同桶同实例）；③ 注入固定时间后原实例与视图签名线四方法产物完全一致
func TestStorageInternalView(t *testing.T) {
	internal := os.Getenv("MINIO_INTERNAL_ENDPOINT")
	if internal == "" {
		t.Skip("未设置 MINIO_INTERNAL_ENDPOINT（如 http://localhost:10002），跳过双端点内网视图测试")
	}
	s := newMinIOStorage(t)
	prefix := fmt.Sprintf("internal/%d/", time.Now().UnixNano())
	minIOBucketReady(t, s, prefix)
	s.InternalEndpoint = internal
	rec := &hostRecordTransport{}
	s.HTTPClient = &http.Client{Transport: rec}
	ctx := context.Background()
	view := s.Internal()

	// ① 视图操作线：Put 成功且 host 全部为内网端点
	key := prefix + "view.txt"
	content := []byte("内网视图操作线内容：经 InternalEndpoint 读写")
	if err := view.Put(ctx, key, "text/plain", bytes.NewReader(content)); err != nil {
		t.Fatalf("视图 Put 失败: %v", err)
	}
	assertAllHosts(t, rec.take(), hostOf(internal))

	// 视图 Get 回读内容一致且 host 仍为内网端点
	meta, body, err := view.Get(ctx, key)
	if err != nil {
		t.Fatalf("视图 Get 失败: %v", err)
	}
	if meta == nil {
		t.Fatal("视图 Get 应命中")
	}
	got, _ := io.ReadAll(body)
	body.Close()
	if !bytes.Equal(got, content) {
		t.Fatalf("视图回读内容不一致: want %q got %q", content, got)
	}
	assertAllHosts(t, rec.take(), hostOf(internal))

	// ② 对照组：原实例操作线打默认端点，且写入对象经视图可见（同桶同实例）
	origKey := prefix + "orig.txt"
	if err := s.Put(ctx, origKey, "text/plain", bytes.NewReader(content)); err != nil {
		t.Fatalf("原实例 Put 失败: %v", err)
	}
	assertAllHosts(t, rec.take(), hostOf(s.Endpoint))
	if ok, err := view.Exists(ctx, origKey); err != nil || !ok {
		t.Fatalf("原实例写入的对象应经内网视图可见: ok=%v err=%v", ok, err)
	}

	// ③ 签名线产物一致：注入固定时间（预签名 URL 含时间戳，不注入则两次调用可能跨秒不等），
	// 在克隆实例上注入——原实例 s 的 Now 保持真实时钟（t.Cleanup 清理仍用其签名发请求，
	// 固定时间会被 MinIO RequestTimeTooSkewed 拒）；原实例与视图（浅拷贝继承注入的 Now）
	// 四方法产物应完全相等
	fixed := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	sigS := *s
	sigS.Now = func() time.Time { return fixed }
	viewFixed := sigS.Internal()
	u1, _ := sigS.UploadURL(ctx, key, 300)
	u2, _ := viewFixed.UploadURL(ctx, key, 300)
	if u1 != u2 {
		t.Fatalf("视图 UploadURL 产物应与原实例一致:\n%s\n%s", u1, u2)
	}
	a1, _ := sigS.AccessURL(ctx, key, 300, "")
	a2, _ := viewFixed.AccessURL(ctx, key, 300, "")
	if a1 != a2 {
		t.Fatalf("视图 AccessURL 产物应与原实例一致:\n%s\n%s", a1, a2)
	}
	if sigS.PublicBase() != viewFixed.PublicBase() {
		t.Fatalf("视图 PublicBase 应与原实例一致: %s vs %s", sigS.PublicBase(), viewFixed.PublicBase())
	}
	tk1, _ := sigS.UploadTicket(ctx, prefix+"sign", "t.txt", 300, 0, 0)
	tk2, _ := viewFixed.UploadTicket(ctx, prefix+"sign", "t.txt", 300, 0, 0)
	if tk1.UploadURL != tk2.UploadURL || !maps.Equal(tk1.FormData, tk2.FormData) {
		t.Fatal("视图 UploadTicket 产物应与原实例一致（UploadURL + FormData 全集）")
	}
}

package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// 素材管理域表项：6 端点（官方文档清点见 change wecom-media-domain proposal）
var (
	epMediaUpload       = Endpoint{"media.upload", http.MethodPost, "/cgi-bin/media/upload", []string{"type"}, true}
	epMediaUploadImg    = Endpoint{"media.uploadimg", http.MethodPost, "/cgi-bin/media/uploadimg", nil, true}
	epMediaUploadByURL  = Endpoint{"media.upload_by_url", http.MethodPost, "/cgi-bin/media/upload_by_url", nil, true}
	epMediaUploadResult = Endpoint{"media.get_upload_by_url_result", http.MethodPost, "/cgi-bin/media/get_upload_by_url_result", nil, true}
	epMediaGet          = Endpoint{"media.get", http.MethodGet, "/cgi-bin/media/get", []string{"media_id"}, false}
	epMediaGetJssdk     = Endpoint{"media.get_jssdk", http.MethodGet, "/cgi-bin/media/get/jssdk", []string{"media_id"}, false}
)

// UploadMedia 上传临时素材（multipart；type=image/voice/video/file），media_id 3 天有效
func (c *Client) UploadMedia(ctx context.Context, mediaType, filename string, content io.Reader) (map[string]any, error) {
	return c.InvokeMultipart(ctx, epMediaUpload, map[string]string{"type": mediaType}, filename, content)
}

// UploadImg 上传图片得永久 URL（2MB 内；仅企业微信内可用）
func (c *Client) UploadImg(ctx context.Context, filename string, content io.Reader) (map[string]any, error) {
	return c.InvokeMultipart(ctx, epMediaUploadImg, nil, filename, content)
}

// UploadMediaByURL 生成异步上传任务（body：scene/type/filename/url/md5；scene=1 入群欢迎语）
func (c *Client) UploadMediaByURL(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMediaUploadByURL, nil, body)
}

// GetUploadByURLResult 查询异步上传结果（status：1 处理中 / 2 完成 / 3 失败，detail 内含 media_id）
func (c *Client) GetUploadByURLResult(ctx context.Context, jobid string) (map[string]any, error) {
	return c.invoke(ctx, epMediaUploadResult, nil, map[string]any{"jobid": jobid})
}

// GetMedia 获取临时素材（文件流；>20M 大文件 Range 分块下载由调用方经 HTTPClient 层处理）
func (c *Client) GetMedia(ctx context.Context, mediaID string) (contentType, filename string, data []byte, err error) {
	return c.InvokeDownload(ctx, epMediaGet, map[string]string{"media_id": mediaID})
}

// GetJssdkVoice 获取高清语音素材（JSSDK uploadVoice 上传的 speex 16K）
func (c *Client) GetJssdkVoice(ctx context.Context, mediaID string) (contentType, filename string, data []byte, err error) {
	return c.InvokeDownload(ctx, epMediaGetJssdk, map[string]string{"media_id": mediaID})
}

// InvokeMultipart multipart/form-data 上传分发（文件字段 media；query 键集校验、token 注入、
// errcode→BizError、HTTP 失败→InfraError，异常分层与 invoke 一致）。四包后续域复用参照。
func (c *Client) InvokeMultipart(ctx context.Context, ep Endpoint, query map[string]string, filename string, content io.Reader) (map[string]any, error) {
	if err := checkQuery(ep, query); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("media", filename)
	if err != nil {
		return nil, &InfraError{Op: "encode", Err: err}
	}
	if _, err := io.Copy(fw, content); err != nil {
		return nil, &InfraError{Op: "encode", Err: err}
	}
	if err := w.Close(); err != nil {
		return nil, &InfraError{Op: "encode", Err: err}
	}
	return c.doCT(ctx, ep.Method, ep.Path, query, buf.Bytes(), w.FormDataContentType())
}

// InvokeDownload 二进制下载分发：成功返回（Content-Type, filename, 文件字节）；失败响应为
// JSON errcode≠0 时返回 BizError。query 键集校验、token 注入同 invoke。四包后续域复用参照。
func (c *Client) InvokeDownload(ctx context.Context, ep Endpoint, query map[string]string) (contentType, filename string, data []byte, err error) {
	if err := checkQuery(ep, query); err != nil {
		return "", "", nil, err
	}
	token, err := c.tokenMgr.Token(ctx, false)
	if err != nil {
		return "", "", nil, &InfraError{Op: "token", Err: fmt.Errorf("获取 access_token 失败: %w", err)}
	}
	base := c.BaseURL
	if base == "" {
		base = defaultQyAPI
	}
	target := base + ep.Path + "?access_token=" + token
	for k, v := range query {
		target += "&" + k + "=" + v
	}
	req, err := http.NewRequestWithContext(ctx, ep.Method, target, nil)
	if err != nil {
		return "", "", nil, &InfraError{Op: "http", Err: err}
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", nil, &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", nil, &InfraError{Op: "http", Err: err}
	}
	ct := resp.Header.Get("Content-Type")
	// 失败响应是 JSON（errcode）；成功是文件流
	if strings.Contains(ct, "json") || (len(raw) > 0 && raw[0] == '{') {
		var decoded map[string]any
		if jerr := json.Unmarshal(raw, &decoded); jerr == nil {
			if code := errcodeOf(decoded); code != 0 {
				errmsg, _ := decoded["errmsg"].(string)
				return "", "", nil, &BizError{Errcode: code, Errmsg: errmsg}
			}
			return "", "", nil, &InfraError{Op: "decode", Err: fmt.Errorf("下载响应为 JSON 但无 errcode: %s", string(raw))}
		}
	}
	name := resp.Header.Get("Content-Disposition")
	if i := strings.Index(name, `filename="`); i >= 0 {
		rest := name[i+len(`filename="`):]
		if j := strings.Index(rest, `"`); j >= 0 {
			name = rest[:j]
		}
	}
	return ct, name, raw, nil
}

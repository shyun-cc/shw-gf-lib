package wxoa

import (
	"context"
	"net/http"
)

// 素材管理表项（永久 5 + 图文 1 + 临时 3；官方取证 asset 页 2026-08-24。
// multipart 三件官方 part 名 media；media/get 二进制走 invokeRaw）
var (
	epMaterialAdd     = Endpoint{"material.add", http.MethodPost, "/cgi-bin/material/add_material", []string{"type"}, true}
	epMaterialGet     = Endpoint{"material.get", http.MethodPost, "/cgi-bin/material/get_material", nil, true}
	epMaterialCount   = Endpoint{"material.count", http.MethodGet, "/cgi-bin/material/get_materialcount", nil, false}
	epMaterialBatch   = Endpoint{"material.batchget", http.MethodPost, "/cgi-bin/material/batchget_material", nil, true}
	epMaterialDel     = Endpoint{"material.del", http.MethodPost, "/cgi-bin/material/del_material", nil, true}
	epMediaUploadImg  = Endpoint{"material.uploadimg", http.MethodPost, "/cgi-bin/media/uploadimg", nil, true}
	epMediaUpload     = Endpoint{"material.media_upload", http.MethodPost, "/cgi-bin/media/upload", []string{"type"}, true}
	epMediaGet        = Endpoint{"material.media_get", http.MethodGet, "/cgi-bin/media/get", []string{"media_id"}, false}
	epMediaJSSDKVoice = Endpoint{"material.media_jssdk", http.MethodGet, "/cgi-bin/media/get/jssdk", []string{"media_id"}, false}
)

// AddMaterial 上传永久素材（type image/voice/video/thumb；永久素材同类上限见官方额度说明；
// video 须 description query 形态 title/intro——由调用方组 query）。
func (s *WxOAService) AddMaterial(ctx context.Context, mediaType, filename string, content []byte, extraQuery map[string]string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]string{"access_token": token, "type": mediaType}
	for k, v := range extraQuery {
		query[k] = v
	}
	return s.invokeMultipart(ctx, epMaterialAdd, query, "media", filename, content)
}

// GetMaterial 获取永久素材（图文/视频返回 JSON；图片语音返回二进制内容）。
func (s *WxOAService) GetMaterial(ctx context.Context, mediaID string) (map[string]any, error) {
	return s.oaPost(ctx, epMaterialGet, map[string]any{"media_id": mediaID})
}

// GetMaterialCount 获取永久素材总数（image/voice/video/news 四类计数）。
func (s *WxOAService) GetMaterialCount(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epMaterialCount, nil)
}

// BatchGetMaterial 获取永久素材列表（type + offset + count ≤20）。
func (s *WxOAService) BatchGetMaterial(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epMaterialBatch, body)
}

// DelMaterial 删除永久素材。
func (s *WxOAService) DelMaterial(ctx context.Context, mediaID string) (map[string]any, error) {
	return s.oaPost(ctx, epMaterialDel, map[string]any{"media_id": mediaID})
}

// UploadImg 上传图文消息内的图片（返回 url 用于图文正文；不受素材上限约束）。
func (s *WxOAService) UploadImg(ctx context.Context, filename string, content []byte) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeMultipart(ctx, epMediaUploadImg, map[string]string{"access_token": token}, "media", filename, content)
}

// UploadTempMedia 新增临时素材（type image/voice/video/thumb；3 天有效）。
func (s *WxOAService) UploadTempMedia(ctx context.Context, mediaType, filename string, content []byte) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeMultipart(ctx, epMediaUpload, map[string]string{"access_token": token, "type": mediaType}, "media", filename, content)
}

// GetTempMedia 获取临时素材（图片语音视频二进制流；video 形态返回 JSON 含 video_url
// 由调用方二次下载——按应答 Content-Type 自动分流）。
func (s *WxOAService) GetTempMedia(ctx context.Context, mediaID string) ([]byte, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeRaw(ctx, epMediaGet, map[string]string{"access_token": token, "media_id": mediaID})
}

// GetJSSDKVoice 获取高清语音素材（JSSDK uploadVoice 上传的 speex 16K 采样）。
func (s *WxOAService) GetJSSDKVoice(ctx context.Context, mediaID string) ([]byte, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeRaw(ctx, epMediaJSSDKVoice, map[string]string{"access_token": token, "media_id": mediaID})
}

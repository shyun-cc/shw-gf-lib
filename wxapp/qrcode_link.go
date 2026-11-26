package wxapp

import (
	"context"
)

// 小程序码与链接族（官方取证见 change wxapp-qrcode-userinfo proposal）。
// 图片三件应答为二进制 buffer（invokeRaw 分发：Content-Type 含 image 返回原始字节，
// 失败应答为 JSON errcode → BizError）。

// CreateWXACode 获取小程序码（path ≤1024 字符；与 CreateWXAQRCode 合计 ≤10 万个，
// 用量见响应 Header Num-Used）。
func (s *WxAppService) CreateWXACode(ctx context.Context, body map[string]any) ([]byte, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeRaw(ctx, epGetWXACode, map[string]string{"access_token": token}, body)
}

// CreateUnlimitedWXACode 获取不限制的小程序码（scene ≤32 字符且限字符集，参数只能放
// scene；5000 次/分钟；适合海量场景）。
func (s *WxAppService) CreateUnlimitedWXACode(ctx context.Context, body map[string]any) ([]byte, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeRaw(ctx, epGetWXACodeUnlimit, map[string]string{"access_token": token}, body)
}

// CreateWXAQRCode 获取小程序二维码（path ≤128 字符；仅已发布版本；与 CreateWXACode 合计 ≤10 万个）。
func (s *WxAppService) CreateWXAQRCode(ctx context.Context, body map[string]any) ([]byte, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeRaw(ctx, epCreateWXAQRCode, map[string]string{"access_token": token}, body)
}

// QueryScheme 查询 scheme 码信息（query_type 0 信息 / 1 每天剩余访问次数）。
func (s *WxAppService) QueryScheme(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epQueryScheme, map[string]string{"access_token": token}, body)
}

// GenerateScheme 获取加密 scheme 码（国内非个人主体；日生成 50 万/打开 300 万共享额度；
// ≤100 QPS；有效期 ≤30 天）。
func (s *WxAppService) GenerateScheme(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epGenerateScheme, map[string]string{"access_token": token}, body)
}

// GenerateNFCScheme 获取 NFC 的小程序 scheme（须已开通 NFC 能力；一机一码 sn 必填、
// 一型一码 sn 必空）。
func (s *WxAppService) GenerateNFCScheme(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epGenerateNFCScheme, map[string]string{"access_token": token}, body)
}

// GenerateURLLink 获取加密 URL Link（有效期 ≤30 天；cloud_base 可定制云开发 H5 中转页）。
func (s *WxAppService) GenerateURLLink(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epGenerateURLLink, map[string]string{"access_token": token}, body)
}

// QueryURLLink 查询加密 URL Link 配置（query_type 0 信息 / 1 剩余次数，与 scheme 共享额度）。
func (s *WxAppService) QueryURLLink(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epQueryURLLink, map[string]string{"access_token": token}, body)
}

// GenerateShortLink 获取 ShortLink（page_url 可带 query ≤1024；短期 30 天不限量、
// 永久 ≤10 万个；日生成 ≤1000 万）。
func (s *WxAppService) GenerateShortLink(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epGenShortLink, map[string]string{"access_token": token}, body)
}

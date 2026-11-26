package wxoa

import (
	"context"
	"net/http"
)

// 智能接口表项（语音 3 + OCR 7；官方 Open_API/ocr 两页取证 2026-08-24。
// OCR 与 wxapp 共用官方 /cv/ocr/* 接口族——公众号侧按 query img_url 形态）
var (
	epAITranslate  = Endpoint{"ai.translate", http.MethodPost, "/cgi-bin/media/voice/translatecontent", nil, true}
	epAIVoiceAdd   = Endpoint{"ai.voice_add", http.MethodPost, "/cgi-bin/media/voice/addvoicetorecofortext", nil, true}
	epAIVoiceQuery = Endpoint{"ai.voice_query", http.MethodPost, "/cgi-bin/media/voice/queryrecoresultfortext", nil, true}

	epOcrComm       = Endpoint{"ai.ocr_comm", http.MethodGet, "/cv/ocr/comm", []string{"img_url"}, false}
	epOcrIdcard     = Endpoint{"ai.ocr_idcard", http.MethodGet, "/cv/ocr/idcard", []string{"img_url"}, false}
	epOcrDriving    = Endpoint{"ai.ocr_driving", http.MethodGet, "/cv/ocr/driving", []string{"img_url"}, false}
	epOcrDrvLicense = Endpoint{"ai.ocr_drivinglicense", http.MethodGet, "/cv/ocr/drivinglicense", []string{"img_url"}, false}
	epOcrBankcard   = Endpoint{"ai.ocr_bankcard", http.MethodGet, "/cv/ocr/bankcard", []string{"img_url"}, false}
	epOcrBizlicense = Endpoint{"ai.ocr_bizlicense", http.MethodGet, "/cv/ocr/bizlicense", []string{"img_url"}, false}
	epOcrMenu       = Endpoint{"ai.ocr_menu", http.MethodGet, "/cv/ocr/menu", []string{"img_url"}, false}
)

// VoiceTranslate 微信翻译（文本内容翻译）。
func (s *WxOAService) VoiceTranslate(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epAITranslate, body)
}

// VoiceUploadReco 上传语音文件转文字（format/voice base64；返回 voice_id）。
func (s *WxOAService) VoiceUploadReco(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epAIVoiceAdd, body)
}

// VoiceQueryReco 获取语音识别结果（voice_id 轮询）。
func (s *WxOAService) VoiceQueryReco(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epAIVoiceQuery, body)
}

// ocrGet OCR 统一 GET（img_url 由微信后台下载识别）。
func (s *WxOAService) ocrGet(ctx context.Context, ep Endpoint, imgURL string) (map[string]any, error) {
	return s.oaGet(ctx, ep, map[string]string{"img_url": imgURL})
}

// OcrComm 通用印刷体识别（认证账号日 100 次免费，更高等服务市场）。
func (s *WxOAService) OcrComm(ctx context.Context, imgURL string) (map[string]any, error) {
	return s.ocrGet(ctx, epOcrComm, imgURL)
}

// OcrIdcard 身份证识别（正反面）。
func (s *WxOAService) OcrIdcard(ctx context.Context, imgURL string) (map[string]any, error) {
	return s.ocrGet(ctx, epOcrIdcard, imgURL)
}

// OcrDriving 行驶证识别。
func (s *WxOAService) OcrDriving(ctx context.Context, imgURL string) (map[string]any, error) {
	return s.ocrGet(ctx, epOcrDriving, imgURL)
}

// OcrDrivingLicense 驾驶证识别。
func (s *WxOAService) OcrDrivingLicense(ctx context.Context, imgURL string) (map[string]any, error) {
	return s.ocrGet(ctx, epOcrDrvLicense, imgURL)
}

// OcrBankcard 银行卡识别。
func (s *WxOAService) OcrBankcard(ctx context.Context, imgURL string) (map[string]any, error) {
	return s.ocrGet(ctx, epOcrBankcard, imgURL)
}

// OcrBizlicense 营业执照识别。
func (s *WxOAService) OcrBizlicense(ctx context.Context, imgURL string) (map[string]any, error) {
	return s.ocrGet(ctx, epOcrBizlicense, imgURL)
}

// OcrMenu 菜单识别（纸质菜单）。
func (s *WxOAService) OcrMenu(ctx context.Context, imgURL string) (map[string]any, error) {
	return s.ocrGet(ctx, epOcrMenu, imgURL)
}

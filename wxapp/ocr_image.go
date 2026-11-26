package wxapp

import (
	"context"
	"errors"
	"net/http"
)

// 图像处理与 OCR 表项（官方取证见 change wxapp-ocr-face-misc proposal；九件统一
// img_url（query）或 img（multipart，官方 part 名 img）二选一，图片 <2M）
var (
	epImgAiCrop   = Endpoint{"img.aicrop", http.MethodPost, "/cv/img/aicrop", []string{"access_token", "img_url", "ratios"}, false}
	epImgQrcode   = Endpoint{"img.qrcode", http.MethodPost, "/cv/img/qrcode", []string{"access_token", "img_url"}, false}
	epImgSuperRes = Endpoint{"img.superres", http.MethodPost, "/cv/img/superresolution", []string{"access_token", "img_url"}, false}

	epOCRComm           = Endpoint{"ocr.comm", http.MethodPost, "/cv/ocr/comm", []string{"access_token", "img_url"}, false}
	epOCRIdcard         = Endpoint{"ocr.idcard", http.MethodPost, "/cv/ocr/idcard", []string{"access_token", "img_url"}, false}
	epOCRDriving        = Endpoint{"ocr.driving", http.MethodPost, "/cv/ocr/driving", []string{"access_token", "img_url"}, false}
	epOCRBankcard       = Endpoint{"ocr.bankcard", http.MethodPost, "/cv/ocr/bankcard", []string{"access_token", "img_url"}, false}
	epOCRBizlicense     = Endpoint{"ocr.bizlicense", http.MethodPost, "/cv/ocr/bizlicense", []string{"access_token", "img_url"}, false}
	epOCRDrivingLicense = Endpoint{"ocr.drivinglicense", http.MethodPost, "/cv/ocr/drivinglicense", []string{"access_token", "img_url"}, false}
)

// imgOrURL 图像/OCR 九件的双形态分发：imgURL 非空走 query；img 非空走 multipart
// （官方 part 名 img）；两者均空为参数错误。
func (s *WxAppService) imgOrURL(ctx context.Context, ep Endpoint, imgURL string, img []byte, extraQuery map[string]string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	if imgURL == "" && len(img) == 0 {
		return nil, &InfraError{Op: "encode", Err: errors.New("img_url 与 img 须二选一")}
	}
	if imgURL != "" {
		query := map[string]string{"access_token": token, "img_url": imgURL}
		for k, v := range extraQuery {
			query[k] = v
		}
		return s.invoke(ctx, ep, query, nil)
	}
	query := map[string]string{"access_token": token}
	for k, v := range extraQuery {
		query[k] = v
	}
	return s.invokeMultipart(ctx, ep, query, "img", "img.bin", img)
}

// ImgAiCrop 图片智能裁剪（ratios 宽高比 ≤5 个逗号分隔，空则算法自动最佳裁剪）。
func (s *WxAppService) ImgAiCrop(ctx context.Context, imgURL string, img []byte, ratios string) (map[string]any, error) {
	var extra map[string]string
	if ratios != "" {
		extra = map[string]string{"ratios": ratios}
	}
	return s.imgOrURL(ctx, epImgAiCrop, imgURL, img, extra)
}

// ImgQrcode 二维码/条码识别（支持条码/QR/DataMatrix/PDF417，QR 与 DataMatrix 返回坐标）。
func (s *WxAppService) ImgQrcode(ctx context.Context, imgURL string, img []byte) (map[string]any, error) {
	return s.imgOrURL(ctx, epImgQrcode, imgURL, img, nil)
}

// ImgSuperResolution 图片高清化 2 倍（返回 media_id 3 天有效；官方注明因系统维护已下架，
// 如需使用须联系微信服务市场客服——表项保留待官方恢复）。
func (s *WxAppService) ImgSuperResolution(ctx context.Context, imgURL string, img []byte) (map[string]any, error) {
	return s.imgOrURL(ctx, epImgSuperRes, imgURL, img, nil)
}

// OCRComm 通用印刷体识别。
func (s *WxAppService) OCRComm(ctx context.Context, imgURL string, img []byte) (map[string]any, error) {
	return s.imgOrURL(ctx, epOCRComm, imgURL, img, nil)
}

// OCRIdcard 身份证识别（返回 type Front/Back、name、id、valid_date、addr、gender、nationality）。
func (s *WxAppService) OCRIdcard(ctx context.Context, imgURL string, img []byte) (map[string]any, error) {
	return s.imgOrURL(ctx, epOCRIdcard, imgURL, img, nil)
}

// OCRDriving 行驶证识别。
func (s *WxAppService) OCRDriving(ctx context.Context, imgURL string, img []byte) (map[string]any, error) {
	return s.imgOrURL(ctx, epOCRDriving, imgURL, img, nil)
}

// OCRBankcard 银行卡识别。
func (s *WxAppService) OCRBankcard(ctx context.Context, imgURL string, img []byte) (map[string]any, error) {
	return s.imgOrURL(ctx, epOCRBankcard, imgURL, img, nil)
}

// OCRBizlicense 营业执照识别。
func (s *WxAppService) OCRBizlicense(ctx context.Context, imgURL string, img []byte) (map[string]any, error) {
	return s.imgOrURL(ctx, epOCRBizlicense, imgURL, img, nil)
}

// OCRDrivingLicense 驾驶证识别。
func (s *WxAppService) OCRDrivingLicense(ctx context.Context, imgURL string, img []byte) (map[string]any, error) {
	return s.imgOrURL(ctx, epOCRDrivingLicense, imgURL, img, nil)
}

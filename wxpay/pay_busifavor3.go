package wxpay

import (
	"context"
	"net/http"
)

// 商家券域第三批表项（官方取证见 change wxpay-v3-busifavor3 proposal：
// 商品券 4015717371 / 过滤查券 4015717400 / 通知地址双件 4015717428·4015717465 /
// 修改基本信息 4015717486 / 退券 4015717496 / 失效 4015717499 / 营销图片上传 4015717515）
var (
	epProductCouponSend    = Endpoint{"v3.busifavor.product_coupon_send", http.MethodPost, "/v3/marketing/busifavor/product-coupons/send", nil, true}
	epUserCouponsByFilter  = Endpoint{"v3.busifavor.user_coupons_filter", http.MethodGet, "/v3/marketing/busifavor/users/{openid}/coupons", []string{"appid", "stock_id", "creator_merchant", "belong_merchant", "sender_merchant", "offset", "limit", "coupon_state"}, false}
	epBusiFavorCallbackSet = Endpoint{"v3.busifavor.callback_set", http.MethodPost, "/v3/marketing/busifavor/callbacks", nil, true}
	epBusiFavorCallbackGet = Endpoint{"v3.busifavor.callback_get", http.MethodGet, "/v3/marketing/busifavor/callbacks", []string{"mchid"}, false}
	epBusiFavorStockPatch  = Endpoint{"v3.busifavor.stock_patch", http.MethodPatch, "/v3/marketing/busifavor/stocks/{stock_id}", nil, true}
	epBusiFavorReturn      = Endpoint{"v3.busifavor.coupon_return", http.MethodPost, "/v3/marketing/busifavor/coupons/return", nil, true}
	epBusiFavorDeactivate  = Endpoint{"v3.busifavor.coupon_deactivate", http.MethodPost, "/v3/marketing/busifavor/coupons/deactivate", nil, true}
	epFavorImageUpload     = Endpoint{"v3.favor.image_upload", http.MethodPost, "/v3/marketing/favor/media/image-upload", nil, true}
)

// SendProductCoupon 向用户发放商品券（仅限已关联商品信息的批次；body 必填 openid/appid/stock_id/
// out_request_no/coupon_type，MEMBER 券须 member_card_id，MERCHANT_API 模式须 coupon_code）。
func (c *Client) SendProductCoupon(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epProductCouponSend, nil, nil, body)
}

// ListUserCouponsByFilter 根据过滤条件查询用户的商家券（query 必填 appid；调用方为批次
// 创建/归属/发放方时对应 creator_merchant/belong_merchant/sender_merchant 必传）。
func (c *Client) ListUserCouponsByFilter(ctx context.Context, openid string, query map[string]string) (map[string]any, error) {
	return c.invoke(ctx, epUserCouponsByFilter, map[string]string{"openid": openid}, query, nil)
}

// SetBusiFavorCallbacks 设置商家券事件通知地址（body 必填 notify_url，须 HTTPS；
// 需在商户平台开通「营销事件推送」能力）。
func (c *Client) SetBusiFavorCallbacks(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorCallbackSet, nil, nil, body)
}

// GetBusiFavorCallbacks 获取商家券事件通知地址（mchid 为空时不带 query，默认查询调用方自身）。
func (c *Client) GetBusiFavorCallbacks(ctx context.Context, mchid string) (map[string]any, error) {
	var query map[string]string
	if mchid != "" {
		query = map[string]string{"mchid": mchid}
	}
	return c.invoke(ctx, epBusiFavorCallbackGet, nil, query, nil)
}

// PatchBusiFavorStock 修改商家券基本信息（body 必填 out_request_no；成功应答 204 无包体）。
func (c *Client) PatchBusiFavorStock(ctx context.Context, stockID string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorStockPatch, map[string]string{"stock_id": stockID}, nil, body)
}

// ReturnBusiFavorCoupon 申请退券（券状态须为已核销 USED；body 必填 coupon_code/stock_id/return_request_no）。
func (c *Client) ReturnBusiFavorCoupon(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorReturn, nil, nil, body)
}

// DeactivateBusiFavorCoupon 使券失效（body 必填 coupon_code/stock_id/deactivate_request_no；
// 失效后券无法再被核销，应答含 wechatpay_deactivate_time）。
func (c *Client) DeactivateBusiFavorCoupon(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorDeactivate, nil, nil, body)
}

// UploadMarketingImage 图片上传（营销专用）：商家券/代金券/支付有礼等场景上传图片取回
// media_url（JPG/BMP/PNG ≤2M）。multipart 形态，Authorization 签名主体为 meta JSON 而非
// 实际 body（官方 4015717515）；sha256 由本方法计算，调用方无需提供。
func (c *Client) UploadMarketingImage(ctx context.Context, filename string, content []byte) (map[string]any, error) {
	return c.invokeMultipart(ctx, epFavorImageUpload, filename, content)
}

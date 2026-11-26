package wxpay

import (
	"context"
	"net/http"
)

// V3 营销余域表项（官方清点见 change wxpay-v3-coupon-extra proposal）
var (
	epCouponUseFlow   = Endpoint{"v3.coupon.use_flow", http.MethodGet, "/v3/marketing/favor/stocks/{stock_id}/use-flow", nil, false}
	epUserCouponsList = Endpoint{"v3.coupon.user_coupons_get", http.MethodGet, "/v3/marketing/favor/users/{openid}/coupons", []string{"appid", "stock_id", "status", "business_type", "creator_mchid", "available_mchid", "offset", "limit"}, false}
)

// DownloadCouponUseFlow 下载批次核销明细（返回 download_url；文件下载走 DownloadBill）
func (c *Client) DownloadCouponUseFlow(ctx context.Context, stockID string) (map[string]any, error) {
	return c.invoke(ctx, epCouponUseFlow, map[string]string{"stock_id": stockID}, nil, nil)
}

// ListUserCoupons 根据商户号查用户的券（query：appid 必填 + stock_id/status/creator_mchid|available_mchid/分页选填；
// GET 形态，与 POST 发放 SendCoupon 同 path 不同方法）
func (c *Client) ListUserCoupons(ctx context.Context, openid string, query map[string]string) (map[string]any, error) {
	return c.invoke(ctx, epUserCouponsList, map[string]string{"openid": openid}, query, nil)
}

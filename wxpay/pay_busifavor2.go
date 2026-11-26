package wxpay

import (
	"context"
	"net/http"
)

// V3 商家券族第二批表项（官方清点见 change wxpay-v3-busifavor2 proposal）
var (
	epBusiFavorCreate  = Endpoint{"v3.busifavor.stock_create", http.MethodPost, "/v3/marketing/busifavor/stocks", nil, true}
	epBusiFavorBudget  = Endpoint{"v3.busifavor.budget_update", http.MethodPatch, "/v3/marketing/busifavor/stocks/{stock_id}/budget", nil, true}
	epBusiFavorCodes   = Endpoint{"v3.busifavor.couponcodes_upload", http.MethodPost, "/v3/marketing/busifavor/stocks/{stock_id}/couponcodes", nil, true}
	epBusiFavorUserGet = Endpoint{"v3.busifavor.user_coupon_get", http.MethodGet, "/v3/marketing/busifavor/users/{openid}/coupons/{coupon_code}/appids/{appid}", nil, false}
)

// CreateBusiFavorStock 创建商家券批次（body：stock_name≤21 字符/belong_merchant/goods_name/stock_type/
// coupon_use_rule/stock_send_rule/out_request_no/coupon_code_mode）
func (c *Client) CreateBusiFavorStock(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorCreate, nil, nil, body)
}

// UpdateBusiFavorBudget 修改批次预算（body：modify_budget_request_no 必填 + 目标量选填）
func (c *Client) UpdateBusiFavorBudget(ctx context.Context, stockID string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorBudget, map[string]string{"stock_id": stockID}, nil, body)
}

// UploadBusiFavorCouponCodes 上传预存 CODE（body：coupon_code_list[]/upload_request_no）
func (c *Client) UploadBusiFavorCouponCodes(ctx context.Context, stockID string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorCodes, map[string]string{"stock_id": stockID}, nil, body)
}

// GetUserBusiFavorCoupon 查询用户券详情（三段路径：openid/coupon_code/appid）
func (c *Client) GetUserBusiFavorCoupon(ctx context.Context, openid, couponCode, appid string) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorUserGet, map[string]string{
		"openid": openid, "coupon_code": couponCode, "appid": appid,
	}, nil, nil)
}

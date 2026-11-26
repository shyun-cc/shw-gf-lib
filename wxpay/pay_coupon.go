package wxpay

import (
	"context"
	"net/http"
)

// V3 营销族代金券核心表项（官方清点见 change wxpay-v3-coupon proposal）
var (
	epCouponStockCreate = Endpoint{"v3.coupon.stock_create", http.MethodPost, "/v3/marketing/favor/coupon-stocks", nil, true}
	epCouponStockStart  = Endpoint{"v3.coupon.stock_start", http.MethodPost, "/v3/marketing/favor/stocks/{stock_id}/start", nil, true}
	epCouponSend        = Endpoint{"v3.coupon.send", http.MethodPost, "/v3/marketing/favor/users/{openid}/coupons", nil, true}
	epCouponGet         = Endpoint{"v3.coupon.get", http.MethodGet, "/v3/marketing/favor/users/{openid}/coupons/{coupon_id}", []string{"appid"}, false}
)

// CreateCouponStock 创建代金券批次（body：stock_name/belong_merchant/可用时间/stock_use_rule/
// coupon_use_rule/no_cash/stock_type=NORMAL/out_request_no；返回 stock_id）
func (c *Client) CreateCouponStock(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCouponStockCreate, nil, nil, body)
}

// StartCouponStock 激活代金券批次（预充值券激活时锁定营销资金；body：stock_creator_mchid）
func (c *Client) StartCouponStock(ctx context.Context, stockID string, stockCreatorMchID string) (map[string]any, error) {
	return c.invoke(ctx, epCouponStockStart, map[string]string{"stock_id": stockID},
		nil, map[string]any{"stock_creator_mchid": stockCreatorMchID})
}

// SendCoupon 发放指定批次代金券（body：stock_id/out_request_no/appid/stock_creator_mchid）
func (c *Client) SendCoupon(ctx context.Context, openid string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCouponSend, map[string]string{"openid": openid}, nil, body)
}

// GetCoupon 查询代金券详情（query：appid 与 openid 绑定关系匹配）
func (c *Client) GetCoupon(ctx context.Context, openid, couponID, appid string) (map[string]any, error) {
	return c.invoke(ctx, epCouponGet, map[string]string{"openid": openid, "coupon_id": couponID},
		map[string]string{"appid": appid}, nil)
}

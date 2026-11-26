package wxpay

import (
	"context"
	"net/http"
)

// V3 商家券族核心表项（官方清点见 change wxpay-v3-busifavor proposal）
var (
	epBusiFavorGet   = Endpoint{"v3.busifavor.stock_get", http.MethodGet, "/v3/marketing/busifavor/stocks/{stock_id}", nil, false}
	epBusiFavorPatch = Endpoint{"v3.busifavor.stock_update", http.MethodPatch, "/v3/marketing/busifavor/stocks/{stock_id}", nil, true}
	epBusiFavorUse   = Endpoint{"v3.busifavor.coupon_use", http.MethodPost, "/v3/marketing/busifavor/coupons/use", nil, true}
)

// GetBusiFavorStock 查询商家券批次详情
func (c *Client) GetBusiFavorStock(ctx context.Context, stockID string) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorGet, map[string]string{"stock_id": stockID}, nil, nil)
}

// UpdateBusiFavorStock 修改商家券基本信息（PATCH；body：out_request_no 必填，余选填——
// 商户 logo 须图片上传 API 的 URL，120×120 JPG/PNG <1M）
func (c *Client) UpdateBusiFavorStock(ctx context.Context, stockID string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorPatch, map[string]string{"stock_id": stockID}, nil, body)
}

// UseBusiFavorCoupon 核销用户的券（body：coupon_code/appid/use_time/use_request_no；
// 自定义 code 模式 stock_id 必传；500 QPS）
func (c *Client) UseBusiFavorCoupon(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorUse, nil, nil, body)
}

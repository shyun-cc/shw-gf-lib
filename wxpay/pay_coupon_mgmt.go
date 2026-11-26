package wxpay

import (
	"context"
	"net/http"
)

// V3 营销族代金券批次管理表项（官方清点见 change wxpay-v3-coupon-mgmt proposal）
var (
	epCouponStockList    = Endpoint{"v3.coupon.stock_list", http.MethodGet, "/v3/marketing/favor/stocks", []string{"offset", "limit", "stock_creator_mchid", "create_start_time", "create_end_time", "status"}, false}
	epCouponStockGet     = Endpoint{"v3.coupon.stock_get", http.MethodGet, "/v3/marketing/favor/stocks/{stock_id}", []string{"stock_creator_mchid"}, false}
	epCouponStockPause   = Endpoint{"v3.coupon.stock_pause", http.MethodPost, "/v3/marketing/favor/stocks/{stock_id}/pause", nil, true}
	epCouponStockRestart = Endpoint{"v3.coupon.stock_restart", http.MethodPost, "/v3/marketing/favor/stocks/{stock_id}/restart", nil, true}
)

// ListCouponStocks 条件查询批次列表（query：offset/limit≤10/stock_creator_mchid 必填 +
// create_start_time/create_end_time（RFC3339）/status（unactivated/audit/running/stoped/paused）选填）
func (c *Client) ListCouponStocks(ctx context.Context, query map[string]string) (map[string]any, error) {
	return c.invoke(ctx, epCouponStockList, nil, query, nil)
}

// GetCouponStock 查询批次详情（query：stock_creator_mchid）
func (c *Client) GetCouponStock(ctx context.Context, stockID, stockCreatorMchID string) (map[string]any, error) {
	return c.invoke(ctx, epCouponStockGet, map[string]string{"stock_id": stockID},
		map[string]string{"stock_creator_mchid": stockCreatorMchID}, nil)
}

// PauseCouponStock 暂停代金券批次（暂停后用户无法领取）
func (c *Client) PauseCouponStock(ctx context.Context, stockID, stockCreatorMchID string) (map[string]any, error) {
	return c.invoke(ctx, epCouponStockPause, map[string]string{"stock_id": stockID},
		nil, map[string]any{"stock_creator_mchid": stockCreatorMchID})
}

// RestartCouponStock 重启代金券批次（仅全场/单品券，不支持立减与折扣）
func (c *Client) RestartCouponStock(ctx context.Context, stockID, stockCreatorMchID string) (map[string]any, error) {
	return c.invoke(ctx, epCouponStockRestart, map[string]string{"stock_id": stockID},
		nil, map[string]any{"stock_creator_mchid": stockCreatorMchID})
}

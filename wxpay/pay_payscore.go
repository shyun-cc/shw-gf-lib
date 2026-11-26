package wxpay

import (
	"context"
	"net/http"
)

// V3 支付分族表项（官方清点见 change wxpay-v3-payscore proposal）
var (
	epPayScoreCreate   = Endpoint{"v3.payscore.serviceorder_create", http.MethodPost, "/v3/payscore/serviceorder", nil, true}
	epPayScoreQuery    = Endpoint{"v3.payscore.serviceorder_query", http.MethodGet, "/v3/payscore/serviceorder", []string{"service_id", "appid", "out_order_no", "query_id"}, false}
	epPayScoreCancel   = Endpoint{"v3.payscore.serviceorder_cancel", http.MethodPost, "/v3/payscore/serviceorder/{out_order_no}/cancel", nil, true}
	epPayScoreComplete = Endpoint{"v3.payscore.serviceorder_complete", http.MethodPost, "/v3/payscore/serviceorder/{out_order_no}/complete", nil, true}
)

// CreatePayScoreOrder 创建支付分订单（body：out_order_no/appid/service_id/service_introduction/
// time_range/risk_fund/notify_url；状态机 CREATED→DOING→DONE/REVOKED/EXPIRED）
func (c *Client) CreatePayScoreOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPayScoreCreate, nil, nil, body)
}

// QueryPayScoreOrder 查询支付分订单（query：service_id/appid 必填 + out_order_no/query_id 二选一）
func (c *Client) QueryPayScoreOrder(ctx context.Context, query map[string]string) (map[string]any, error) {
	return c.invoke(ctx, epPayScoreQuery, nil, query, nil)
}

// CancelPayScoreOrder 取消支付分订单（body：appid（与创单一致）/reason≤50 字符）
func (c *Client) CancelPayScoreOrder(ctx context.Context, outOrderNo string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPayScoreCancel, map[string]string{"out_order_no": outOrderNo}, nil, body)
}

// CompletePayScoreOrder 完结支付分订单（body：appid/service_id/post_payments[{name}]）
func (c *Client) CompletePayScoreOrder(ctx context.Context, outOrderNo string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPayScoreComplete, map[string]string{"out_order_no": outOrderNo}, nil, body)
}

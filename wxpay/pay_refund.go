package wxpay

import (
	"context"
	"net/http"
)

// V3 合单余三变体 + 商户订单号查单 + 退款族表项（官方清点见 change wxpay-v3-refund proposal）
var (
	epCombineJSAPI   = Endpoint{"v3.combine.transactions.jsapi", http.MethodPost, "/v3/combine-transactions/jsapi", nil, true}
	epCombineAPP     = Endpoint{"v3.combine.transactions.app", http.MethodPost, "/v3/combine-transactions/app", nil, true}
	epCombineH5      = Endpoint{"v3.combine.transactions.h5", http.MethodPost, "/v3/combine-transactions/h5", nil, true}
	epQueryTxByNo    = Endpoint{"v3.pay.transactions.out_trade_no", http.MethodGet, "/v3/pay/transactions/out-trade-no/{out_trade_no}", []string{"mchid"}, false}
	epCreateRefund   = Endpoint{"v3.refund.create", http.MethodPost, "/v3/refund/domestic/refunds", nil, true}
	epQueryRefund    = Endpoint{"v3.refund.query", http.MethodGet, "/v3/refund/domestic/refunds/{out_refund_no}", nil, false}
	epAbnormalRefund = Endpoint{"v3.refund.apply_abnormal", http.MethodPost, "/v3/refund/domestic/refunds/{refund_id}/apply-abnormal-refund", nil, true}
)

// PrepayCombineJSAPI JSAPI 合单下单（body：combine_appid/combine_mchid/combine_out_trade_no/
// sub_orders（2-10，每笔 mchid/out_trade_no/attach/description/amount{total_amount,currency}）/
// combine_payer_info/notify_url；返回 prepay_id 2 小时有效）
func (c *Client) PrepayCombineJSAPI(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCombineJSAPI, nil, nil, body)
}

// PrepayCombineAPP APP 合单下单
func (c *Client) PrepayCombineAPP(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCombineAPP, nil, nil, body)
}

// PrepayCombineH5 H5 合单下单
func (c *Client) PrepayCombineH5(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCombineH5, nil, nil, body)
}

// QueryTransactionByOutTradeNo 商户订单号查单（query：mchid）
func (c *Client) QueryTransactionByOutTradeNo(ctx context.Context, outTradeNo string) (map[string]any, error) {
	return c.invoke(ctx, epQueryTxByNo, map[string]string{"out_trade_no": outTradeNo},
		map[string]string{"mchid": c.Creds.MchID}, nil)
}

// CreateRefund 申请退款（body：out_refund_no + amount{refund,total,currency} +
// transaction_id/out_trade_no 二选一；退款回调 event_type=REFUND.* 走 VerifyNotify+DecryptResource）
func (c *Client) CreateRefund(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCreateRefund, nil, nil, body)
}

// QueryRefund 查询单笔退款（按商户退款单号）
func (c *Client) QueryRefund(ctx context.Context, outRefundNo string) (map[string]any, error) {
	return c.invoke(ctx, epQueryRefund, map[string]string{"out_refund_no": outRefundNo}, nil, nil)
}

// ApplyAbnormalRefund 发起异常退款（body：out_refund_no/type；type=USER_BANK_CARD 时 bank_* 实必填）
func (c *Client) ApplyAbnormalRefund(ctx context.Context, refundID string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAbnormalRefund, map[string]string{"refund_id": refundID}, nil, body)
}

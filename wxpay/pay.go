package wxpay

import (
	"context"
)

// PrepayJSAPI JSAPI/小程序下单（生成 prepay_id）。body：appid/mchid/description/out_trade_no/
// notify_url/amount.total/payer.openid（appid 可省——Client 已持 Credentials.AppID 时由调用方决定是否重复携带）
func (c *Client) PrepayJSAPI(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epJSAPITx, nil, nil, body)
}

// PrepayAPP APP 下单（body 形态同 JSAPI，无 payer）
func (c *Client) PrepayAPP(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAPPTx, nil, nil, body)
}

// PrepayH5 H5 下单（额外必填 scene_info{payer_client_ip,h5_info}；返回 h5_url 5 分钟有效）
func (c *Client) PrepayH5(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epH5Tx, nil, nil, body)
}

// PrepayNative Native 下单（返回 code_url 供二维码生成）
func (c *Client) PrepayNative(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epNativeTx, nil, nil, body)
}

// PrepayCombineNative Native 合单下单（body：combine_appid/combine_out_trade_no/combine_mchid/
// sub_orders（2-10）/notify_url）
func (c *Client) PrepayCombineNative(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epCombineNative, nil, nil, body)
}

// QueryTransactionByID 微信支付订单号查单（query：mchid）
func (c *Client) QueryTransactionByID(ctx context.Context, transactionID string) (map[string]any, error) {
	return c.invoke(ctx, epQueryTxByID, map[string]string{"transaction_id": transactionID},
		map[string]string{"mchid": c.Creds.MchID}, nil)
}

// CloseTransaction 关闭订单（body：mchid；成功 204 无包体）
func (c *Client) CloseTransaction(ctx context.Context, outTradeNo string) (map[string]any, error) {
	return c.invoke(ctx, epCloseTx, map[string]string{"out_trade_no": outTradeNo}, nil,
		map[string]any{"mchid": c.Creds.MchID})
}

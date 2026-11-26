package wxpay

import (
	"context"
	"net/http"
)

// V3 营销通知地址设置表项（官方清点见 change wxpay-v3-callbacks proposal）
var (
	epFavorNotify     = Endpoint{"v3.coupon.favor_callbacks", http.MethodPost, "/v3/marketing/favor/callbacks", nil, true}
	epBusiFavorNotify = Endpoint{"v3.coupon.busifavor_callbacks", http.MethodPost, "/v3/marketing/busifavor/callbacks", nil, true}
)

// SetFavorNotifyURL 设置代金券消息通知地址（核销事件推送前提；notify_url 须 https 且不带参数）
func (c *Client) SetFavorNotifyURL(ctx context.Context, mchid, notifyURL string, enabled bool) (map[string]any, error) {
	return c.invoke(ctx, epFavorNotify, nil, nil, map[string]any{
		"mchid": mchid, "notify_url": notifyURL, "switch": enabled,
	})
}

// SetBusiFavorNotifyURL 设置商家券事件通知地址（notify_url 须 https）
func (c *Client) SetBusiFavorNotifyURL(ctx context.Context, notifyURL string) (map[string]any, error) {
	return c.invoke(ctx, epBusiFavorNotify, nil, nil, map[string]any{"notify_url": notifyURL})
}

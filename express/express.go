// Package express 快递物流抽象：Provider 9 方法接口（对应 PHP ExpressProviderInterface）。
//
// 追踪域 3 方法：track / subscribe / handleTrackingCallback；
// 下单域 6 方法：createOrder / cancelOrder / modifyOrder / getOrderDetail / queryPrice / handleOrderCallback。
// 业务参数以 map[string]any 透传（库不做快递平台业务字段校验）；
// kd100 实现见子包 express/kd100。
package express

import "context"

// Provider 快递物流 Provider 接口（9 方法）
type Provider interface {
	// Track 实时查询物流轨迹（com=快递公司编码，num=运单号）
	Track(ctx context.Context, com, num string) (map[string]any, error)
	// Subscribe 订阅物流推送（param 含 com/num/callback_url 等）
	Subscribe(ctx context.Context, param map[string]any) (map[string]any, error)
	// HandleTrackingCallback 物流回调验签（param=原始 param 字段 JSON 串，sign=回调签名）
	HandleTrackingCallback(param, sign string) error

	// CreateOrder 寄件下单
	CreateOrder(ctx context.Context, param map[string]any) (map[string]any, error)
	// CancelOrder 取消下单
	CancelOrder(ctx context.Context, param map[string]any) (map[string]any, error)
	// ModifyOrder 修改订单
	ModifyOrder(ctx context.Context, param map[string]any) (map[string]any, error)
	// GetOrderDetail 查询订单详情
	GetOrderDetail(ctx context.Context, param map[string]any) (map[string]any, error)
	// QueryPrice 价格查询（支持批量）
	QueryPrice(ctx context.Context, param map[string]any) (map[string]any, error)
	// HandleOrderCallback 订单回调验签（param/sign 同追踪域；t=下单类时间戳）
	HandleOrderCallback(param, sign, t string) error
}

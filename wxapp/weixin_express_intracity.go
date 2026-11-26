package wxapp

import (
	"context"
	"net/http"
)

// 微信物流服务·同城配送表项（16 端点；官方取证见 change wxapp-weixin-express proposal）
var (
	epCityApply       = Endpoint{"wexp.city_apply", http.MethodPost, "/cgi-bin/express/intracity/apply", []string{"access_token"}, false}
	epCityCreateStore = Endpoint{"wexp.city_createstore", http.MethodPost, "/cgi-bin/express/intracity/createstore", []string{"access_token"}, true}
	epCityQueryStore  = Endpoint{"wexp.city_querystore", http.MethodPost, "/cgi-bin/express/intracity/querystore", []string{"access_token"}, true}
	epCityUpdateStore = Endpoint{"wexp.city_updatestore", http.MethodPost, "/cgi-bin/express/intracity/updatestore", []string{"access_token"}, true}
	epCityCharge      = Endpoint{"wexp.city_storecharge", http.MethodPost, "/cgi-bin/express/intracity/storecharge", []string{"access_token"}, true}
	epCityRefund      = Endpoint{"wexp.city_storerefund", http.MethodPost, "/cgi-bin/express/intracity/storerefund", []string{"access_token"}, true}
	epCityQueryFlow   = Endpoint{"wexp.city_queryflow", http.MethodPost, "/cgi-bin/express/intracity/queryflow", []string{"access_token"}, true}
	epCityBalance     = Endpoint{"wexp.city_balancequery", http.MethodPost, "/cgi-bin/express/intracity/balancequery", []string{"access_token"}, true}
	epCityPreAddOrder = Endpoint{"wexp.city_preaddorder", http.MethodPost, "/cgi-bin/express/intracity/preaddorder", []string{"access_token"}, true}
	epCityAddOrder    = Endpoint{"wexp.city_addorder", http.MethodPost, "/cgi-bin/express/intracity/addorder", []string{"access_token"}, true}
	epCityQueryOrder  = Endpoint{"wexp.city_queryorder", http.MethodPost, "/cgi-bin/express/intracity/queryorder", []string{"access_token"}, true}
	epCityCancelOrder = Endpoint{"wexp.city_cancelorder", http.MethodPost, "/cgi-bin/express/intracity/cancelorder", []string{"access_token"}, true}
	epCitySetPayMode  = Endpoint{"wexp.city_setpaymode", http.MethodPost, "/cgi-bin/express/intracity/setpaymode", []string{"access_token"}, true}
	epCityGetPayMode  = Endpoint{"wexp.city_getpaymode", http.MethodPost, "/cgi-bin/express/intracity/getpaymode", []string{"access_token"}, true}
	epCityGetCity     = Endpoint{"wexp.city_getcity", http.MethodPost, "/cgi-bin/express/intracity/getcity", []string{"access_token"}, true}
	epCityMockNotify  = Endpoint{"wexp.city_mocknotify", http.MethodPost, "/cgi-bin/express/intracity/mocknotify", []string{"access_token"}, true}
)

// ApplyIntracity 开通门店权限（无 body）。
func (s *WxAppService) ApplyIntracity(ctx context.Context) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityApply, nil)
}

// CreateIntracityStore 创建门店（address_info 省市区/门牌/经纬度/phone；out_store_id 唯一；
// order_pattern 1 价格优先/2 运力优先须 service_trans_prefer DADA|SFTC；返回 wx_store_id）。
func (s *WxAppService) CreateIntracityStore(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityCreateStore, body)
}

// QueryIntracityStore 查询门店（wx_store_id/out_store_id 选填，空=本小程序全部门店）。
func (s *WxAppService) QueryIntracityStore(ctx context.Context, wxStoreID, outStoreID string) (map[string]any, error) {
	body := map[string]any{}
	if wxStoreID != "" {
		body["wx_store_id"] = wxStoreID
	}
	if outStoreID != "" {
		body["out_store_id"] = outStoreID
	}
	return s.wexpInvoke(ctx, epCityQueryStore, body)
}

// UpdateIntracityStore 更新门店（keys 编号二选一 + content 更新内容）。
func (s *WxAppService) UpdateIntracityStore(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityUpdateStore, body)
}

// ChargeIntracity 门店运费充值（service_trans_id DADA/SFTC + amount 分 50 元起 + pay_mode
// 三态默认门店；充值有效期 1 个月超期原路退回；返回 payurl）。
func (s *WxAppService) ChargeIntracity(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityCharge, body)
}

// RefundIntracity 门店运费退款（在途配送订单须完结；5 分钟到账）。
func (s *WxAppService) RefundIntracity(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityRefund, body)
}

// QueryIntracityFlow 门店运费流水查询（flow_type 1 充值/2 消费/3 退款 + pay_mode；
// 时间缺省近 90 天）。
func (s *WxAppService) QueryIntracityFlow(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityQueryFlow, body)
}

// QueryIntracityBalance 门店余额查询（service_trans_id 选填，空=全部运力余额明细）。
func (s *WxAppService) QueryIntracityBalance(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityBalance, body)
}

// PreAddIntracityOrder 查询运费（询价数分钟内有效——达达约 3 分钟；实际以下单接口为准）。
func (s *WxAppService) PreAddIntracityOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityPreAddOrder, body)
}

// AddIntracityOrder 创建配送单（store_order_id 同店唯一、重复传入幂等重入；
// verify_code_type 0 不生成/1 取货码/2 收货码/3 都生成；use_sandbox=1 免充值测试）。
func (s *WxAppService) AddIntracityOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityAddOrder, body)
}

// QueryIntracityOrder 查询配送单（wx_store_id+store_order_id 成对 或 wx_order_id 单独，二选一）。
func (s *WxAppService) QueryIntracityOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityQueryOrder, body)
}

// CancelIntracityOrder 取消配送单（cancel_reason_id 1 不需要/2 填错/3 无人接单/99 其他；
// 配送中取消扣违约金：顺丰接单 2 分钟内/达达 1 分钟内 ¥2）。
func (s *WxAppService) CancelIntracityOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityCancelOrder, body)
}

// SetIntracityPayMode 设置扣费主体（pay_mode 门店/小程序/服务商；设置后须管理员点模板
// 消息确认方生效）。
func (s *WxAppService) SetIntracityPayMode(ctx context.Context, appid, payMode string) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCitySetPayMode, map[string]any{"appid": appid, "pay_mode": payMode})
}

// GetIntracityPayMode 查询扣费主体。
func (s *WxAppService) GetIntracityPayMode(ctx context.Context, appid string) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityGetPayMode, map[string]any{"appid": appid})
}

// GetIntracityCities 查询支持同城配送的城市（service_trans_id 选填空=全部运力）。
func (s *WxAppService) GetIntracityCities(ctx context.Context, serviceTransID string) (map[string]any, error) {
	body := map[string]any{}
	if serviceTransID != "" {
		body["service_trans_id"] = serviceTransID
	}
	return s.wexpInvoke(ctx, epCityGetCity, body)
}

// MockIntracityNotify 模拟回调（沙盒联调：order_status 30000 接单/40000 到店/70000 完成；
// 主动触发订单状态回调）。
func (s *WxAppService) MockIntracityNotify(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epCityMockNotify, body)
}

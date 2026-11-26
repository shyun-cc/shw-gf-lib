package wxapp

import (
	"context"
	"net/http"
)

// 即时配送表项（商户侧 14 + 运力侧 1；官方取证见 change wxapp-express-delivery proposal。
// delivery_sign 签名语义：SHA1(shopid[+shop_order_id]+AppSecret)——AppSecret 不入库，
// 签名由调用方计算后传入 body）
var (
	epLocalDeliveryGet = Endpoint{"local.delivery_getall", http.MethodPost, "/cgi-bin/express/local/business/delivery/getall", []string{"access_token"}, false}
	epLocalOrderPreAdd = Endpoint{"local.order_pre_add", http.MethodPost, "/cgi-bin/express/local/business/order/pre_add", []string{"access_token"}, true}
	epLocalShopGet     = Endpoint{"local.shop_get", http.MethodPost, "/cgi-bin/express/local/business/shop/get", []string{"access_token"}, false}
	epLocalPreCancel   = Endpoint{"local.order_precancel", http.MethodPost, "/cgi-bin/express/local/business/order/precancel", []string{"access_token"}, true}
	epLocalOpen        = Endpoint{"local.open", http.MethodPost, "/cgi-bin/express/local/business/open", []string{"access_token"}, false}
	epLocalShopAdd     = Endpoint{"local.shop_add", http.MethodPost, "/cgi-bin/express/local/business/shop/add", []string{"access_token"}, true}
	epLocalOrderReAdd  = Endpoint{"local.order_readd", http.MethodPost, "/cgi-bin/express/local/business/order/readd", []string{"access_token"}, true}
	epLocalRealMock    = Endpoint{"local.realmock_update", http.MethodPost, "/cgi-bin/express/local/business/realmock_update_order", []string{"access_token"}, true}
	epLocalMock        = Endpoint{"local.mock_update", http.MethodPost, "/cgi-bin/express/local/business/test_update_order", []string{"access_token"}, true}
	epLocalOrderGet    = Endpoint{"local.order_get", http.MethodPost, "/cgi-bin/express/local/business/order/get", []string{"access_token"}, true}
	epLocalConfirmRet  = Endpoint{"local.order_confirm_return", http.MethodPost, "/cgi-bin/express/local/business/order/confirm_return", []string{"access_token"}, true}
	epLocalOrderCancel = Endpoint{"local.order_cancel", http.MethodPost, "/cgi-bin/express/local/business/order/cancel", []string{"access_token"}, true}
	epLocalAddTips     = Endpoint{"local.order_addtips", http.MethodPost, "/cgi-bin/express/local/business/order/addtips", []string{"access_token"}, true}
	epLocalOrderAdd    = Endpoint{"local.order_add", http.MethodPost, "/cgi-bin/express/local/business/order/add", []string{"access_token"}, true}
	epLocalProvUpdate  = Endpoint{"local.provider_update_order", http.MethodPost, "/cgi-bin/express/local/delivery/update_order", []string{"access_token"}, true}
)

// localInvoke 即时配送 POST 分发（无 body 端点传 nil——nil map 须转接口 nil，
// 否则 json.Marshal(map(nil)) 产出字面 "null" body）。
func (s *WxAppService) localInvoke(ctx context.Context, ep Endpoint, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return s.invoke(ctx, ep, map[string]string{"access_token": token}, nil)
	}
	return s.invoke(ctx, ep, map[string]string{"access_token": token}, body)
}

// GetAllImmeDeliveries 获取已支持的配送公司列表（无 body）。
func (s *WxAppService) GetAllImmeDeliveries(ctx context.Context) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalDeliveryGet, nil)
}

// PreAddLocalOrder 预下配送单（十二参数族；返回 fee 族与 delivery_token——达达锁价 3 分钟）。
func (s *WxAppService) PreAddLocalOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalOrderPreAdd, body)
}

// GetBindAccounts 拉取已绑定配送账号（audit_result 0 通过/1 审核中/2 不通过）。
func (s *WxAppService) GetBindAccounts(ctx context.Context) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalShopGet, nil)
}

// PreCancelLocalOrder 预取消配送单（返回 deduct_fee 预计违约金；达达仅骑手取货前可取消）。
func (s *WxAppService) PreCancelLocalOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalPreCancel, body)
}

// OpenDelivery 申请开通即时配送（第三方服务商专用，无 body；管理员收模板消息完成开通）。
func (s *WxAppService) OpenDelivery(ctx context.Context) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalOpen, nil)
}

// BindLocalAccount 发起绑定配送公司请求（delivery_id；管理员收模板消息到配送公司网站完成）。
func (s *WxAppService) BindLocalAccount(ctx context.Context, deliveryID string) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalShopAdd, map[string]any{"delivery_id": deliveryID})
}

// ReAddLocalOrder 重新下单（同下单族+delivery_token；**美团不支持重下**——须改 shop_order_id
// 走 AddLocalOrder）。
func (s *WxAppService) ReAddLocalOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalOrderReAdd, body)
}

// RealMockUpdateOrder 模拟更新配送单状态（商户侧测试）。
func (s *WxAppService) RealMockUpdateOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalRealMock, body)
}

// MockUpdateOrder 模拟配送公司更新配送单状态（沙盒：shopid 固定 test_shop_id）。
func (s *WxAppService) MockUpdateOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalMock, body)
}

// GetLocalOrder 拉取配送单信息（order_status 102 已分配/202 已取货/301 配送中/302 成功；
// 返回骑手信息与 reach_time）。
func (s *WxAppService) GetLocalOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalOrderGet, body)
}

// AbnormalConfirmReturn 异常件退回商家确认（waybill_id + delivery_sign）。
func (s *WxAppService) AbnormalConfirmReturn(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalConfirmRet, body)
}

// CancelLocalOrder 取消配送单（cancel_reason_id 1-6、6 须文字说明；返回 deduct_fee 违约金）。
func (s *WxAppService) CancelLocalOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalOrderCancel, body)
}

// AddLocalTips 添加小费（仅待接单态、逐次递增；顺丰 ≤10 次 200 元封顶/闪送固定档位/
// 达达 ≤30 元/**美团不支持**）。
func (s *WxAppService) AddLocalTips(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalAddTips, body)
}

// AddLocalOrder 添加配送单（正式下单；delivery_token 来自预下单锁价；终态 302 成功或
// 失败族 103/203/204/205/401/501/502）。
func (s *WxAppService) AddLocalOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalOrderAdd, body)
}

// UpdateLocalDeliveryOrder 运力方更新配送单状态（wx_token 取自下单事件；骑手接单时须带
// agent{name,phone} 与 expected_delivery_time）。
func (s *WxAppService) UpdateLocalDeliveryOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.localInvoke(ctx, epLocalProvUpdate, body)
}

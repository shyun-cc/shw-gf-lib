package wxapp

import (
	"context"
	"net/http"
)

// 小程序交易管理服务表项（发货信息管理族；官方取证见 change wxapp-trade proposal）
var (
	epTradeUploadShip   = Endpoint{"trade.upload_shipping", http.MethodPost, "/wxa/sec/order/upload_shipping_info", []string{"access_token"}, true}
	epTradeUploadComb   = Endpoint{"trade.upload_combined", http.MethodPost, "/wxa/sec/order/upload_combined_shipping_info", []string{"access_token"}, true}
	epTradeGetOrder     = Endpoint{"trade.get_order", http.MethodPost, "/wxa/sec/order/get_order", []string{"access_token"}, true}
	epTradeGetOrderList = Endpoint{"trade.get_order_list", http.MethodPost, "/wxa/sec/order/get_order_list", []string{"access_token"}, true}
	epTradeNotifyRecv   = Endpoint{"trade.notify_receive", http.MethodPost, "/wxa/sec/order/notify_confirm_receive", []string{"access_token"}, true}
	epTradeSetJumpPath  = Endpoint{"trade.set_jump_path", http.MethodPost, "/wxa/sec/order/set_msg_jump_path", []string{"access_token"}, true}
	epTradeIsManaged    = Endpoint{"trade.is_managed", http.MethodPost, "/wxa/sec/order/is_trade_managed", []string{"access_token"}, true}
	epTradeIsConfirm    = Endpoint{"trade.is_confirm_done", http.MethodPost, "/wxa/sec/order/is_trade_management_confirmation_completed", []string{"access_token"}, true}
	epTradeOpSpecial    = Endpoint{"trade.op_special", http.MethodPost, "/wxa/sec/order/opspecialorder", []string{"access_token"}, true}
	epTradeBrandApply   = Endpoint{"trade.brand_apply", http.MethodPost, "/wxa/sec/famousbrand/apply", []string{"access_token"}, true}
	epTradeBrandStatus  = Endpoint{"trade.brand_status", http.MethodPost, "/wxa/sec/famousbrand/get_status", []string{"access_token"}, true}
	epTradeSetType      = Endpoint{"trade.set_trade_type", http.MethodPost, "/wxa/sec/order/setwxatradetypecgi", []string{"access_token"}, true}
)

// UploadShippingInfo 发货信息录入（一个支付单仅可更新一次；logistics_type 1 实体/2 同城/
// 3 虚拟/4 自提；shipping_list 1-15 条 item_desc ≤120 字；顺丰须掩码 contact；
// delivery_mode 2 分拆时 is_all_delivered 必填）。
func (s *WxAppService) UploadShippingInfo(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeUploadShip, map[string]string{"access_token": token}, body)
}

// UploadCombinedShippingInfo 发货信息合单录入（主单 order_key + sub_orders 子单族，
// 子单单号类型须与主单一致；分拆发货仅支持快递 ≤15 包裹；已完成发货后再调视为重新发货
// ——每支付单仅一次机会）。
func (s *WxAppService) UploadCombinedShippingInfo(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeUploadComb, map[string]string{"access_token": token}, body)
}

// GetTradeOrder 查询订单发货状态（transaction_id 或 merchant_id+merchant_trade_no 二选一）。
func (s *WxAppService) GetTradeOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeGetOrder, map[string]string{"access_token": token}, body)
}

// GetTradeOrderList 查询订单列表（order_state 1 待发货-6 资金待结算；last_index 翻页
// 默认 page_size 100）。
func (s *WxAppService) GetTradeOrderList(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeGetOrderList, map[string]string{"access_token": token}, body)
}

// NotifyConfirmReceive 确认收货提醒（仅物流快递类型；签收时间须晚于发货时间；每单一次）。
func (s *WxAppService) NotifyConfirmReceive(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeNotifyRecv, map[string]string{"access_token": token}, body)
}

// SetMsgJumpPath 消息跳转路径设置（平台自动追加单号 query；路径空或不存在回退默认页）。
func (s *WxAppService) SetMsgJumpPath(ctx context.Context, path string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeSetJumpPath, map[string]string{"access_token": token}, map[string]string{"path": path})
}

// IsTradeManaged 查询是否开通发货信息管理服务（appid；非服务商仅可查本账号）。
func (s *WxAppService) IsTradeManaged(ctx context.Context, appid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeIsManaged, map[string]string{"access_token": token}, map[string]string{"appid": appid})
}

// IsTradeManagementConfirmationCompleted 查询交易结算管理确认完成状态（completed 布尔）。
func (s *WxAppService) IsTradeManagementConfirmationCompleted(ctx context.Context, appid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeIsConfirm, map[string]string{"access_token": token}, map[string]string{"appid": appid})
}

// OpSpecialOrder 特殊发货报备（type 1 预售 delay_to 须支付后 15-330 天 / 2 测试订单免发货）。
func (s *WxAppService) OpSpecialOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeOpSpecial, map[string]string{"access_token": token}, body)
}

// FamousBrandApply 品牌申请（apply_for 1 知名品牌须 audit_info（brand_type 1-8 条件佐证
// 材料 media_id）/ 2 已接入小店商品组件）。
func (s *WxAppService) FamousBrandApply(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeBrandApply, map[string]string{"access_token": token}, body)
}

// GetFamousBrandApplyStatus 品牌申请状态查询（无业务参数；status 1 审核中/2 驳回含
// audit_reason/3 通过）。
func (s *WxAppService) GetFamousBrandApplyStatus(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeBrandStatus, map[string]string{"access_token": token}, map[string]any{})
}

// SetTradeType 交易类型变更申请（trade_type 1 综合/2 实物电商/3 线下服务/4 在线服务；
// material_list ≤10 个视频 ≤3；每账号仅可申请一次）。
func (s *WxAppService) SetTradeType(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTradeSetType, map[string]string{"access_token": token}, body)
}

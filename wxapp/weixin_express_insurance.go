package wxapp

import (
	"context"
	"net/http"
)

// 微信物流服务·无忧退货 11 + 退货组件 3 表项（官方取证见 change wxapp-weixin-express proposal）
var (
	epInsOpen        = Endpoint{"wexp.ins_open", http.MethodGet, "/wxa/business/insurance_freight/open", []string{"access_token"}, false}
	epInsQueryOpen   = Endpoint{"wexp.ins_query_open", http.MethodGet, "/wxa/business/insurance_freight/query_open", []string{"access_token"}, false}
	epInsCreateOrder = Endpoint{"wexp.ins_createorder", http.MethodPost, "/wxa/business/insurance_freight/createorder", []string{"access_token"}, true}
	epInsClaim       = Endpoint{"wexp.ins_claim", http.MethodPost, "/wxa/business/insurance_freight/claim", []string{"access_token"}, true}
	epInsChargeID    = Endpoint{"wexp.ins_createchargeid", http.MethodPost, "/wxa/business/insurance_freight/createchargeid", []string{"access_token"}, true}
	epInsApplyPay    = Endpoint{"wexp.ins_applypay", http.MethodPost, "/wxa/business/insurance_freight/applypay", []string{"access_token"}, true}
	epInsPayOrders   = Endpoint{"wexp.ins_getpayorderlist", http.MethodPost, "/wxa/business/insurance_freight/getpayorderlist", []string{"access_token"}, true}
	epInsRefund      = Endpoint{"wexp.ins_refund", http.MethodPost, "/wxa/business/insurance_freight/refund", []string{"access_token"}, false}
	epInsSummary     = Endpoint{"wexp.ins_getsummary", http.MethodPost, "/wxa/business/insurance_freight/getsummary", []string{"access_token"}, true}
	epInsOrders      = Endpoint{"wexp.ins_getorderlist", http.MethodPost, "/wxa/business/insurance_freight/getorderlist", []string{"access_token"}, true}
	epInsNotifyFunds = Endpoint{"wexp.ins_notify_funds", http.MethodPost, "/wxa/business/insurance_freight/update_notify_funds", []string{"access_token"}, true}

	epRetAdd    = Endpoint{"wexp.ret_add", http.MethodPost, "/cgi-bin/express/delivery/no_worry_return/add", []string{"access_token"}, true}
	epRetGet    = Endpoint{"wexp.ret_get", http.MethodPost, "/cgi-bin/express/delivery/no_worry_return/get", []string{"access_token"}, true}
	epRetUnbind = Endpoint{"wexp.ret_unbind", http.MethodPost, "/cgi-bin/express/delivery/no_worry_return/unbind", []string{"access_token"}, true}
)

// insGet GET 双件辅助。
func (s *WxAppService) insGet(ctx context.Context, ep Endpoint) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, ep, map[string]string{"access_token": token}, nil)
}

// OpenInsuranceFreight 开通无忧退货（GET 无参）。
func (s *WxAppService) OpenInsuranceFreight(ctx context.Context) (map[string]any, error) {
	return s.insGet(ctx, epInsOpen)
}

// QueryInsuranceOpen 查询无忧退货开通状态（is_open 0/1）。
func (s *WxAppService) QueryInsuranceOpen(ctx context.Context) (map[string]any, error) {
	return s.insGet(ctx, epInsQueryOpen)
}

// CreateInsuranceOrder 发货时投保（order_no 仅可投保一次；pay_time 误差 3 天内；金额单位分；
// 返回 policy_no/insurance_end_date/预估理赔额/保费）。
func (s *WxAppService) CreateInsuranceOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epInsCreateOrder, body)
}

// ClaimInsurance 无忧退理赔（openid/order_no 须与投保一致；refund_delivery_no 理赔时唯一）。
func (s *WxAppService) ClaimInsurance(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epInsClaim, body)
}

// CreateChargeID 申请充值订单号（quota 单位分；返回 order_id）。
func (s *WxAppService) CreateChargeID(ctx context.Context, quota int) (map[string]any, error) {
	return s.wexpInvoke(ctx, epInsChargeID, map[string]any{"quota": quota})
}

// ApplyInsurancePay 申请支付（order_id 与充值单一致；JS 等语言建议传 string 防精度丢失；
// 充值后退款只能退回原充值账号）。
func (s *WxAppService) ApplyInsurancePay(ctx context.Context, orderID string) (map[string]any, error) {
	return s.wexpInvoke(ctx, epInsApplyPay, map[string]any{"order_id": orderID})
}

// GetInsurancePayOrders 拉取充值订单（status_list 1 待支付/2 支付成功/3 使用中/4 已用完/
// 5 退款中/6 已退款/10 支付超时）。
func (s *WxAppService) GetInsurancePayOrders(ctx context.Context, statusList []int, offset, limit int) (map[string]any, error) {
	body := map[string]any{"status_list": statusList}
	if offset > 0 {
		body["offset"] = offset
	}
	if limit > 0 {
		body["limit"] = limit
	}
	return s.wexpInvoke(ctx, epInsPayOrders, body)
}

// RefundInsurance 充值保费退款（无 body；只退回原充值账号）。
func (s *WxAppService) RefundInsurance(ctx context.Context) (map[string]any, error) {
	return s.wexpInvoke(ctx, epInsRefund, nil)
}

// GetInsuranceSummary 拉取理赔摘要（投保/理赔统计 + 当前保费与账号余额）。
func (s *WxAppService) GetInsuranceSummary(ctx context.Context, beginTime, endTime int64) (map[string]any, error) {
	return s.wexpInvoke(ctx, epInsSummary, map[string]any{"begin_time": beginTime, "end_time": endTime})
}

// GetInsuranceOrders 拉取保单信息（过滤族全选填：openid/order_no/policy_no/运单/时间/
// status_list 2 保障中-7 投保过期；limit ≤100）。
func (s *WxAppService) GetInsuranceOrders(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epInsOrders, body)
}

// SetInsuranceNotifyFunds 设置保费告警余额（notify_funds 单位分，0=不通知；余额不变 24h 一次）。
func (s *WxAppService) SetInsuranceNotifyFunds(ctx context.Context, notifyFunds int) (map[string]any, error) {
	return s.wexpInvoke(ctx, epInsNotifyFunds, map[string]any{"notify_funds": notifyFunds})
}

// AddReturnId 创建退货 ID（shop_order_id 与退货 ID 一一对应；wx_pay_id 须已投保；
// 创建后平台自动向用户下发模板消息提醒退货）。
func (s *WxAppService) AddReturnId(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epRetAdd, body)
}

// GetReturnId 查询退货 ID 状态（用户是否已填退货信息与退货物流）。
func (s *WxAppService) GetReturnId(ctx context.Context, returnID string) (map[string]any, error) {
	return s.wexpInvoke(ctx, epRetGet, map[string]any{"return_id": returnID})
}

// UnbindReturnId 解绑退货 ID（仅用户自主填写运单号时支持解绑）。
func (s *WxAppService) UnbindReturnId(ctx context.Context, returnID string) (map[string]any, error) {
	return s.wexpInvoke(ctx, epRetUnbind, map[string]any{"return_id": returnID})
}

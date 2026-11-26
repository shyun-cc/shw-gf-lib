package wxapp

import (
	"context"
	"net/http"
)

// 物流助手·运力方表项（配送公司侧调用；官方取证见 change wxapp-express-delivery proposal）
var (
	epExpProvBizUpdate = Endpoint{"exp.provider_business_update", http.MethodPost, "/cgi-bin/express/delivery/service/business/update", []string{"access_token"}, true}
	epExpProvPath      = Endpoint{"exp.provider_path_update", http.MethodPost, "/cgi-bin/express/delivery/path/update", []string{"access_token"}, true}
	epExpProvPreview   = Endpoint{"exp.template_preview", http.MethodPost, "/cgi-bin/express/delivery/template/preview", []string{"access_token"}, true}
	epExpProvContact   = Endpoint{"exp.contact_get", http.MethodPost, "/cgi-bin/express/delivery/contact/get", []string{"access_token"}, true}
	epExpScatterCancel = Endpoint{"exp.scatter_cancel", http.MethodPost, "/cgi-bin/express/delivery/single_waybill/cancel_order", []string{"access_token"}, true}
	epExpScatterFee    = Endpoint{"exp.scatter_fee", http.MethodPost, "/cgi-bin/express/delivery/single_waybill/fee", []string{"access_token"}, true}
	epExpScatterRefund = Endpoint{"exp.scatter_refund", http.MethodPost, "/cgi-bin/express/delivery/single_waybill/refund_order", []string{"access_token"}, true}
	epExpScatterBill   = Endpoint{"exp.scatter_bill", http.MethodPost, "/cgi-bin/express/delivery/single_waybill/get_bill", []string{"access_token"}, true}
	epExpScatterComp   = Endpoint{"exp.scatter_complaint", http.MethodPost, "/cgi-bin/express/delivery/scatter/update_complaint_result", []string{"access_token"}, true}
	epExpScatterStatus = Endpoint{"exp.scatter_status", http.MethodPost, "/cgi-bin/express/delivery/single_waybill/update", []string{"access_token"}, true}
)

// UpdateExpressBusiness 运力方更新商户审核结果（result_code 0 通过，非 0 须 result_msg）。
func (s *WxAppService) UpdateExpressBusiness(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpProvBizUpdate, map[string]string{"access_token": token}, body)
}

// UpdateExpressPath 运力方更新运单轨迹（token 取自商户下单事件推送；action_type 如
// 100001 揽件/200001 运输/300002 派送/300003 签收/400001 取消）。
func (s *WxAppService) UpdateExpressPath(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpProvPath, map[string]string{"access_token": token}, body)
}

// PreviewExpressTemplate 运力方预览面单模板（waybill_template 须 BASE64；custom 结构同
// AddExpressOrder 体；返回 rendered_waybill_template）。
func (s *WxAppService) PreviewExpressTemplate(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpProvPreview, map[string]string{"access_token": token}, body)
}

// GetExpressContact 运力方获取面单联系人信息（返回 sender/receiver，地址已合并省市区）。
func (s *WxAppService) GetExpressContact(ctx context.Context, token_, waybillID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpProvContact, map[string]string{"access_token": token}, map[string]string{"token": token_, "waybill_id": waybillID})
}

// ScatterCancelOrder 运力取消订单（token 取自下单事件推送）。
func (s *WxAppService) ScatterCancelOrder(ctx context.Context, token_, reason string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpScatterCancel, map[string]string{"access_token": token}, map[string]string{"token": token_, "reason": reason})
}

// ScatterUpdateOrderFee 运力更新需支付运费（金额单位分；need_pay 0 否/1 是/2 支付分；
// 订单须非待揽件状态——支付分流程先更新状态再传费）。
func (s *WxAppService) ScatterUpdateOrderFee(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpScatterFee, map[string]string{"access_token": token}, body)
}

// ScatterRefundOrder 运力公司退款（支持部分退款；金额单位分）。
func (s *WxAppService) ScatterRefundOrder(ctx context.Context, token_, fee string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpScatterRefund, map[string]string{"access_token": token}, map[string]string{"token": token_, "fee": fee})
}

// ScatterGetBill 对账单下载（date YYYYMMDD；type ALL/SUCCESS/REFUND；成功返回账单文件
// 内容、失败 JSON——错误路径走 BizError）。
func (s *WxAppService) ScatterGetBill(ctx context.Context, date, billType string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpScatterBill, map[string]string{"access_token": token}, map[string]string{"date": date, "type": billType})
}

// ScatterUpdateComplaintResult 运力返回投诉处理结果（官方 body 同时携带 access_token 字段
// ——照官方形态由调用方组 body 传入）。
func (s *WxAppService) ScatterUpdateComplaintResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpScatterComp, map[string]string{"access_token": token}, body)
}

// ScatterUpdateOrderStatus 运力更新订单状态（action 族 + 取件/派件员姓名电话四项必填）。
func (s *WxAppService) ScatterUpdateOrderStatus(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpScatterStatus, map[string]string{"access_token": token}, body)
}

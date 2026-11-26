package wxapp

import (
	"context"
	"net/http"
)

// 微信物流服务·查询组件 + 消息组件 + 运力消息推送表项（官方取证见 change
// wxapp-weixin-express proposal；两组 get_delivery_list 为官方重复列示的同一接口，不重复立）
var (
	epWexpTraceWaybill = Endpoint{"wexp.search_trace_waybill", http.MethodPost, "/cgi-bin/express/delivery/open_msg/trace_waybill", []string{"access_token"}, true}
	epWexpQueryTrace   = Endpoint{"wexp.search_query_trace", http.MethodPost, "/cgi-bin/express/delivery/open_msg/query_trace", []string{"access_token"}, true}
	epWexpUpdateGoods  = Endpoint{"wexp.search_update_goods", http.MethodPost, "/cgi-bin/express/delivery/open_msg/update_waybill_goods", []string{"access_token"}, true}
	epWexpDeliveryList = Endpoint{"wexp.search_delivery_list", http.MethodPost, "/cgi-bin/express/delivery/open_msg/get_delivery_list", []string{"access_token"}, false}

	epWexpFollowWaybill = Endpoint{"wexp.msg_follow_waybill", http.MethodPost, "/cgi-bin/express/delivery/open_msg/follow_waybill", []string{"access_token"}, true}
	epWexpQueryFollow   = Endpoint{"wexp.msg_query_follow_trace", http.MethodPost, "/cgi-bin/express/delivery/open_msg/query_follow_trace", []string{"access_token"}, true}
	epWexpUpdateFollow  = Endpoint{"wexp.msg_update_follow_goods", http.MethodPost, "/cgi-bin/express/delivery/open_msg/update_follow_waybill_goods", []string{"access_token"}, true}

	epWexpUserQuery  = Endpoint{"wexp.push_userquery", http.MethodPost, "/cgi-bin/express/delivery/userquery", []string{"access_token"}, true}
	epWexpPathNotify = Endpoint{"wexp.push_pathnotify", http.MethodPost, "/cgi-bin/express/delivery/pathnotify", []string{"access_token"}, true}
)

// wexpInvoke 微信物流 POST 分发（nil body 转接口 nil 防 "null"）。
func (s *WxAppService) wexpInvoke(ctx context.Context, ep Endpoint, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return s.invoke(ctx, ep, map[string]string{"access_token": token}, nil)
	}
	return s.invoke(ctx, ep, map[string]string{"access_token": token}, body)
}

// TraceWaybill 查询组件·传运单（openid+收件人手机+运单号+商品+微信支付单号+落地页路径；
// 返回 waybill_token 供查询）。
func (s *WxAppService) TraceWaybill(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epWexpTraceWaybill, body)
}

// QueryTrace 查询组件·查询运单（waybill_token 来自 TraceWaybill）。
func (s *WxAppService) QueryTrace(ctx context.Context, waybillToken, openid string) (map[string]any, error) {
	body := map[string]any{"waybill_token": waybillToken}
	if openid != "" {
		body["openid"] = openid
	}
	return s.wexpInvoke(ctx, epWexpQueryTrace, body)
}

// UpdateWaybillGoods 查询组件·更新物流信息（goods_name ≤60 字符）。
func (s *WxAppService) UpdateWaybillGoods(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epWexpUpdateGoods, body)
}

// GetOpenMsgDeliveryList 获取运力 id 列表（查询/消息两组共用同一官方接口）。
func (s *WxAppService) GetOpenMsgDeliveryList(ctx context.Context) (map[string]any, error) {
	return s.wexpInvoke(ctx, epWexpDeliveryList, nil)
}

// FollowWaybill 消息组件·传运单（微信跟踪关键物流节点向用户推送消息）。
func (s *WxAppService) FollowWaybill(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epWexpFollowWaybill, body)
}

// QueryFollowTrace 消息组件·查运单。
func (s *WxAppService) QueryFollowTrace(ctx context.Context, waybillToken, openid string) (map[string]any, error) {
	body := map[string]any{"waybill_token": waybillToken}
	if openid != "" {
		body["openid"] = openid
	}
	return s.wexpInvoke(ctx, epWexpQueryFollow, body)
}

// UpdateFollowWaybillGoods 消息组件·更新物品信息（修改次数有上限，超限 9300560）。
func (s *WxAppService) UpdateFollowWaybillGoods(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epWexpUpdateFollow, body)
}

// DeliveryUserQuery 运力方·用户手机状态查询（exist 0 未绑定/1 已绑定）。
func (s *WxAppService) DeliveryUserQuery(ctx context.Context, phone string) (map[string]any, error) {
	return s.wexpInvoke(ctx, epWexpUserQuery, map[string]any{"phone": phone})
}

// DeliveryPathNotify 运力方·推送已绑定物流轨迹信息（path 内 action 族 + 取派件员选填）。
func (s *WxAppService) DeliveryPathNotify(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.wexpInvoke(ctx, epWexpPathNotify, body)
}

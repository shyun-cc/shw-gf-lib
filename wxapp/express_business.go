package wxapp

import (
	"context"
	"net/http"
)

// 物流助手·小程序使用表项（官方取证见 change wxapp-express-delivery proposal）
var (
	epExpAccountBind = Endpoint{"exp.account_bind", http.MethodPost, "/cgi-bin/express/business/account/bind", []string{"access_token"}, true}
	epExpAccountGet  = Endpoint{"exp.account_getall", http.MethodGet, "/cgi-bin/express/business/account/getall", []string{"access_token"}, false}
	epExpDeliveryGet = Endpoint{"exp.delivery_getall", http.MethodGet, "/cgi-bin/express/business/delivery/getall", []string{"access_token"}, false}
	epExpOrderAdd    = Endpoint{"exp.order_add", http.MethodPost, "/cgi-bin/express/business/order/add", []string{"access_token"}, true}
	epExpOrderCancel = Endpoint{"exp.order_cancel", http.MethodPost, "/cgi-bin/express/business/order/cancel", []string{"access_token"}, true}
	epExpOrderGet    = Endpoint{"exp.order_get", http.MethodPost, "/cgi-bin/express/business/order/get", []string{"access_token"}, true}
	epExpOrderBatch  = Endpoint{"exp.order_batchget", http.MethodPost, "/cgi-bin/express/business/order/batchget", []string{"access_token"}, true}
	epExpPathGet     = Endpoint{"exp.path_get", http.MethodPost, "/cgi-bin/express/business/path/get", []string{"access_token"}, true}
	epExpPrinterUpd  = Endpoint{"exp.printer_update", http.MethodPost, "/cgi-bin/express/business/printer/update", []string{"access_token"}, true}
	epExpPrinterGet  = Endpoint{"exp.printer_getall", http.MethodGet, "/cgi-bin/express/business/printer/getall", []string{"access_token"}, false}
	epExpQuotaGet    = Endpoint{"exp.quota_get", http.MethodPost, "/cgi-bin/express/business/quota/get", []string{"access_token"}, true}
	epExpTestUpdate  = Endpoint{"exp.test_update_order", http.MethodPost, "/cgi-bin/express/business/test_update_order", []string{"access_token"}, true}
)

// BindExpressAccount 绑定/解绑物流账号（type bind/unbind；EMS 审核须 remark_content）。
func (s *WxAppService) BindExpressAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpAccountBind, map[string]string{"access_token": token}, body)
}

// GetAllExpressAccounts 获取所有绑定的物流账号（GET 无参）。
func (s *WxAppService) GetAllExpressAccounts(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpAccountGet, map[string]string{"access_token": token}, nil)
}

// GetAllDeliveries 获取支持的快递公司列表（GET 无参）。
func (s *WxAppService) GetAllDeliveries(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpDeliveryGet, map[string]string{"access_token": token}, nil)
}

// AddExpressOrder 生成运单（order_id ≤512 字节全局唯一；八对象族 sender/receiver/cargo/
// shop/insured/service；顺丰散单 expect_time 必传；返回 waybill_id 与面单数据）。
func (s *WxAppService) AddExpressOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpOrderAdd, map[string]string{"access_token": token}, body)
}

// CancelExpressOrder 取消运单（返回运力侧 delivery_resultcode 族）。
func (s *WxAppService) CancelExpressOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpOrderCancel, map[string]string{"access_token": token}, body)
}

// GetExpressOrder 获取运单数据（print_html 为面单 HTML 的 BASE64；order_status 0 正常/1 取消）。
func (s *WxAppService) GetExpressOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpOrderGet, map[string]string{"access_token": token}, body)
}

// BatchGetExpressOrders 批量获取运单数据（order_list ≤100；openid/print_type 不生效）。
func (s *WxAppService) BatchGetExpressOrders(ctx context.Context, orderList []map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpOrderBatch, map[string]string{"access_token": token}, map[string]any{"order_list": orderList})
}

// GetExpressPath 查询运单轨迹。
func (s *WxAppService) GetExpressPath(ctx context.Context, deliveryID, waybillID, openid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]string{"delivery_id": deliveryID, "waybill_id": waybillID}
	if openid != "" {
		body["openid"] = openid
	}
	return s.invoke(ctx, epExpPathGet, map[string]string{"access_token": token}, body)
}

// UpdateExpressPrinter 配置面单打印员（update_type bind/unbind；tagid_list ≤10 平台型小程序用）。
func (s *WxAppService) UpdateExpressPrinter(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpPrinterUpd, map[string]string{"access_token": token}, body)
}

// GetExpressPrinters 获取打印员列表（GET 无参；微信打单 PC 软件场景）。
func (s *WxAppService) GetExpressPrinters(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpPrinterGet, map[string]string{"access_token": token}, nil)
}

// GetExpressQuota 获取电子面单余额（仅加盟类快递公司）。
func (s *WxAppService) GetExpressQuota(ctx context.Context, deliveryID, bizID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpQuotaGet, map[string]string{"access_token": token}, map[string]string{"delivery_id": deliveryID, "biz_id": bizID})
}

// TestUpdateExpressOrder 模拟更新订单状态（测试专用：biz_id=test_biz_id、delivery_id=TEST；
// action_type 如 100001 揽件/300003 签收/400001 取消）。
func (s *WxAppService) TestUpdateExpressOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epExpTestUpdate, map[string]string{"access_token": token}, body)
}

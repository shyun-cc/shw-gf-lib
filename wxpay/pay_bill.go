package wxpay

import (
	"context"
	"io"
	"net/http"
	"net/url"
)

// V3 账单族与分账族表项（官方清点见 change wxpay-v3-bill-profitsharing proposal）
var (
	epTradeBill             = Endpoint{"v3.bill.tradebill", http.MethodGet, "/v3/bill/tradebill", []string{"bill_date", "bill_type", "tar_type"}, false}
	epProfitsharingBill     = Endpoint{"v3.bill.profitsharing", http.MethodGet, "/v3/profitsharing/bills", []string{"bill_date", "tar_type"}, false}
	epReceiverAdd           = Endpoint{"v3.profitsharing.receivers_add", http.MethodPost, "/v3/profitsharing/receivers/add", nil, true}
	epProfitsharingNew      = Endpoint{"v3.profitsharing.orders_create", http.MethodPost, "/v3/profitsharing/orders", nil, true}
	epProfitsharingGet      = Endpoint{"v3.profitsharing.orders_query", http.MethodGet, "/v3/profitsharing/orders/{out_order_no}", []string{"transaction_id"}, false}
	epProfitsharingUnfreeze = Endpoint{"v3.profitsharing.orders_unfreeze", http.MethodPost, "/v3/profitsharing/orders/unfreeze", nil, true}
)

// ApplyTradeBill 申请交易账单（query：bill_date 必填/bill_type/tar_type；每日 10 点后生成昨日账单）
func (c *Client) ApplyTradeBill(ctx context.Context, billDate, billType, tarType string) (map[string]any, error) {
	query := map[string]string{"bill_date": billDate}
	if billType != "" {
		query["bill_type"] = billType
	}
	if tarType != "" {
		query["tar_type"] = tarType
	}
	return c.invoke(ctx, epTradeBill, nil, query, nil)
}

// ApplyProfitsharingBill 申请分账账单（query：bill_date 必填/tar_type）
func (c *Client) ApplyProfitsharingBill(ctx context.Context, billDate, tarType string) (map[string]any, error) {
	query := map[string]string{"bill_date": billDate}
	if tarType != "" {
		query["tar_type"] = tarType
	}
	return c.invoke(ctx, epProfitsharingBill, nil, query, nil)
}

// DownloadBill 下载账单：对申请接口返回的 download_url（30 秒有效）按 APIv3 标准签名发起请求，
// 返回账单文件字节。官方明确该响应不含签名值——跳过应答验签。
func (c *Client) DownloadBill(ctx context.Context, downloadURL string) ([]byte, error) {
	u, err := url.Parse(downloadURL)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	pathWithQuery := u.RequestURI()
	auth, err := c.AuthorizationHeader(http.MethodGet, pathWithQuery, "")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	req.Header.Set("Authorization", auth)
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &InfraError{Op: "http", Err: err}
	}
	if resp.StatusCode >= 400 {
		return nil, &BizError{Code: "DOWNLOAD_FAILED", Message: string(data), HTTPStatus: resp.StatusCode}
	}
	return data, nil
}

// AddProfitsharingReceiver 添加分账接收方（body：appid/type/account/relation_type；上限 2 万）
func (c *Client) AddProfitsharingReceiver(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epReceiverAdd, nil, nil, body)
}

// CreateProfitsharingOrder 请求分账（body：transaction_id/out_order_no/receivers/unfreeze_unsplit；
// unfreeze_unsplit=true 解冻后不可再次分账）
func (c *Client) CreateProfitsharingOrder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epProfitsharingNew, nil, nil, body)
}

// QueryProfitsharingOrder 查询分账结果（query：transaction_id）
func (c *Client) QueryProfitsharingOrder(ctx context.Context, outOrderNo, transactionID string) (map[string]any, error) {
	return c.invoke(ctx, epProfitsharingGet, map[string]string{"out_order_no": outOrderNo},
		map[string]string{"transaction_id": transactionID}, nil)
}

// UnfreezeProfitsharingRemainder 解冻剩余资金（body：transaction_id/out_order_no/description；异步处理）
func (c *Client) UnfreezeProfitsharingRemainder(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epProfitsharingUnfreeze, nil, nil, body)
}

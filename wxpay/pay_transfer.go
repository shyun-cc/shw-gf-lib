package wxpay

import (
	"context"
	"net/http"
)

// V3 商家转账族表项（官方清点见 change wxpay-v3-transfer proposal）
var (
	epTransferCreate   = Endpoint{"v3.transfer.create", http.MethodPost, "/v3/fund-app/mch-transfer/transfer-bills", nil, true}
	epTransferPreAuth  = Endpoint{"v3.transfer.pre_transfer_with_authorization", http.MethodPost, "/v3/fund-app/mch-transfer/transfer-bills/pre-transfer-with-authorization", nil, true}
	epTransferAuthed   = Endpoint{"v3.transfer.transfer_with_authorization", http.MethodPost, "/v3/fund-app/mch-transfer/transfer-bills/transfer", nil, true}
	epTransferQueryNo  = Endpoint{"v3.transfer.query_out_bill_no", http.MethodGet, "/v3/fund-app/mch-transfer/transfer-bills/out-bill-no/{out_bill_no}", nil, false}
	epTransferQueryWx  = Endpoint{"v3.transfer.query_transfer_bill_no", http.MethodGet, "/v3/fund-app/mch-transfer/transfer-bills/transfer-bill-no/{transfer_bill_no}", nil, false}
	epTransferElecSign = Endpoint{"v3.transfer.elecsign", http.MethodGet, "/v3/fund-app/mch-transfer/elecsign/out-bill-no/{out_bill_no}", nil, false}
	epTransferReceipt  = Endpoint{"v3.transfer.bill_receipt", http.MethodPost, "/v3/transfer/bill-receipt", nil, true}
)

// CreateTransferBill 发起转账（用户确认模式；body：appid/out_bill_no/transfer_scene_id/openid/
// transfer_amount/transfer_remark/transfer_scene_report_infos）
func (c *Client) CreateTransferBill(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epTransferCreate, nil, nil, body)
}

// PreTransferWithAuthorization 发起转账并完成免确认收款授权（≥2000 元须 user_name）
func (c *Client) PreTransferWithAuthorization(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epTransferPreAuth, nil, nil, body)
}

// TransferWithAuthorization 用户授权后转账（authorization_id/out_authorization_no 二选一）
func (c *Client) TransferWithAuthorization(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epTransferAuthed, nil, nil, body)
}

// QueryTransferByOutBillNo 商户单号查询转账单
func (c *Client) QueryTransferByOutBillNo(ctx context.Context, outBillNo string) (map[string]any, error) {
	return c.invoke(ctx, epTransferQueryNo, map[string]string{"out_bill_no": outBillNo}, nil, nil)
}

// QueryTransferByTransferBillNo 微信单号查询转账单（仅近 30 天，更久走资金账单）
func (c *Client) QueryTransferByTransferBillNo(ctx context.Context, transferBillNo string) (map[string]any, error) {
	return c.invoke(ctx, epTransferQueryWx, map[string]string{"transfer_bill_no": transferBillNo}, nil, nil)
}

// QueryTransferElecSign 商户单号查询电子回单（下载地址 10 分钟有效，需校验文件摘要）
func (c *Client) QueryTransferElecSign(ctx context.Context, outBillNo string) (map[string]any, error) {
	return c.invoke(ctx, epTransferElecSign, map[string]string{"out_bill_no": outBillNo}, nil, nil)
}

// ApplyTransferBillReceipt 转账账单电子回单申请受理（body：out_batch_no；旧版批次族，发起时须传收款人姓名）
func (c *Client) ApplyTransferBillReceipt(ctx context.Context, outBatchNo string) (map[string]any, error) {
	return c.invoke(ctx, epTransferReceipt, nil, nil, map[string]any{"out_batch_no": outBatchNo})
}

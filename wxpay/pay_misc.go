package wxpay

import (
	"context"
	"net/http"
	"strconv"
)

// V3 资金账单 + 消费者投诉 2.0 核心五件表项（官方清点见 change wxpay-v3-misc proposal）
var (
	epFundFlowBill   = Endpoint{"v3.bill.fundflowbill", http.MethodGet, "/v3/bill/fundflowbill", []string{"bill_date", "account_type", "tar_type"}, false}
	epComplaintList  = Endpoint{"v3.complaint.list", http.MethodGet, "/v3/merchant-service/complaints-v2", []string{"limit", "offset", "begin_date", "end_date", "complainted_mchid"}, false}
	epComplaintGet   = Endpoint{"v3.complaint.get", http.MethodGet, "/v3/merchant-service/complaints-v2/{complaint_id}", nil, false}
	epComplaintHist  = Endpoint{"v3.complaint.negotiation_historys", http.MethodGet, "/v3/merchant-service/complaints-v2/{complaint_id}/negotiation-historys", []string{"limit", "offset"}, false}
	epComplaintReply = Endpoint{"v3.complaint.response", http.MethodPost, "/v3/merchant-service/complaints-v2/{complaint_id}/response", nil, true}
	epComplaintNotif = Endpoint{"v3.complaint.notification_url", http.MethodGet, "/v3/merchant-service/complaint-notifications", nil, false}
)

// ApplyFundFlowBill 申请资金账单（资金实际到账流水，区别于交易账单；下载走 DownloadBill）
func (c *Client) ApplyFundFlowBill(ctx context.Context, billDate, accountType, tarType string) (map[string]any, error) {
	query := map[string]string{"bill_date": billDate}
	if accountType != "" {
		query["account_type"] = accountType
	}
	if tarType != "" {
		query["tar_type"] = tarType
	}
	return c.invoke(ctx, epFundFlowBill, nil, query, nil)
}

// ListComplaints 查询投诉单列表（begin_date/end_date 必填）
func (c *Client) ListComplaints(ctx context.Context, query map[string]string) (map[string]any, error) {
	return c.invoke(ctx, epComplaintList, nil, query, nil)
}

// GetComplaint 查询投诉单详情
func (c *Client) GetComplaint(ctx context.Context, complaintID string) (map[string]any, error) {
	return c.invoke(ctx, epComplaintGet, map[string]string{"complaint_id": complaintID}, nil, nil)
}

// ListComplaintNegotiationHistorys 查询投诉单协商历史（limit 1-300）
func (c *Client) ListComplaintNegotiationHistorys(ctx context.Context, complaintID string, limit, offset int) (map[string]any, error) {
	query := map[string]string{}
	if limit > 0 {
		query["limit"] = strconv.Itoa(limit)
	}
	if offset > 0 {
		query["offset"] = strconv.Itoa(offset)
	}
	return c.invoke(ctx, epComplaintHist, map[string]string{"complaint_id": complaintID}, query, nil)
}

// ReplyComplaint 回复用户（首次回复后投诉单脱离"待处理"；body：complainted_mchid/response_content/…）
func (c *Client) ReplyComplaint(ctx context.Context, complaintID string, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epComplaintReply, map[string]string{"complaint_id": complaintID}, nil, body)
}

// GetComplaintNotificationURL 查询投诉通知回调地址
func (c *Client) GetComplaintNotificationURL(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epComplaintNotif, nil, nil, nil)
}

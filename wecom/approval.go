package wecom

import (
	"context"
	"net/http"
)

// 审批域表项（OA 族）：模板 3 + 申请/单号/详情 3 + 旧数据 1 + 假期 3 + 自建审批 1
// （官方清点见 change wecom-approval-domain proposal）
var (
	epApprovalTemplateGet = Endpoint{"approval.template_detail", http.MethodPost, "/cgi-bin/oa/gettemplatedetail", nil, true}
	epApprovalApply       = Endpoint{"approval.applyevent", http.MethodPost, "/cgi-bin/oa/applyevent", nil, true}
	epApprovalInfo        = Endpoint{"approval.info", http.MethodPost, "/cgi-bin/oa/getapprovalinfo", nil, true}
	epApprovalDetail      = Endpoint{"approval.detail", http.MethodPost, "/cgi-bin/oa/getapprovaldetail", nil, true}
	epApprovalOldData     = Endpoint{"approval.corp_old_data", http.MethodPost, "/cgi-bin/corp/getapprovaldata", nil, true}
	epVacationCorpConf    = Endpoint{"approval.vacation_corp_conf", http.MethodGet, "/cgi-bin/oa/vacation/getcorpconf", nil, false}
	epVacationQuotaGet    = Endpoint{"approval.vacation_quota", http.MethodPost, "/cgi-bin/oa/vacation/getuservacationquota", nil, true}
	epVacationQuotaSet    = Endpoint{"approval.vacation_quota_set", http.MethodPost, "/cgi-bin/oa/vacation/setoneuserquota", nil, true}
	epOpenApprovalData    = Endpoint{"approval.open_data", http.MethodPost, "/cgi-bin/corp/getopenapprovaldata", nil, true}
	epApprovalTplCreate   = Endpoint{"approval.template_create", http.MethodPost, "/cgi-bin/oa/approval/create_template", nil, true}
	epApprovalTplUpdate   = Endpoint{"approval.template_update", http.MethodPost, "/cgi-bin/oa/approval/update_template", nil, true}
)

// GetApprovalTemplateDetail 获取审批模板详情
func (c *Client) GetApprovalTemplateDetail(ctx context.Context, templateID string) (map[string]any, error) {
	return c.invoke(ctx, epApprovalTemplateGet, nil, map[string]any{"template_id": templateID})
}

// ApplyApprovalEvent 提交审批申请（body：creator_userid/template_id/use_template_approver/process/apply_data/summary_list）
func (c *Client) ApplyApprovalEvent(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epApprovalApply, nil, body)
}

// GetApprovalInfo 批量获取审批单号（body：starttime/endtime/new_cursor/size/filters）
func (c *Client) GetApprovalInfo(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epApprovalInfo, nil, body)
}

// GetApprovalDetail 获取审批申请详情（sp_no 审批单号）
func (c *Client) GetApprovalDetail(ctx context.Context, spNo string) (map[string]any, error) {
	return c.invoke(ctx, epApprovalDetail, nil, map[string]any{"sp_no": spNo})
}

// GetCorpApprovalData 获取审批数据（旧接口，不推荐——建议 getapprovalinfo + getapprovaldetail）
func (c *Client) GetCorpApprovalData(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epApprovalOldData, nil, body)
}

// GetVacationCorpConf 获取企业假期管理配置
func (c *Client) GetVacationCorpConf(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epVacationCorpConf, nil, nil)
}

// GetUserVacationQuota 获取成员假期余额
func (c *Client) GetUserVacationQuota(ctx context.Context, userid string) (map[string]any, error) {
	return c.invoke(ctx, epVacationQuotaGet, nil, map[string]any{"userid": userid})
}

// SetOneUserVacationQuota 修改成员假期余额（body：userid/vacation_id/leftduration/time_attr/remarks）
func (c *Client) SetOneUserVacationQuota(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epVacationQuotaSet, nil, body)
}

// GetOpenApprovalData 查询自建应用审批单当前状态（thirdNo 发起时自定义单号）
func (c *Client) GetOpenApprovalData(ctx context.Context, thirdNo string) (map[string]any, error) {
	return c.invoke(ctx, epOpenApprovalData, nil, map[string]any{"thirdNo": thirdNo})
}

// CreateApprovalTemplate 创建审批模板（body：template_name/template_content）
func (c *Client) CreateApprovalTemplate(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epApprovalTplCreate, nil, body)
}

// UpdateApprovalTemplate 更新审批模板（body：template_id/template_name/template_content）
func (c *Client) UpdateApprovalTemplate(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epApprovalTplUpdate, nil, body)
}

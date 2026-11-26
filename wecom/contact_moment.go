package wecom

import (
	"context"
	"net/http"
)

// 客户联系子域④表项：客户朋友圈 14（含 moment_strategy 6）+ 获客助手 9
// （官方清点见 change wecom-contact-moment-acquisition proposal）
var (
	epMomentAddTask       = Endpoint{"ec.moment.add_task", http.MethodPost, "/cgi-bin/externalcontact/add_moment_task", nil, true}
	epMomentTaskResult    = Endpoint{"ec.moment.task_result", http.MethodGet, "/cgi-bin/externalcontact/get_moment_task_result", []string{"jobid"}, false}
	epMomentCancel        = Endpoint{"ec.moment.cancel", http.MethodPost, "/cgi-bin/externalcontact/cancel_moment_task", nil, true}
	epMomentList          = Endpoint{"ec.moment.list", http.MethodPost, "/cgi-bin/externalcontact/get_moment_list", nil, true}
	epMomentTask          = Endpoint{"ec.moment.task", http.MethodPost, "/cgi-bin/externalcontact/get_moment_task", nil, true}
	epMomentCustomerList  = Endpoint{"ec.moment.customer_list", http.MethodPost, "/cgi-bin/externalcontact/get_moment_customer_list", nil, true}
	epMomentSendResult    = Endpoint{"ec.moment.send_result", http.MethodPost, "/cgi-bin/externalcontact/get_moment_send_result", nil, true}
	epMomentComments      = Endpoint{"ec.moment.comments", http.MethodPost, "/cgi-bin/externalcontact/get_moment_comments", nil, true}
	epMomentStrategyList  = Endpoint{"ec.moment_strategy.list", http.MethodPost, "/cgi-bin/externalcontact/moment_strategy/list", nil, true}
	epMomentStrategyGet   = Endpoint{"ec.moment_strategy.get", http.MethodPost, "/cgi-bin/externalcontact/moment_strategy/get", nil, true}
	epMomentStrategyRange = Endpoint{"ec.moment_strategy.get_range", http.MethodPost, "/cgi-bin/externalcontact/moment_strategy/get_range", nil, true}
	epMomentStrategyNew   = Endpoint{"ec.moment_strategy.create", http.MethodPost, "/cgi-bin/externalcontact/moment_strategy/create", nil, true}
	epMomentStrategyEdit  = Endpoint{"ec.moment_strategy.edit", http.MethodPost, "/cgi-bin/externalcontact/moment_strategy/edit", nil, true}
	epMomentStrategyDel   = Endpoint{"ec.moment_strategy.del", http.MethodPost, "/cgi-bin/externalcontact/moment_strategy/del", nil, true}
	epAcqListLink         = Endpoint{"ec.acquisition.list_link", http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/list_link", nil, true}
	epAcqGet              = Endpoint{"ec.acquisition.get", http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/get", nil, true}
	epAcqCreateLink       = Endpoint{"ec.acquisition.create_link", http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/create_link", nil, true}
	epAcqUpdateLink       = Endpoint{"ec.acquisition.update_link", http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/update_link", nil, true}
	epAcqDeleteLink       = Endpoint{"ec.acquisition.delete_link", http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/delete_link", nil, true}
	epAcqCustomer         = Endpoint{"ec.acquisition.customer", http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/customer", nil, true}
	epAcqQuota            = Endpoint{"ec.acquisition.quota", http.MethodGet, "/cgi-bin/externalcontact/customer_acquisition_quota", nil, false}
	epAcqStatistic        = Endpoint{"ec.acquisition.statistic", http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/statistic", nil, true}
	epAcqChatInfo         = Endpoint{"ec.acquisition.chat_info", http.MethodPost, "/cgi-bin/externalcontact/customer_acquisition/get_chat_info", nil, true}
)

// AddMomentTask 创建朋友圈发表任务（body：text/attachments/visible_range）
func (c *Client) AddMomentTask(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentAddTask, nil, body)
}

// GetMomentTaskResult 获取任务创建结果（异步 jobid）
func (c *Client) GetMomentTaskResult(ctx context.Context, jobid string) (map[string]any, error) {
	return c.invoke(ctx, epMomentTaskResult, map[string]string{"jobid": jobid}, nil)
}

// CancelMomentTask 停止发表企业朋友圈
func (c *Client) CancelMomentTask(ctx context.Context, momentID string) (map[string]any, error) {
	return c.invoke(ctx, epMomentCancel, nil, map[string]any{"moment_id": momentID})
}

// GetMomentList 获取企业全部的发表列表（body：start_time/end_time/creator/filter_type/cursor/limit）
func (c *Client) GetMomentList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentList, nil, body)
}

// GetMomentTask 获取客户朋友圈企业发表的列表（body：moment_id/cursor/limit）
func (c *Client) GetMomentTask(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentTask, nil, body)
}

// GetMomentCustomerList 获取发表时选择的可见范围（body：moment_id/userid/cursor/limit）
func (c *Client) GetMomentCustomerList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentCustomerList, nil, body)
}

// GetMomentSendResult 获取发表后的可见客户列表（body：moment_id/userid/cursor/limit）
func (c *Client) GetMomentSendResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentSendResult, nil, body)
}

// GetMomentComments 获取互动数据（body：moment_id/userid）
func (c *Client) GetMomentComments(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentComments, nil, body)
}

// ListMomentStrategy 获取朋友圈规则组列表（body：cursor/limit）
func (c *Client) ListMomentStrategy(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentStrategyList, nil, body)
}

// GetMomentStrategy 获取朋友圈规则组详情
func (c *Client) GetMomentStrategy(ctx context.Context, strategyID int) (map[string]any, error) {
	return c.invoke(ctx, epMomentStrategyGet, nil, map[string]any{"strategy_id": strategyID})
}

// GetMomentStrategyRange 获取朋友圈规则组管理范围（body：strategy_id/cursor/limit）
func (c *Client) GetMomentStrategyRange(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentStrategyRange, nil, body)
}

// CreateMomentStrategy 创建朋友圈规则组（body：parent_id/strategy_name/admin_list/privilege/range）
func (c *Client) CreateMomentStrategy(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentStrategyNew, nil, body)
}

// EditMomentStrategy 编辑朋友圈规则组
func (c *Client) EditMomentStrategy(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMomentStrategyEdit, nil, body)
}

// DeleteMomentStrategy 删除朋友圈规则组
func (c *Client) DeleteMomentStrategy(ctx context.Context, strategyID int) (map[string]any, error) {
	return c.invoke(ctx, epMomentStrategyDel, nil, map[string]any{"strategy_id": strategyID})
}

// ListAcquisitionLink 获取获客链接列表（body：limit/cursor）
func (c *Client) ListAcquisitionLink(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAcqListLink, nil, body)
}

// GetAcquisitionLink 获取获客链接详情
func (c *Client) GetAcquisitionLink(ctx context.Context, linkID string) (map[string]any, error) {
	return c.invoke(ctx, epAcqGet, nil, map[string]any{"link_id": linkID})
}

// CreateAcquisitionLink 创建获客链接（body：link_name/range/skip_verify/priority_option/mark_source）
func (c *Client) CreateAcquisitionLink(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAcqCreateLink, nil, body)
}

// UpdateAcquisitionLink 编辑获客链接（body：link_id + 可更新字段）
func (c *Client) UpdateAcquisitionLink(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAcqUpdateLink, nil, body)
}

// DeleteAcquisitionLink 删除获客链接
func (c *Client) DeleteAcquisitionLink(ctx context.Context, linkID string) (map[string]any, error) {
	return c.invoke(ctx, epAcqDeleteLink, nil, map[string]any{"link_id": linkID})
}

// GetAcquisitionCustomer 获取由获客链接添加的客户列表（body：link_id/limit/cursor）
func (c *Client) GetAcquisitionCustomer(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAcqCustomer, nil, body)
}

// GetAcquisitionQuota 查询获客助手剩余使用量
func (c *Client) GetAcquisitionQuota(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epAcqQuota, nil, nil)
}

// GetAcquisitionStatistic 查询链接使用详情（body：link_id/start_time/end_time）
func (c *Client) GetAcquisitionStatistic(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAcqStatistic, nil, body)
}

// GetAcquisitionChatInfo 获取成员多次收消息详情（chat_key 来自 message_from_customer 事件，30 分钟内有效）
func (c *Client) GetAcquisitionChatInfo(ctx context.Context, chatKey string) (map[string]any, error) {
	return c.invoke(ctx, epAcqChatInfo, nil, map[string]any{"chat_key": chatKey})
}

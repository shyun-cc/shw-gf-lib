package wecom

import (
	"context"
	"net/http"
)

// 客户联系子域①表项：客户管理 5 + 规则组 6 + 企业标签 4 + 规则组标签 4 + 编辑客户标签 1
// （官方清点见 change wecom-contact-customer-tags proposal）
var (
	epECFollowUserList  = Endpoint{"ec.follow_user_list", http.MethodGet, "/cgi-bin/externalcontact/get_follow_user_list", nil, false}
	epECList            = Endpoint{"ec.list", http.MethodGet, "/cgi-bin/externalcontact/list", []string{"userid"}, false}
	epECGet             = Endpoint{"ec.get", http.MethodGet, "/cgi-bin/externalcontact/get", []string{"external_userid", "cursor"}, false}
	epECBatchGet        = Endpoint{"ec.batch_get", http.MethodPost, "/cgi-bin/externalcontact/batch/get_by_user", nil, true}
	epECRemark          = Endpoint{"ec.remark", http.MethodPost, "/cgi-bin/externalcontact/remark", nil, true}
	epECStrategyList    = Endpoint{"ec.strategy.list", http.MethodPost, "/cgi-bin/externalcontact/customer_strategy/list", nil, true}
	epECStrategyGet     = Endpoint{"ec.strategy.get", http.MethodPost, "/cgi-bin/externalcontact/customer_strategy/get", nil, true}
	epECStrategyRange   = Endpoint{"ec.strategy.get_range", http.MethodPost, "/cgi-bin/externalcontact/customer_strategy/get_range", nil, true}
	epECStrategyCreate  = Endpoint{"ec.strategy.create", http.MethodPost, "/cgi-bin/externalcontact/customer_strategy/create", nil, true}
	epECStrategyEdit    = Endpoint{"ec.strategy.edit", http.MethodPost, "/cgi-bin/externalcontact/customer_strategy/edit", nil, true}
	epECStrategyDel     = Endpoint{"ec.strategy.del", http.MethodPost, "/cgi-bin/externalcontact/customer_strategy/del", nil, true}
	epECCorpTagList     = Endpoint{"ec.corp_tag.list", http.MethodPost, "/cgi-bin/externalcontact/get_corp_tag_list", nil, true}
	epECCorpTagAdd      = Endpoint{"ec.corp_tag.add", http.MethodPost, "/cgi-bin/externalcontact/add_corp_tag", nil, true}
	epECCorpTagEdit     = Endpoint{"ec.corp_tag.edit", http.MethodPost, "/cgi-bin/externalcontact/edit_corp_tag", nil, true}
	epECCorpTagDel      = Endpoint{"ec.corp_tag.del", http.MethodPost, "/cgi-bin/externalcontact/del_corp_tag", nil, true}
	epECStrategyTagList = Endpoint{"ec.strategy_tag.list", http.MethodPost, "/cgi-bin/externalcontact/get_strategy_tag_list", nil, true}
	epECStrategyTagAdd  = Endpoint{"ec.strategy_tag.add", http.MethodPost, "/cgi-bin/externalcontact/add_strategy_tag", nil, true}
	epECStrategyTagEdit = Endpoint{"ec.strategy_tag.edit", http.MethodPost, "/cgi-bin/externalcontact/edit_strategy_tag", nil, true}
	epECStrategyTagDel  = Endpoint{"ec.strategy_tag.del", http.MethodPost, "/cgi-bin/externalcontact/del_strategy_tag", nil, true}
	epECMarkTag         = Endpoint{"ec.mark_tag", http.MethodPost, "/cgi-bin/externalcontact/mark_tag", nil, true}
)

// GetFollowUserList 获取配置了客户联系功能的成员列表
func (c *Client) GetFollowUserList(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epECFollowUserList, nil, nil)
}

// ListExternalContacts 获取客户列表（成员的 external_userid 列表）
func (c *Client) ListExternalContacts(ctx context.Context, userid string) (map[string]any, error) {
	return c.invoke(ctx, epECList, map[string]string{"userid": userid}, nil)
}

// GetExternalContact 获取客户详情；cursor 分页续传（空串首页）
func (c *Client) GetExternalContact(ctx context.Context, externalUserid, cursor string) (map[string]any, error) {
	query := map[string]string{"external_userid": externalUserid}
	if cursor != "" {
		query["cursor"] = cursor
	}
	return c.invoke(ctx, epECGet, query, nil)
}

// BatchGetExternalContactsByUser 批量获取客户详情（body：userid_list/cursor/limit）
func (c *Client) BatchGetExternalContactsByUser(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECBatchGet, nil, body)
}

// UpdateExternalContactRemark 修改客户备注信息（body：userid/external_userid/remark/description/…）
func (c *Client) UpdateExternalContactRemark(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECRemark, nil, body)
}

// ListCustomerStrategy 获取规则组列表（body：cursor/limit）
func (c *Client) ListCustomerStrategy(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyList, nil, body)
}

// GetCustomerStrategy 获取规则组详情
func (c *Client) GetCustomerStrategy(ctx context.Context, strategyID int) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyGet, nil, map[string]any{"strategy_id": strategyID})
}

// GetCustomerStrategyRange 获取规则组管理范围（body：strategy_id/cursor/limit）
func (c *Client) GetCustomerStrategyRange(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyRange, nil, body)
}

// CreateCustomerStrategy 创建规则组（body：parent_id/strategy_name/admin_list/privilege/range；仅支持串行调用）
func (c *Client) CreateCustomerStrategy(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyCreate, nil, body)
}

// EditCustomerStrategy 编辑规则组（body：strategy_id/…/range_add/range_del）
func (c *Client) EditCustomerStrategy(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyEdit, nil, body)
}

// DeleteCustomerStrategy 删除规则组
func (c *Client) DeleteCustomerStrategy(ctx context.Context, strategyID int) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyDel, nil, map[string]any{"strategy_id": strategyID})
}

// GetCorpTagList 获取企业标签库（body：tag_id/group_id 可选过滤）
func (c *Client) GetCorpTagList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECCorpTagList, nil, body)
}

// AddCorpTag 添加企业客户标签（body：group_id/group_name/order/tag/agentid）
func (c *Client) AddCorpTag(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECCorpTagAdd, nil, body)
}

// EditCorpTag 编辑企业客户标签（body：id/name/order/agentid；仅本应用创建的标签）
func (c *Client) EditCorpTag(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECCorpTagEdit, nil, body)
}

// DelCorpTag 删除企业客户标签（body：tag_id/group_id/agentid）
func (c *Client) DelCorpTag(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECCorpTagDel, nil, body)
}

// GetStrategyTagList 获取规则组标签（body：strategy_id/tag_id/group_id）
func (c *Client) GetStrategyTagList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyTagList, nil, body)
}

// AddStrategyTag 创建规则组标签（body：strategy_id/group_id/group_name/order/tag）
func (c *Client) AddStrategyTag(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyTagAdd, nil, body)
}

// EditStrategyTag 编辑规则组标签（body：id/name/order）
func (c *Client) EditStrategyTag(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyTagEdit, nil, body)
}

// DelStrategyTag 删除规则组标签（body：tag_id/group_id 数组，不可同时为空）
func (c *Client) DelStrategyTag(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECStrategyTagDel, nil, body)
}

// MarkExternalContactTag 编辑客户企业标签（body：userid/external_userid/add_tag/remove_tag）
func (c *Client) MarkExternalContactTag(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECMarkTag, nil, body)
}

package wecom

import (
	"context"
	"net/http"
)

// 客户联系子域②表项：在职继承 3 + 离职继承 4（官方清点见 change wecom-contact-inherit-domain proposal）
var (
	epECOnjobTransferCustomer = Endpoint{"ec.inherit.onjob_transfer_customer", http.MethodPost, "/cgi-bin/externalcontact/transfer_customer", nil, true}
	epECOnjobTransferResult   = Endpoint{"ec.inherit.onjob_transfer_result", http.MethodPost, "/cgi-bin/externalcontact/transfer_result", nil, true}
	epECOnjobTransferGroup    = Endpoint{"ec.inherit.onjob_transfer_group", http.MethodPost, "/cgi-bin/externalcontact/groupchat/onjob_transfer", nil, true}
	epECUnassignedList        = Endpoint{"ec.inherit.unassigned_list", http.MethodPost, "/cgi-bin/externalcontact/get_unassigned_list", nil, true}
	epECResignedTransferCust  = Endpoint{"ec.inherit.resigned_transfer_customer", http.MethodPost, "/cgi-bin/externalcontact/resigned/transfer_customer", nil, true}
	epECResignedTransferRes   = Endpoint{"ec.inherit.resigned_transfer_result", http.MethodPost, "/cgi-bin/externalcontact/resigned/transfer_result", nil, true}
	epECResignedTransferGroup = Endpoint{"ec.inherit.resigned_transfer_group", http.MethodPost, "/cgi-bin/externalcontact/groupchat/transfer", nil, true}
)

// TransferOnjobCustomer 分配在职成员的客户（body：handover_userid/takeover_userid/external_userid/transfer_success_msg）
func (c *Client) TransferOnjobCustomer(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECOnjobTransferCustomer, nil, body)
}

// GetOnjobTransferResult 查询客户接替状态（在职继承）
func (c *Client) GetOnjobTransferResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECOnjobTransferResult, nil, body)
}

// TransferOnjobGroupChat 分配在职成员的客户群（body：chat_id_list/new_owner）
func (c *Client) TransferOnjobGroupChat(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECOnjobTransferGroup, nil, body)
}

// GetUnassignedList 获取待分配的离职成员列表（body：page_size/cursor）
func (c *Client) GetUnassignedList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECUnassignedList, nil, body)
}

// TransferResignedCustomer 分配离职成员的客户
func (c *Client) TransferResignedCustomer(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECResignedTransferCust, nil, body)
}

// GetResignedTransferResult 查询客户接替状态（离职继承）
func (c *Client) GetResignedTransferResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECResignedTransferRes, nil, body)
}

// TransferResignedGroupChat 分配离职成员的客户群（body：chat_id_list/new_owner）
func (c *Client) TransferResignedGroupChat(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECResignedTransferGroup, nil, body)
}

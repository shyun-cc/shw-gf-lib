package wecom

import (
	"context"
	"net/http"
)

// 客户联系子域③表项：客户群 3 + 「联系我」6 + 进群方式 4（官方清点见 change wecom-contact-groupchat-domain proposal）
var (
	epECGroupChatList    = Endpoint{"ec.groupchat.list", http.MethodPost, "/cgi-bin/externalcontact/groupchat/list", nil, true}
	epECGroupChatGet     = Endpoint{"ec.groupchat.get", http.MethodPost, "/cgi-bin/externalcontact/groupchat/get", nil, true}
	epECOpenGidToChat    = Endpoint{"ec.groupchat.opengid_to_chatid", http.MethodPost, "/cgi-bin/externalcontact/opengid_to_chatid", nil, true}
	epECContactWayAdd    = Endpoint{"ec.contact_way.add", http.MethodPost, "/cgi-bin/externalcontact/add_contact_way", nil, true}
	epECContactWayGet    = Endpoint{"ec.contact_way.get", http.MethodPost, "/cgi-bin/externalcontact/get_contact_way", nil, true}
	epECContactWayList   = Endpoint{"ec.contact_way.list", http.MethodPost, "/cgi-bin/externalcontact/list_contact_way", nil, true}
	epECContactWayUpdate = Endpoint{"ec.contact_way.update", http.MethodPost, "/cgi-bin/externalcontact/update_contact_way", nil, true}
	epECContactWayDel    = Endpoint{"ec.contact_way.del", http.MethodPost, "/cgi-bin/externalcontact/del_contact_way", nil, true}
	epECCloseTempChat    = Endpoint{"ec.contact_way.close_temp_chat", http.MethodPost, "/cgi-bin/externalcontact/close_temp_chat", nil, true}
	epECJoinWayAdd       = Endpoint{"ec.join_way.add", http.MethodPost, "/cgi-bin/externalcontact/groupchat/add_join_way", nil, true}
	epECJoinWayGet       = Endpoint{"ec.join_way.get", http.MethodPost, "/cgi-bin/externalcontact/groupchat/get_join_way", nil, true}
	epECJoinWayUpdate    = Endpoint{"ec.join_way.update", http.MethodPost, "/cgi-bin/externalcontact/groupchat/update_join_way", nil, true}
	epECJoinWayDel       = Endpoint{"ec.join_way.del", http.MethodPost, "/cgi-bin/externalcontact/groupchat/del_join_way", nil, true}
)

// ListGroupChat 获取客户群列表（body：status_filter/owner_filter/cursor/limit）
func (c *Client) ListGroupChat(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECGroupChatList, nil, body)
}

// GetGroupChat 获取客户群详情（body：chat_id/need_name）
func (c *Client) GetGroupChat(ctx context.Context, chatID string, needName bool) (map[string]any, error) {
	return c.invoke(ctx, epECGroupChatGet, nil, map[string]any{"chat_id": chatID, "need_name": needName})
}

// OpenGidToChatId 客户群 opengid 转换 chat_id
func (c *Client) OpenGidToChatId(ctx context.Context, opengid string) (map[string]any, error) {
	return c.invoke(ctx, epECOpenGidToChat, nil, map[string]any{"opengid": opengid})
}

// AddContactWay 配置客户联系「联系我」方式（body：type/scene/style/user/party/…）
func (c *Client) AddContactWay(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECContactWayAdd, nil, body)
}

// GetContactWay 获取企业已配置的「联系我」方式
func (c *Client) GetContactWay(ctx context.Context, configID string) (map[string]any, error) {
	return c.invoke(ctx, epECContactWayGet, nil, map[string]any{"config_id": configID})
}

// ListContactWay 获取「联系我」配置列表（body：start_time/end_time/cursor/limit）
func (c *Client) ListContactWay(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECContactWayList, nil, body)
}

// UpdateContactWay 更新「联系我」方式（body：config_id + 可更新字段）
func (c *Client) UpdateContactWay(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECContactWayUpdate, nil, body)
}

// DelContactWay 删除「联系我」方式
func (c *Client) DelContactWay(ctx context.Context, configID string) (map[string]any, error) {
	return c.invoke(ctx, epECContactWayDel, nil, map[string]any{"config_id": configID})
}

// CloseTempChat 结束临时会话
func (c *Client) CloseTempChat(ctx context.Context, userid, externalUserid string) (map[string]any, error) {
	return c.invoke(ctx, epECCloseTempChat, nil, map[string]any{"userid": userid, "external_userid": externalUserid})
}

// AddJoinWay 配置客户群进群方式（body：scene/chat_id_list/auto_create_room/…）
func (c *Client) AddJoinWay(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECJoinWayAdd, nil, body)
}

// GetJoinWay 获取进群方式配置
func (c *Client) GetJoinWay(ctx context.Context, configID string) (map[string]any, error) {
	return c.invoke(ctx, epECJoinWayGet, nil, map[string]any{"config_id": configID})
}

// UpdateJoinWay 更新进群方式配置（body：config_id + 可更新字段）
func (c *Client) UpdateJoinWay(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epECJoinWayUpdate, nil, body)
}

// DelJoinWay 删除进群方式配置
func (c *Client) DelJoinWay(ctx context.Context, configID string) (map[string]any, error) {
	return c.invoke(ctx, epECJoinWayDel, nil, map[string]any{"config_id": configID})
}

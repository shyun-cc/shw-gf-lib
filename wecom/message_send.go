package wecom

import (
	"context"
	"encoding/json"
	"net/http"
)

// 消息收发域表项：应用消息 3 + 群聊会话 4 + 学校通知 1（官方清点见 change wecom-message-domain proposal）
var (
	epMsgSend       = Endpoint{"message.send", http.MethodPost, "/cgi-bin/message/send", nil, true}
	epMsgUpdateCard = Endpoint{"message.update_template_card", http.MethodPost, "/cgi-bin/message/update_template_card", nil, true}
	epMsgRecall     = Endpoint{"message.recall", http.MethodPost, "/cgi-bin/message/recall", nil, true}
	epAppChatCreate = Endpoint{"appchat.create", http.MethodPost, "/cgi-bin/appchat/create", nil, true}
	epAppChatUpdate = Endpoint{"appchat.update", http.MethodPost, "/cgi-bin/appchat/update", nil, true}
	epAppChatGet    = Endpoint{"appchat.get", http.MethodGet, "/cgi-bin/appchat/get", []string{"chatid"}, false}
	epAppChatSend   = Endpoint{"appchat.send", http.MethodPost, "/cgi-bin/appchat/send", nil, true}
	epSchoolNotice  = Endpoint{"school.notice_send", http.MethodPost, "/cgi-bin/externalcontact/message/send", nil, true}
)

// SendMessage 发送应用消息：body 含 "msg" 键且值为 message.go 的 Msg 类型时自动展开合并
// （Msg 序列化的 msgtype+载体段并入外层路由字段）；原生 map（内联载体）透传。
func (c *Client) SendMessage(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMsgSend, nil, expandMsg(body))
}

// UpdateTemplateCard 更新模版卡片消息（body：userids/response_code/template_card/button 等）
func (c *Client) UpdateTemplateCard(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMsgUpdateCard, nil, body)
}

// RecallMessage 撤回应用消息
func (c *Client) RecallMessage(ctx context.Context, msgid string) (map[string]any, error) {
	return c.invoke(ctx, epMsgRecall, nil, map[string]any{"msgid": msgid})
}

// CreateAppChat 创建群聊会话（body：name/owner/userlist/chatid）
func (c *Client) CreateAppChat(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAppChatCreate, nil, body)
}

// UpdateAppChat 修改群聊会话（extra 可选字段：name/owner/add_user_list/del_user_list）
func (c *Client) UpdateAppChat(ctx context.Context, chatid string, extra map[string]any) (map[string]any, error) {
	if extra == nil {
		extra = map[string]any{}
	}
	extra["chatid"] = chatid
	return c.invoke(ctx, epAppChatUpdate, nil, extra)
}

// GetAppChat 获取群聊会话
func (c *Client) GetAppChat(ctx context.Context, chatid string) (map[string]any, error) {
	return c.invoke(ctx, epAppChatGet, map[string]string{"chatid": chatid}, nil)
}

// SendAppChat 应用推送消息到群聊（body：chatid/msgtype/safe + 载体）
func (c *Client) SendAppChat(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAppChatSend, nil, expandMsg(body))
}

// SendSchoolNotice 发送「学校通知」（body：recv_scope/to_parent_userid/… + 载体；支持 msg 键展开）
func (c *Client) SendSchoolNotice(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSchoolNotice, nil, expandMsg(body))
}

// expandMsg body 含 "msg" 键且其值实现 json.Marshaler（9 种 Msg 类型）时，
// 序列化→解码→合入外层（删除 msg 键）；否则原样返回。
func expandMsg(body map[string]any) map[string]any {
	raw, ok := body["msg"]
	if !ok {
		return body
	}
	m, ok := raw.(json.Marshaler)
	if !ok {
		return body
	}
	encoded, err := m.MarshalJSON()
	if err != nil {
		return body
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return body
	}
	merged := make(map[string]any, len(body)-1+len(decoded))
	for k, v := range body {
		if k != "msg" {
			merged[k] = v
		}
	}
	for k, v := range decoded {
		merged[k] = v
	}
	return merged
}

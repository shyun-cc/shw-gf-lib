package wecom

import (
	"context"
	"net/http"
)

// 微信客服域表项：升级服务 3 + 客服账号 5 + 接待人员 3 + 会话分配 2 + 消息 2 +
// 客户基础信息 1 + 统计 2 + 知识库 8（官方清点见 change wecom-kf-domain proposal）
var (
	epKfUpgradeCfg     = Endpoint{"kf.upgrade_config", http.MethodGet, "/cgi-bin/kf/customer/get_upgrade_service_config", nil, false}
	epKfUpgrade        = Endpoint{"kf.upgrade_service", http.MethodPost, "/cgi-bin/kf/customer/upgrade_service", nil, true}
	epKfUpgradeCancel  = Endpoint{"kf.cancel_upgrade_service", http.MethodPost, "/cgi-bin/kf/customer/cancel_upgrade_service", nil, true}
	epKfAccountAdd     = Endpoint{"kf.account.add", http.MethodPost, "/cgi-bin/kf/account/add", nil, true}
	epKfAccountDel     = Endpoint{"kf.account.del", http.MethodPost, "/cgi-bin/kf/account/del", nil, true}
	epKfAccountUpdate  = Endpoint{"kf.account.update", http.MethodPost, "/cgi-bin/kf/account/update", nil, true}
	epKfAccountList    = Endpoint{"kf.account.list", http.MethodPost, "/cgi-bin/kf/account/list", nil, true}
	epKfContactWay     = Endpoint{"kf.contact_way", http.MethodPost, "/cgi-bin/kf/add_contact_way", nil, true}
	epKfServicerAdd    = Endpoint{"kf.servicer.add", http.MethodPost, "/cgi-bin/kf/servicer/add", nil, true}
	epKfServicerDel    = Endpoint{"kf.servicer.del", http.MethodPost, "/cgi-bin/kf/servicer/del", nil, true}
	epKfServicerList   = Endpoint{"kf.servicer.list", http.MethodGet, "/cgi-bin/kf/servicer/list", []string{"open_kfid"}, false}
	epKfStateGet       = Endpoint{"kf.service_state.get", http.MethodPost, "/cgi-bin/kf/service_state/get", nil, true}
	epKfStateTrans     = Endpoint{"kf.service_state.trans", http.MethodPost, "/cgi-bin/kf/service_state/trans", nil, true}
	epKfSendMsg        = Endpoint{"kf.send_msg", http.MethodPost, "/cgi-bin/kf/send_msg", nil, true}
	epKfSendMsgOnEvent = Endpoint{"kf.send_msg_on_event", http.MethodPost, "/cgi-bin/kf/send_msg_on_event", nil, true}
	epKfCustomerGet    = Endpoint{"kf.customer.batchget", http.MethodPost, "/cgi-bin/kf/customer/batchget", nil, true}
	epKfCorpStat       = Endpoint{"kf.stats.corp", http.MethodPost, "/cgi-bin/kf/get_corp_statistic", nil, true}
	epKfServicerStat   = Endpoint{"kf.stats.servicer", http.MethodPost, "/cgi-bin/kf/get_servicer_statistic", nil, true}
	epKfGroupAdd       = Endpoint{"kf.knowledge.add_group", http.MethodPost, "/cgi-bin/kf/knowledge/add_group", nil, true}
	epKfGroupDel       = Endpoint{"kf.knowledge.del_group", http.MethodPost, "/cgi-bin/kf/knowledge/del_group", nil, true}
	epKfGroupMod       = Endpoint{"kf.knowledge.mod_group", http.MethodPost, "/cgi-bin/kf/knowledge/mod_group", nil, true}
	epKfGroupList      = Endpoint{"kf.knowledge.list_group", http.MethodPost, "/cgi-bin/kf/knowledge/list_group", nil, true}
	epKfIntentAdd      = Endpoint{"kf.knowledge.add_intent", http.MethodPost, "/cgi-bin/kf/knowledge/add_intent", nil, true}
	epKfIntentDel      = Endpoint{"kf.knowledge.del_intent", http.MethodPost, "/cgi-bin/kf/knowledge/del_intent", nil, true}
	epKfIntentMod      = Endpoint{"kf.knowledge.mod_intent", http.MethodPost, "/cgi-bin/kf/knowledge/mod_intent", nil, true}
	epKfIntentList     = Endpoint{"kf.knowledge.list_intent", http.MethodPost, "/cgi-bin/kf/knowledge/list_intent", nil, true}
)

// GetUpgradeServiceConfig 获取「升级服务」配置的专员与客户群
func (c *Client) GetUpgradeServiceConfig(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epKfUpgradeCfg, nil, nil)
}

// UpgradeService 为客户升级为专员或客户群服务（body：open_kfid/external_userid/type/member/groupchat）
func (c *Client) UpgradeService(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfUpgrade, nil, body)
}

// CancelUpgradeService 为客户取消推荐
func (c *Client) CancelUpgradeService(ctx context.Context, openKfid, externalUserid string) (map[string]any, error) {
	return c.invoke(ctx, epKfUpgradeCancel, nil, map[string]any{"open_kfid": openKfid, "external_userid": externalUserid})
}

// AddKfAccount 添加客服账号（body：name/media_id）
func (c *Client) AddKfAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfAccountAdd, nil, body)
}

// DelKfAccount 删除客服账号
func (c *Client) DelKfAccount(ctx context.Context, openKfid string) (map[string]any, error) {
	return c.invoke(ctx, epKfAccountDel, nil, map[string]any{"open_kfid": openKfid})
}

// UpdateKfAccount 修改客服账号（body：open_kfid/name/media_id）
func (c *Client) UpdateKfAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfAccountUpdate, nil, body)
}

// ListKfAccount 获取客服账号列表（body：offset/limit）
func (c *Client) ListKfAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfAccountList, nil, body)
}

// GetKfContactWay 获取客服账号链接（body：open_kfid/scene）
func (c *Client) GetKfContactWay(ctx context.Context, openKfid, scene string) (map[string]any, error) {
	return c.invoke(ctx, epKfContactWay, nil, map[string]any{"open_kfid": openKfid, "scene": scene})
}

// AddKfServicer 添加接待人员（body：open_kfid/userid_list/department_id_list）
func (c *Client) AddKfServicer(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfServicerAdd, nil, body)
}

// DelKfServicer 删除接待人员
func (c *Client) DelKfServicer(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfServicerDel, nil, body)
}

// ListKfServicer 获取接待人员列表
func (c *Client) ListKfServicer(ctx context.Context, openKfid string) (map[string]any, error) {
	return c.invoke(ctx, epKfServicerList, map[string]string{"open_kfid": openKfid}, nil)
}

// GetKfServiceState 获取会话状态（body：open_kfid/external_userid）
func (c *Client) GetKfServiceState(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfStateGet, nil, body)
}

// TransKfServiceState 变更会话状态（body：open_kfid/external_userid/service_state/servicer_userid）
func (c *Client) TransKfServiceState(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfStateTrans, nil, body)
}

// SendKfMsg 发送消息（body：touser/open_kfid/msgtype + 载体；支持 msg 键展开 Msg 类型）
func (c *Client) SendKfMsg(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfSendMsg, nil, expandMsg(body))
}

// SendKfMsgOnEvent 发送欢迎语等事件响应消息（body：code/msgtype + 载体）
func (c *Client) SendKfMsgOnEvent(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfSendMsgOnEvent, nil, expandMsg(body))
}

// BatchGetKfCustomer 获取客户基础信息（body：external_userid_list/need_enter_session_context）
func (c *Client) BatchGetKfCustomer(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfCustomerGet, nil, body)
}

// GetKfCorpStatistic 客户数据统计——企业汇总（body：open_kfid/start_time/end_time）
func (c *Client) GetKfCorpStatistic(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfCorpStat, nil, body)
}

// GetKfServicerStatistic 客户数据统计——接待人员明细（body：open_kfid/servicer_userid/start_time/end_time）
func (c *Client) GetKfServicerStatistic(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfServicerStat, nil, body)
}

// AddKfGroup 添加知识库分组（body：name）
func (c *Client) AddKfGroup(ctx context.Context, name string) (map[string]any, error) {
	return c.invoke(ctx, epKfGroupAdd, nil, map[string]any{"name": name})
}

// DelKfGroup 删除知识库分组
func (c *Client) DelKfGroup(ctx context.Context, groupID string) (map[string]any, error) {
	return c.invoke(ctx, epKfGroupDel, nil, map[string]any{"group_id": groupID})
}

// ModKfGroup 修改知识库分组（body：group_id/name）
func (c *Client) ModKfGroup(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfGroupMod, nil, body)
}

// ListKfGroup 获取知识库分组列表（body：cursor/limit/group_id）
func (c *Client) ListKfGroup(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfGroupList, nil, body)
}

// AddKfIntent 添加知识库问答（body：group_id/question/similar_questions/answers）
func (c *Client) AddKfIntent(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfIntentAdd, nil, body)
}

// DelKfIntent 删除知识库问答
func (c *Client) DelKfIntent(ctx context.Context, intentID string) (map[string]any, error) {
	return c.invoke(ctx, epKfIntentDel, nil, map[string]any{"intent_id": intentID})
}

// ModKfIntent 修改知识库问答（body：intent_id/question/similar_questions/answers）
func (c *Client) ModKfIntent(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfIntentMod, nil, body)
}

// ListKfIntent 获取知识库问答列表（body：cursor/limit/group_id/intent_id）
func (c *Client) ListKfIntent(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epKfIntentList, nil, body)
}

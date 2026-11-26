package wxapp

import (
	"context"
	"net/http"
	"strconv"
)

// 消息相关·动态消息 + 订阅消息表项（官方取证见 change wxapp-subscribe-message proposal）
var (
	epActivityIDCreate = Endpoint{"msg.activityid_create", http.MethodGet, "/cgi-bin/message/wxopen/activityid/create", []string{"access_token", "unionid", "openid"}, false}
	epUpdatableSend    = Endpoint{"msg.updatable_send", http.MethodPost, "/cgi-bin/message/wxopen/updatablemsg/send", []string{"access_token"}, true}
	epChatToolSend     = Endpoint{"msg.chattool_send", http.MethodPost, "/cgi-bin/message/wxopen/chattoolmsg/send", []string{"access_token"}, true}

	epSubsGetCategory = Endpoint{"subsmsg.getcategory", http.MethodGet, "/wxaapi/newtmpl/getcategory", []string{"access_token"}, false}
	epSubsPubTitles   = Endpoint{"subsmsg.pubtitles", http.MethodGet, "/wxaapi/newtmpl/getpubtemplatetitles", []string{"access_token", "ids", "start", "limit"}, false}
	epSubsPubKeywords = Endpoint{"subsmsg.pubkeywords", http.MethodGet, "/wxaapi/newtmpl/getpubtemplatekeywords", []string{"access_token", "tid"}, false}
	epSubsAddTemplate = Endpoint{"subsmsg.addtemplate", http.MethodPost, "/wxaapi/newtmpl/addtemplate", []string{"access_token"}, true}
	epSubsGetTemplate = Endpoint{"subsmsg.gettemplate", http.MethodGet, "/wxaapi/newtmpl/gettemplate", []string{"access_token"}, false}
	epSubsDelTemplate = Endpoint{"subsmsg.deltemplate", http.MethodPost, "/wxaapi/newtmpl/deltemplate", []string{"access_token"}, true}
	epSubsSend        = Endpoint{"subsmsg.send", http.MethodPost, "/cgi-bin/message/subscribe/send", []string{"access_token"}, true}
	epSubsSetNotify   = Endpoint{"subsmsg.set_user_notify", http.MethodPost, "/wxa/set_user_notify", []string{"access_token"}, true}
	epSubsSetNotifyEx = Endpoint{"subsmsg.set_notifyext", http.MethodPost, "/wxa/set_user_notifyext", []string{"access_token"}, true}
	epSubsGetNotify   = Endpoint{"subsmsg.get_user_notify", http.MethodPost, "/wxa/get_user_notify", []string{"access_token"}, true}
)

// CreateActivityID 创建动态消息 activity_id（默认 24 小时过期；私密消息场景 unionid/openid
// 二选一指定分享者，普通场景均留空）。
func (s *WxAppService) CreateActivityID(ctx context.Context, unionid, openid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]string{"access_token": token}
	if unionid != "" {
		query["unionid"] = unionid
	}
	if openid != "" {
		query["openid"] = openid
	}
	return s.invoke(ctx, epActivityIDCreate, query, nil)
}

// SetUpdatableMsg 修改被分享的动态消息（target_state 0 未开始须 member_count/room_limit、
// 1 已开始须 path/version_type——条件必填由调用方保证）。
func (s *WxAppService) SetUpdatableMsg(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epUpdatableSend, map[string]string{"access_token": token}, body)
}

// SetChatToolMsg 修改小程序聊天工具的动态卡片消息（version_type 0 正式/1 开发/2 体验；
// participator_info_list.state 目前仅支持设为 1 已完成）。
func (s *WxAppService) SetChatToolMsg(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epChatToolSend, map[string]string{"access_token": token}, body)
}

// GetSubscribeCategory 获取账号所属类目（用于查询公共模板库）。
func (s *WxAppService) GetSubscribeCategory(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsGetCategory, map[string]string{"access_token": token}, nil)
}

// ListPublicTemplateTitles 获取类目下的公共模板标题（ids 多类目逗号分隔；limit ≤30）。
func (s *WxAppService) ListPublicTemplateTitles(ctx context.Context, ids string, start, limit int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsPubTitles, map[string]string{
		"access_token": token, "ids": ids, "start": strconv.Itoa(start), "limit": strconv.Itoa(limit),
	}, nil)
}

// GetPublicTemplateKeywords 获取模板标题下的关键词（kid 供选用模板组合）。
func (s *WxAppService) GetPublicTemplateKeywords(ctx context.Context, tid string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsPubKeywords, map[string]string{"access_token": token, "tid": tid}, nil)
}

// AddSubscribeTemplate 选用模板到私有库（kidList 2-5 个关键词；sceneDesc ≤15 字；返回 priTmplId）。
func (s *WxAppService) AddSubscribeTemplate(ctx context.Context, tid string, kidList []int, sceneDesc string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsAddTemplate, map[string]string{"access_token": token}, map[string]any{
		"tid": tid, "kidList": kidList, "sceneDesc": sceneDesc,
	})
}

// ListSubscribeTemplates 获取已有模板列表（priTmplId 为发送订阅消息的模板 id）。
func (s *WxAppService) ListSubscribeTemplates(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsGetTemplate, map[string]string{"access_token": token}, nil)
}

// DelSubscribeTemplate 删除私有模板。
func (s *WxAppService) DelSubscribeTemplate(ctx context.Context, priTmplID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsDelTemplate, map[string]string{"access_token": token}, map[string]string{"priTmplId": priTmplID})
}

// SendSubscribeMessage 发送订阅消息（touser/template_id/data/miniprogram_state(developer/
// trial/formal)/lang 必填，page 选填——由调用方组 body）。
func (s *WxAppService) SendSubscribeMessage(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsSend, map[string]string{"access_token": token}, body)
}

// SetUserNotify 激活与更新服务卡片（微信支付订单号作 notify_code 时可传 check_json 校验）。
func (s *WxAppService) SetUserNotify(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsSetNotify, map[string]string{"access_token": token}, body)
}

// SetUserNotifyExt 更新服务卡片扩展信息。
func (s *WxAppService) SetUserNotifyExt(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsSetNotifyEx, map[string]string{"access_token": token}, body)
}

// GetUserNotify 查询服务卡片状态（code_state 0 正常/1 风险/2 异常/10 用户拒收）。
func (s *WxAppService) GetUserNotify(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epSubsGetNotify, map[string]string{"access_token": token}, body)
}

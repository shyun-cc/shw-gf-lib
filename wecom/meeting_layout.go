package wecom

import (
	"context"
	"net/http"
)

// 会议域子域⑤表项·布局与背景管理（15 端点）：基础布局 4 + 高级布局 6（仅 H.323/SIP 终端）+ 背景 5
// （官方清点见 change wecom-meeting-layout-record proposal）
var (
	epMtgLayoutListTpl    = Endpoint{"meeting.layout.list_template", http.MethodGet, "/cgi-bin/meeting/layout/list_template", nil, false}
	epMtgLayoutAdd        = Endpoint{"meeting.layout.add", http.MethodPost, "/cgi-bin/meeting/layout/add", nil, true}
	epMtgLayoutUpdate     = Endpoint{"meeting.layout.update", http.MethodPost, "/cgi-bin/meeting/layout/update", nil, true}
	epMtgLayoutSetDefault = Endpoint{"meeting.layout.set_default", http.MethodPost, "/cgi-bin/meeting/layout/set_default", nil, true}
	epAdvLayoutAdd        = Endpoint{"meeting.advanced_layout.add", http.MethodPost, "/cgi-bin/meeting/advanced_layout/add", nil, true}
	epAdvLayoutUpdate     = Endpoint{"meeting.advanced_layout.update", http.MethodPost, "/cgi-bin/meeting/advanced_layout/update", nil, true}
	epAdvLayoutApply      = Endpoint{"meeting.advanced_layout.apply", http.MethodPost, "/cgi-bin/meeting/advanced_layout/apply", nil, true}
	epAdvLayoutList       = Endpoint{"meeting.advanced_layout.list", http.MethodPost, "/cgi-bin/meeting/advanced_layout/list", nil, true}
	epAdvLayoutUserGet    = Endpoint{"meeting.advanced_layout.get_user_layout", http.MethodPost, "/cgi-bin/meeting/advanced_layout/get_user_layout", nil, true}
	epAdvLayoutBatchDel   = Endpoint{"meeting.advanced_layout.batch_delete", http.MethodPost, "/cgi-bin/meeting/advanced_layout/batch_delete", nil, true}
	epMtgBgAdd            = Endpoint{"meeting.layout.add_background", http.MethodPost, "/cgi-bin/meeting/layout/add_background", nil, true}
	epMtgBgSetDefault     = Endpoint{"meeting.layout.set_default_background", http.MethodPost, "/cgi-bin/meeting/layout/set_default_background", nil, true}
	epMtgBgList           = Endpoint{"meeting.layout.list_background", http.MethodPost, "/cgi-bin/meeting/layout/list_background", nil, true}
	epMtgBgDelete         = Endpoint{"meeting.layout.delete_background", http.MethodPost, "/cgi-bin/meeting/layout/delete_background", nil, true}
	epMtgBgBatchDelete    = Endpoint{"meeting.layout.batch_delete_background", http.MethodPost, "/cgi-bin/meeting/layout/batch_delete_background", nil, true}
)

// ListMeetingLayoutTemplates 获取企业布局模板列表（模板图含宫格 ID，供布局座次映射）。
func (c *Client) ListMeetingLayoutTemplates(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epMtgLayoutListTpl, nil, nil)
}

// AddMeetingLayout 添加会议基础布局（一场会议 ≤10 个布局；宫格类型 1 视频/2 共享/3 拓展应用，
// 拓展应用每页仅一个且须与会议绑定）。
func (c *Client) AddMeetingLayout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgLayoutAdd, nil, body)
}

// UpdateMeetingLayout 修改会议基础布局（座次会前仅邀请者、会中仅参会成员可设）。
func (c *Client) UpdateMeetingLayout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgLayoutUpdate, nil, body)
}

// SetMeetingDefaultLayout 设置会议默认布局（selected_layout_id 空串恢复会议自带默认布局）。
func (c *Client) SetMeetingDefaultLayout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgLayoutSetDefault, nil, body)
}

// AddMeetingAdvancedLayout 添加会议高级布局（仅 H.323/SIP 会议室终端；≤20 个布局；
// 轮询设置 polling_setting 含间隔单位/时长/忽略未开视频/未入会）。
func (c *Client) AddMeetingAdvancedLayout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAdvLayoutAdd, nil, body)
}

// UpdateMeetingAdvancedLayout 修改会议高级布局（全量更新；被会议使用中的布局改后自动应用）。
func (c *Client) UpdateMeetingAdvancedLayout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAdvLayoutUpdate, nil, body)
}

// ApplyMeetingAdvancedLayout 设置高级布局（user_list ≤20 人个性布局，空列表对整个会议设置；
// 优先级 个性布局 > 自定义布局 > 默认布局；MRA 不支持同框模式）。
func (c *Client) ApplyMeetingAdvancedLayout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAdvLayoutApply, nil, body)
}

// ListMeetingAdvancedLayouts 获取会议布局列表（基础 + 高级，含 selected_layout_id）。
func (c *Client) ListMeetingAdvancedLayouts(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAdvLayoutList, nil, body)
}

// GetMeetingUserLayout 获取用户布局（tmp_openid + instance_id 定位；高级布局仅 H.323/SIP）。
func (c *Client) GetMeetingUserLayout(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAdvLayoutUserGet, nil, body)
}

// BatchDeleteMeetingLayouts 批量删除布局（layout_id_list ≤20；应用中的布局不可删，不校验存在性）。
func (c *Client) BatchDeleteMeetingLayouts(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAdvLayoutBatchDel, nil, body)
}

// AddMeetingBackground 添加会议背景（≤7 张；PNG ≤10MB ≥1920x1080；异步上传，
// 结果经素材上传回调通知）。
func (c *Client) AddMeetingBackground(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgBgAdd, nil, body)
}

// SetMeetingDefaultBackground 设置会议默认背景（selected_background_id 空串恢复默认黑色背景）。
func (c *Client) SetMeetingDefaultBackground(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgBgSetDefault, nil, body)
}

// ListMeetingBackgrounds 获取会议背景列表（含 selected_background_id）。
func (c *Client) ListMeetingBackgrounds(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgBgList, nil, body)
}

// DeleteMeetingBackground 删除会议背景（应用中的背景不可删，先切换或恢复默认）。
func (c *Client) DeleteMeetingBackground(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgBgDelete, nil, body)
}

// BatchDeleteMeetingBackgrounds 批量删除会议背景。
func (c *Client) BatchDeleteMeetingBackgrounds(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgBgBatchDelete, nil, body)
}

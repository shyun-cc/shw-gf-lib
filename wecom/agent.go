package wecom

import (
	"context"
	"net/http"
	"strconv"
)

// 应用管理域表项：应用信息 3 + 自定义菜单 3 + 工作台 5（官方清点见 change wecom-agent-domain proposal）
var (
	epAgentGet       = Endpoint{"agent.get", http.MethodGet, "/cgi-bin/agent/get", []string{"agentid"}, false}
	epAgentList      = Endpoint{"agent.list", http.MethodGet, "/cgi-bin/agent/list", nil, false}
	epAgentSet       = Endpoint{"agent.set", http.MethodPost, "/cgi-bin/agent/set", nil, true}
	epMenuCreate     = Endpoint{"menu.create", http.MethodPost, "/cgi-bin/menu/create", []string{"agentid"}, true}
	epMenuGet        = Endpoint{"menu.get", http.MethodGet, "/cgi-bin/menu/get", []string{"agentid"}, false}
	epMenuDelete     = Endpoint{"menu.delete", http.MethodGet, "/cgi-bin/menu/delete", []string{"agentid"}, false}
	epWBSetTemplate  = Endpoint{"agent.set_workbench_template", http.MethodPost, "/cgi-bin/agent/set_workbench_template", nil, true}
	epWBGetTemplate  = Endpoint{"agent.get_workbench_template", http.MethodPost, "/cgi-bin/agent/get_workbench_template", nil, true}
	epWBSetData      = Endpoint{"agent.set_workbench_data", http.MethodPost, "/cgi-bin/agent/set_workbench_data", nil, true}
	epWBBatchSetData = Endpoint{"agent.batch_set_workbench_data", http.MethodPost, "/cgi-bin/agent/batch_set_workbench_data", nil, true}
	epWBGetData      = Endpoint{"agent.get_workbench_data", http.MethodPost, "/cgi-bin/agent/get_workbench_data", nil, true}
)

// GetAgent 获取指定应用详情
func (c *Client) GetAgent(ctx context.Context, agentid int) (map[string]any, error) {
	return c.invoke(ctx, epAgentGet, map[string]string{"agentid": strconv.Itoa(agentid)}, nil)
}

// ListAgents 获取 access_token 对应的应用列表
func (c *Client) ListAgents(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epAgentList, nil, nil)
}

// SetAgent 设置应用（body：name/description/logo_mediaid/redirect_domain/home_url 等，agentid 必填）
func (c *Client) SetAgent(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epAgentSet, nil, body)
}

// CreateMenu 创建自定义菜单（body：button 树）
func (c *Client) CreateMenu(ctx context.Context, agentid int, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMenuCreate, map[string]string{"agentid": strconv.Itoa(agentid)}, body)
}

// GetMenu 获取自定义菜单
func (c *Client) GetMenu(ctx context.Context, agentid int) (map[string]any, error) {
	return c.invoke(ctx, epMenuGet, map[string]string{"agentid": strconv.Itoa(agentid)}, nil)
}

// DeleteMenu 删除自定义菜单
func (c *Client) DeleteMenu(ctx context.Context, agentid int) (map[string]any, error) {
	return c.invoke(ctx, epMenuDelete, map[string]string{"agentid": strconv.Itoa(agentid)}, nil)
}

// SetWorkbenchTemplate 设置应用在工作台展示的模版（body：agentid/type/keydata/image/list/webview/replace_user_data）
func (c *Client) SetWorkbenchTemplate(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWBSetTemplate, nil, body)
}

// GetWorkbenchTemplate 获取应用在工作台展示的模版
func (c *Client) GetWorkbenchTemplate(ctx context.Context, agentid int) (map[string]any, error) {
	return c.invoke(ctx, epWBGetTemplate, nil, map[string]any{"agentid": agentid})
}

// SetWorkbenchData 设置应用在用户工作台展示的数据（body：agentid/userid/type/…）
func (c *Client) SetWorkbenchData(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWBSetData, nil, body)
}

// BatchSetWorkbenchData 批量设置用户工作台数据（body：agentid/userid_list/data）
func (c *Client) BatchSetWorkbenchData(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epWBBatchSetData, nil, body)
}

// GetWorkbenchData 获取应用在用户工作台展示的数据
func (c *Client) GetWorkbenchData(ctx context.Context, agentid int, userid string) (map[string]any, error) {
	return c.invoke(ctx, epWBGetData, nil, map[string]any{"agentid": agentid, "userid": userid})
}

package wecom

import (
	"context"
	"net/http"
)

// 通讯录管理域表项：查看权限规则 4 + 异步导入 4 + 异步导出 5（官方文档清点见 change wecom-contact-domain proposal）
var (
	epRuleCreate       = Endpoint{"contactrule.create", http.MethodPost, "/cgi-bin/contactrule/create", nil, true}
	epRuleList         = Endpoint{"contactrule.list", http.MethodPost, "/cgi-bin/contactrule/list", nil, false}
	epRuleUpdate       = Endpoint{"contactrule.update", http.MethodPost, "/cgi-bin/contactrule/update", nil, true}
	epRuleDelete       = Endpoint{"contactrule.delete", http.MethodPost, "/cgi-bin/contactrule/delete", nil, true}
	epBatchSyncUser    = Endpoint{"batch.syncuser", http.MethodPost, "/cgi-bin/batch/syncuser", nil, true}
	epBatchReplaceUser = Endpoint{"batch.replaceuser", http.MethodPost, "/cgi-bin/batch/replaceuser", nil, true}
	epBatchReplaceDept = Endpoint{"batch.replaceparty", http.MethodPost, "/cgi-bin/batch/replaceparty", nil, true}
	epBatchGetResult   = Endpoint{"batch.getresult", http.MethodGet, "/cgi-bin/batch/getresult", []string{"jobid"}, false}
	epExportSimpleUser = Endpoint{"export.simple_user", http.MethodPost, "/cgi-bin/export/simple_user", nil, true}
	epExportUser       = Endpoint{"export.user", http.MethodPost, "/cgi-bin/export/user", nil, true}
	epExportDept       = Endpoint{"export.department", http.MethodPost, "/cgi-bin/export/department", nil, true}
	epExportTagUser    = Endpoint{"export.taguser", http.MethodPost, "/cgi-bin/export/taguser", nil, true}
	epExportGetResult  = Endpoint{"export.get_result", http.MethodGet, "/cgi-bin/export/get_result", []string{"jobid"}, false}
)

// CreateContactRule 创建通讯录查看权限规则（rules 数组结构见官方文档）；仅通讯录同步应用可调
func (c *Client) CreateContactRule(ctx context.Context, rules []map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRuleCreate, nil, map[string]any{"rules": rules})
}

// ListContactRule 读取规则列表
func (c *Client) ListContactRule(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epRuleList, nil, nil)
}

// UpdateContactRule 修改规则（rules 内含 rule_id）
func (c *Client) UpdateContactRule(ctx context.Context, rules []map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epRuleUpdate, nil, map[string]any{"rules": rules})
}

// DeleteContactRule 删除规则（rule_ids 最多 100）
func (c *Client) DeleteContactRule(ctx context.Context, ruleIDs []string) (map[string]any, error) {
	return c.invoke(ctx, epRuleDelete, nil, map[string]any{"rule_ids": ruleIDs})
}

// BatchSyncUser 增量更新成员（body：media_id/to_invite/callback）；返回 jobid
func (c *Client) BatchSyncUser(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBatchSyncUser, nil, body)
}

// BatchReplaceUser 全量覆盖成员
func (c *Client) BatchReplaceUser(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBatchReplaceUser, nil, body)
}

// BatchReplaceParty 全量覆盖部门
func (c *Client) BatchReplaceParty(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBatchReplaceDept, nil, body)
}

// BatchGetResult 获取异步任务结果（jobid 来自 syncuser/replaceuser/replaceparty）
func (c *Client) BatchGetResult(ctx context.Context, jobid string) (map[string]any, error) {
	return c.invoke(ctx, epBatchGetResult, map[string]string{"jobid": jobid}, nil)
}

// ExportSimpleUser 导出成员（encodingAESKey 为导出文件加密密钥；blockSize 0 用官方默认）
func (c *Client) ExportSimpleUser(ctx context.Context, encodingAESKey string, blockSize int) (map[string]any, error) {
	return c.invoke(ctx, epExportSimpleUser, nil, exportBody(0, encodingAESKey, blockSize, false))
}

// ExportUser 导出成员详情
func (c *Client) ExportUser(ctx context.Context, encodingAESKey string, blockSize int) (map[string]any, error) {
	return c.invoke(ctx, epExportUser, nil, exportBody(0, encodingAESKey, blockSize, false))
}

// ExportDepartment 导出部门
func (c *Client) ExportDepartment(ctx context.Context, encodingAESKey string, blockSize int) (map[string]any, error) {
	return c.invoke(ctx, epExportDept, nil, exportBody(0, encodingAESKey, blockSize, false))
}

// ExportTagUser 导出标签成员
func (c *Client) ExportTagUser(ctx context.Context, tagID int, encodingAESKey string, blockSize int) (map[string]any, error) {
	return c.invoke(ctx, epExportTagUser, nil, exportBody(tagID, encodingAESKey, blockSize, true))
}

// ExportGetResult 获取导出结果（jobid 来自 export 族）；结果文件的下载与解密由调用方处理
func (c *Client) ExportGetResult(ctx context.Context, jobid string) (map[string]any, error) {
	return c.invoke(ctx, epExportGetResult, map[string]string{"jobid": jobid}, nil)
}

func exportBody(tagID int, encodingAESKey string, blockSize int, withTag bool) map[string]any {
	body := map[string]any{"encoding_aeskey": encodingAESKey}
	if withTag {
		body["tagid"] = tagID
	}
	if blockSize > 0 {
		body["block_size"] = blockSize
	}
	return body
}

package wecom

import (
	"context"
	"net/http"
)

// 基础杂项表项（按 2026-08-24 范围裁决 wecom 收官批；官方取证见 change
// wecom-security-misc proposal——IP 3 + ID 转换 1 + 操作日志 2 + 安全管理 13）
var (
	epMiscAPIDomainIP  = Endpoint{"misc.api_domain_ip", http.MethodGet, "/cgi-bin/get_api_domain_ip", nil, false}
	epMiscCallbackIP   = Endpoint{"misc.callback_ip", http.MethodGet, "/cgi-bin/getcallbackip", nil, false}
	epMiscServerDomain = Endpoint{"misc.server_domain_ip", http.MethodGet, "/cgi-bin/security/get_server_domain_ip", nil, false}
	epMiscConvertTmpID = Endpoint{"misc.convert_tmp_external_userid", http.MethodPost, "/cgi-bin/idconvert/convert_tmp_external_userid", nil, true}

	epMiscMemberLog = Endpoint{"misc.member_oper_log", http.MethodPost, "/cgi-bin/security/member_oper_log/list", nil, true}
	epMiscAdminLog  = Endpoint{"misc.admin_oper_log", http.MethodPost, "/cgi-bin/security/admin_oper_log/list", nil, true}

	epSecScreen     = Endpoint{"sec.screen_record", http.MethodPost, "/cgi-bin/security/get_screen_oper_record", nil, true}
	epSecFile       = Endpoint{"sec.file_record", http.MethodPost, "/cgi-bin/security/get_file_oper_record", nil, true}
	epSecDevImport  = Endpoint{"sec.device_import", http.MethodPost, "/cgi-bin/security/trustdevice/import", nil, true}
	epSecDevList    = Endpoint{"sec.device_list", http.MethodPost, "/cgi-bin/security/trustdevice/list", nil, true}
	epSecDevByUser  = Endpoint{"sec.device_get_by_user", http.MethodPost, "/cgi-bin/security/trustdevice/get_by_user", nil, true}
	epSecDevDelete  = Endpoint{"sec.device_delete", http.MethodPost, "/cgi-bin/security/trustdevice/delete", nil, true}
	epSecDevApprove = Endpoint{"sec.device_approve", http.MethodPost, "/cgi-bin/security/trustdevice/approve", nil, true}
	epSecDevReject  = Endpoint{"sec.device_reject", http.MethodPost, "/cgi-bin/security/trustdevice/reject", nil, true}

	epSecVipAddJob = Endpoint{"sec.vip_add_job", http.MethodPost, "/cgi-bin/security/vip/submit_batch_add_job", nil, true}
	epSecVipAddRes = Endpoint{"sec.vip_add_result", http.MethodPost, "/cgi-bin/security/vip/batch_add_job_result", nil, true}
	epSecVipDelJob = Endpoint{"sec.vip_del_job", http.MethodPost, "/cgi-bin/security/vip/submit_batch_del_job", nil, true}
	epSecVipDelRes = Endpoint{"sec.vip_del_result", http.MethodPost, "/cgi-bin/security/vip/batch_del_job_result", nil, true}
	epSecVipList   = Endpoint{"sec.vip_list", http.MethodPost, "/cgi-bin/security/vip/list", nil, true}
)

// GetAPIDomainIPs 获取企业微信接口 IP 段（qyapi 域名解析段；建议每日拉取更新防火墙）。
func (c *Client) GetAPIDomainIPs(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epMiscAPIDomainIP, nil, nil)
}

// GetCallbackIPs 获取企业微信回调 IP 段（回调来源白名单用）。
func (c *Client) GetCallbackIPs(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epMiscCallbackIP, nil, nil)
}

// GetServerDomainIPs 获取企业微信域名 IP 信息（domain_list+ip_list 明细含协议端口；
// 仅自建应用且须配置到「域名IP-可调用API的应用」）。
func (c *Client) GetServerDomainIPs(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epMiscServerDomain, nil, nil)
}

// ConvertTmpExternalUserid tmp_external_userid 转换（business_type 1 会议/2 收集表/3 智能表；
// user_type 1 客户（须客户联系权限）/2 企业互联/3 上下游/4 互联企业；list ≤100；
// 调用方应用须与获取 tmp_external_userid 的应用一致）。
func (c *Client) ConvertTmpExternalUserid(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMiscConvertTmpID, nil, body)
}

// ListMemberOperLogs 获取成员操作记录（时间窗 ≤7 天且 180 天内；仅应用可见范围内操作人；
// 600 次/分；不同过滤条件的 cursor 不可混用）。
func (c *Client) ListMemberOperLogs(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMiscMemberLog, nil, body)
}

// ListAdminOperLogs 获取管理端操作日志（同窗限；默认全量不做应用可见范围筛选；
// 接口自身调用不产生日志）。
func (c *Client) ListAdminOperLogs(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMiscAdminLog, nil, body)
}

// GetScreenOperRecords 查询截屏操作记录（≤14 天窗；userid/部门 ≤100；
// screen_shot_type 1 聊天-6 其他；limit ≤1000；仅自建+截屏管理配置）。
func (c *Client) GetScreenOperRecords(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecScreen, nil, body)
}

// GetFileOperRecords 获取文件操作记录——文件防泄漏（≤14 天窗；operation.type 101 上传-
// 10002 拒绝下载申请；source 401 聊天-411 上下游；记录永久保存）。
func (c *Client) GetFileOperRecords(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecFile, nil, body)
}

// ImportTrustDevices 导入可信企业设备（≤100/次；Windows 须 mac_addr、Mac 须 seq_no；
// 返回 device_code 与重复清单）。
func (c *Client) ImportTrustDevices(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecDevImport, nil, body)
}

// ListTrustDevices 获取设备信息（type 1 可信企业/2 未知/3 可信个人；limit ≤100；
// status 2/4/6 的敏感字段脱敏返回）。
func (c *Client) ListTrustDevices(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecDevList, nil, body)
}

// GetUserTrustDevices 获取成员使用设备（last_login_userid + type）。
func (c *Client) GetUserTrustDevices(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecDevByUser, nil, body)
}

// DeleteTrustDevices 删除设备信息（type + device_code_list ≤100）。
func (c *Client) DeleteTrustDevices(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecDevDelete, nil, body)
}

// ApproveTrustDevices 确认为可信设备（仅 status 3/4 待确认可操作；≤100）。
func (c *Client) ApproveTrustDevices(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecDevApprove, nil, body)
}

// RejectTrustDevices 驳回可信设备申请（仅 status 3/4；返回 success/fail 双列表）。
func (c *Client) RejectTrustDevices(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecDevReject, nil, body)
}

// SubmitSecVIPAddJob 分配安全管理高级功能账号（≤100；异步任务返回 jobid——与会议/邮件
// vip 族不同 path 各自独立；仅自建+「安全与管理」配置）。
func (c *Client) SubmitSecVIPAddJob(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecVipAddJob, nil, body)
}

// GetSecVIPAddJobResult 查询分配结果（jobid）。
func (c *Client) GetSecVIPAddJobResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecVipAddRes, nil, body)
}

// SubmitSecVIPDelJob 取消安全管理高级功能账号（异步任务）。
func (c *Client) SubmitSecVIPDelJob(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecVipDelJob, nil, body)
}

// GetSecVIPDelJobResult 查询取消结果。
func (c *Client) GetSecVIPDelJobResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecVipDelRes, nil, body)
}

// ListSecVIPAccounts 获取安全管理高级功能账号列表（cursor/limit ≤200；has_more 翻页）。
func (c *Client) ListSecVIPAccounts(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epSecVipList, nil, body)
}

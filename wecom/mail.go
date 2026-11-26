package wecom

import (
	"context"
	"net/http"
	"strconv"
)

// 邮件域表项（exmail）：26 端点（官方清点见 change wecom-mail-domain proposal；
// 三个 compose_send 同 path 独立表项，body 结构区分普通/日程/会议）
var (
	epEmailActAccount = Endpoint{"exmail.account.act_email", http.MethodPost, "/cgi-bin/exmail/account/act_email", nil, true}
	epEmailNewCount   = Endpoint{"exmail.mail.get_newcount", http.MethodPost, "/cgi-bin/exmail/mail/get_newcount", nil, true}
	epMailSend        = Endpoint{"exmail.compose_send", http.MethodPost, "/cgi-bin/exmail/app/compose_send", nil, true}
	epMailSendSched   = Endpoint{"exmail.compose_send_schedule", http.MethodPost, "/cgi-bin/exmail/app/compose_send", nil, true}
	epMailSendMeeting = Endpoint{"exmail.compose_send_meeting", http.MethodPost, "/cgi-bin/exmail/app/compose_send", nil, true}
	epMailList        = Endpoint{"exmail.app.get_mail_list", http.MethodPost, "/cgi-bin/exmail/app/get_mail_list", nil, true}
	epMailRead        = Endpoint{"exmail.app.read_mail", http.MethodPost, "/cgi-bin/exmail/app/read_mail", nil, true}
	epEmailAliasUpd   = Endpoint{"exmail.app.update_email_alias", http.MethodPost, "/cgi-bin/exmail/app/update_email_alias", nil, true}
	epEmailAliasGet   = Endpoint{"exmail.app.get_email_alias", http.MethodPost, "/cgi-bin/exmail/app/get_email_alias", nil, false}
	epMailGroupCreate = Endpoint{"exmail.group.create", http.MethodPost, "/cgi-bin/exmail/group/create", nil, true}
	epMailGroupUpdate = Endpoint{"exmail.group.update", http.MethodPost, "/cgi-bin/exmail/group/update", nil, true}
	epMailGroupDelete = Endpoint{"exmail.group.delete", http.MethodPost, "/cgi-bin/exmail/group/delete", nil, true}
	epMailGroupGet    = Endpoint{"exmail.group.get", http.MethodGet, "/cgi-bin/exmail/group/get", []string{"groupid"}, false}
	epMailGroupSearch = Endpoint{"exmail.group.search", http.MethodGet, "/cgi-bin/exmail/group/search", []string{"fuzzy", "groupid"}, false}
	epPubMailCreate   = Endpoint{"exmail.publicmail.create", http.MethodPost, "/cgi-bin/exmail/publicmail/create", nil, true}
	epPubMailUpdate   = Endpoint{"exmail.publicmail.update", http.MethodPost, "/cgi-bin/exmail/publicmail/update", nil, true}
	epPubMailDelete   = Endpoint{"exmail.publicmail.delete", http.MethodPost, "/cgi-bin/exmail/publicmail/delete", nil, true}
	epPubMailGet      = Endpoint{"exmail.publicmail.get", http.MethodPost, "/cgi-bin/exmail/publicmail/get", nil, true}
	epPubMailSearch   = Endpoint{"exmail.publicmail.search", http.MethodGet, "/cgi-bin/exmail/publicmail/search", []string{"fuzzy", "email"}, false}
	epPubMailAuthList = Endpoint{"exmail.publicmail.get_auth_code_list", http.MethodPost, "/cgi-bin/exmail/publicmail/get_auth_code_list", nil, true}
	epPubMailAuthDel  = Endpoint{"exmail.publicmail.delete_auth_code", http.MethodPost, "/cgi-bin/exmail/publicmail/delete_auth_code", nil, true}
	epVipAdd          = Endpoint{"exmail.vip.batch_add", http.MethodPost, "/cgi-bin/exmail/vip/batch_add", nil, true}
	epVipDel          = Endpoint{"exmail.vip.batch_del", http.MethodPost, "/cgi-bin/exmail/vip/batch_del", nil, true}
	epVipList         = Endpoint{"exmail.vip.list", http.MethodPost, "/cgi-bin/exmail/vip/list", nil, true}
	epUserOptGet      = Endpoint{"exmail.useroption.get", http.MethodPost, "/cgi-bin/exmail/useroption/get", nil, true}
	epUserOptUpdate   = Endpoint{"exmail.useroption.update", http.MethodPost, "/cgi-bin/exmail/useroption/update", nil, true}
)

// ActivateEmailAccount 禁用/启用邮箱账号（userid 与 publicemailID 至少一项，同传只操作
// userid；不可操作超管与企业创建人；type 1 启用 / 2 禁用）。
func (c *Client) ActivateEmailAccount(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epEmailActAccount, nil, body)
}

// GetEmailNewCount 获取成员邮箱未读邮件数。
func (c *Client) GetEmailNewCount(ctx context.Context, userid string) (map[string]any, error) {
	return c.invoke(ctx, epEmailNewCount, nil, map[string]any{"userid": userid})
}

// SendMail 发送普通邮件（to.emails/userids 至少一、subject、content 必填；附件 ≤200 个
// 且附件加正文 ≤50M；正文与标题支持 id 转译模板）。
func (c *Client) SendMail(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMailSend, nil, body)
}

// SendScheduleMail 发送日程邮件（compose_send 同 path；schedule.start_time/end_time 必填，
// method cancel 取消须带 schedule_id）。
func (c *Client) SendScheduleMail(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMailSendSched, nil, body)
}

// SendMeetingMail 发送会议邮件（compose_send 同 path；schedule 必填 +
// meeting.meeting_admins 仅可指定 1 人；hosts ≤10、option.password 4-6 位纯数字）。
func (c *Client) SendMeetingMail(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMailSendMeeting, nil, body)
}

// GetMailList 获取应用收件箱邮件列表（begin_time/end_time 必填；cursor/limit ≤1000 翻页，
// 返回 mail_list + next_cursor + has_more）。
func (c *Client) GetMailList(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMailList, nil, body)
}

// ReadMail 获取邮件内容（mail_id 来自 GetMailList；返回 mail_data 为 eml 内容）。
func (c *Client) ReadMail(ctx context.Context, mailID string) (map[string]any, error) {
	return c.invoke(ctx, epMailRead, nil, map[string]any{"mail_id": mailID})
}

// UpdateEmailAlias 更新应用邮箱账号（new_email；原账号转为别名邮箱保留收信）。
func (c *Client) UpdateEmailAlias(ctx context.Context, newEmail string) (map[string]any, error) {
	return c.invoke(ctx, epEmailAliasUpd, nil, map[string]any{"new_email": newEmail})
}

// GetEmailAlias 查询应用邮箱账号（返回主邮箱 email 与别名列表 alias_list）。
func (c *Client) GetEmailAlias(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epEmailAliasGet, nil, nil)
}

// CreateMailGroup 创建邮件群组（groupid/groupname 必填；成员四列表至少一项；
// allow_type 0 企业成员/1 任何人/2 组内成员/3 自定义须 allow_* 至少一项）。
func (c *Client) CreateMailGroup(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMailGroupCreate, nil, body)
}

// UpdateMailGroup 更新邮件群组（groupid 必填；数组字段不传不变传空清空，成员不允许全清空）。
func (c *Client) UpdateMailGroup(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMailGroupUpdate, nil, body)
}

// DeleteMailGroup 删除邮件群组（仅可删除自身应用创建的群组）。
func (c *Client) DeleteMailGroup(ctx context.Context, groupid string) (map[string]any, error) {
	return c.invoke(ctx, epMailGroupDelete, nil, map[string]any{"groupid": groupid})
}

// GetMailGroup 获取邮件群组详情（GET；仅可查询自身应用创建的群组）。
func (c *Client) GetMailGroup(ctx context.Context, groupid string) (map[string]any, error) {
	return c.invoke(ctx, epMailGroupGet, map[string]string{"groupid": groupid}, nil)
}

// SearchMailGroup 模糊搜索邮件群组（fuzzy 1 开启模糊/0 全量；groupid 选填）。
func (c *Client) SearchMailGroup(ctx context.Context, fuzzy int, groupid string) (map[string]any, error) {
	query := map[string]string{"fuzzy": strconv.Itoa(fuzzy)}
	if groupid != "" {
		query["groupid"] = groupid
	}
	return c.invoke(ctx, epMailGroupSearch, query, nil)
}

// CreatePublicMail 创建公共邮箱（email/name 必填；成员三列表至少一项；
// create_auth_code=1 时返回的 auth_code 仅此一次）。
func (c *Client) CreatePublicMail(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPubMailCreate, nil, body)
}

// UpdatePublicMail 更新公共邮箱（id 必填；alias_list ≤5 覆盖式更新；客户端密码上限 10 个）。
func (c *Client) UpdatePublicMail(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epPubMailUpdate, nil, body)
}

// DeletePublicMail 删除公共邮箱（仅可删除自身应用创建的）。
func (c *Client) DeletePublicMail(ctx context.Context, id uint32) (map[string]any, error) {
	return c.invoke(ctx, epPubMailDelete, nil, map[string]any{"id": id})
}

// GetPublicMail 获取公共邮箱详情（POST；id_list 批量）。
func (c *Client) GetPublicMail(ctx context.Context, idList []uint32) (map[string]any, error) {
	return c.invoke(ctx, epPubMailGet, nil, map[string]any{"id_list": idList})
}

// SearchPublicMail 模糊搜索公共邮箱（fuzzy 1 模糊/0 全量；email 选填，可匹配名称或地址）。
func (c *Client) SearchPublicMail(ctx context.Context, fuzzy int, email string) (map[string]any, error) {
	query := map[string]string{"fuzzy": strconv.Itoa(fuzzy)}
	if email != "" {
		query["email"] = email
	}
	return c.invoke(ctx, epPubMailSearch, query, nil)
}

// ListPublicMailAuthCodes 获取公共邮箱客户端专用密码列表（不返回密码本身，
// 密码仅在创建/更新时返回一次；last_use_time 为 0 表示从未使用）。
func (c *Client) ListPublicMailAuthCodes(ctx context.Context, id uint32) (map[string]any, error) {
	return c.invoke(ctx, epPubMailAuthList, nil, map[string]any{"id": id})
}

// DeletePublicMailAuthCode 删除公共邮箱客户端专用密码。
func (c *Client) DeletePublicMailAuthCode(ctx context.Context, id, authCodeID uint32) (map[string]any, error) {
	return c.invoke(ctx, epPubMailAuthDel, nil, map[string]any{"id": id, "auth_code_id": authCodeID})
}

// AddVIPAccounts 分配高级功能账号（userid_list ≤100；返回 succ/fail 双列表）。
func (c *Client) AddVIPAccounts(ctx context.Context, useridList []string) (map[string]any, error) {
	return c.invoke(ctx, epVipAdd, nil, map[string]any{"userid_list": useridList})
}

// RemoveVIPAccounts 取消高级功能账号（userid_list ≤100）。
func (c *Client) RemoveVIPAccounts(ctx context.Context, useridList []string) (map[string]any, error) {
	return c.invoke(ctx, epVipDel, nil, map[string]any{"userid_list": useridList})
}

// ListVIPAccounts 获取高级功能账号列表（cursor/limit ≤200 翻页，以 has_more 判断续拉）。
func (c *Client) ListVIPAccounts(ctx context.Context, cursor string, limit int) (map[string]any, error) {
	body := map[string]any{}
	if cursor != "" {
		body["cursor"] = cursor
	}
	if limit > 0 {
		body["limit"] = limit
	}
	return c.invoke(ctx, epVipList, nil, body)
}

// GetUserOptions 获取用户功能属性（type 数组：1 强制安全登录/2 IMAP/SMTP/3 POP/SMTP/4 安全登录）。
func (c *Client) GetUserOptions(ctx context.Context, userid string, types []int) (map[string]any, error) {
	return c.invoke(ctx, epUserOptGet, nil, map[string]any{"userid": userid, "type": types})
}

// UpdateUserOptions 更改用户功能属性（option.list[{type, value "1"/"0"}]）。
func (c *Client) UpdateUserOptions(ctx context.Context, userid string, optionList []map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epUserOptUpdate, nil, map[string]any{"userid": userid, "option": map[string]any{"list": optionList}})
}

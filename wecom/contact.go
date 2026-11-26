package wecom

import (
	"context"
	"net/http"
	"strconv"
)

// 通讯录管理域表项：成员 15 + 部门 6 + 标签 7（官方文档清点见 change wecom-contact-domain proposal）
var (
	epUserCreate      = Endpoint{"user.create", http.MethodPost, "/cgi-bin/user/create", nil, true}
	epUserGet         = Endpoint{"user.get", http.MethodGet, "/cgi-bin/user/get", []string{"userid"}, false}
	epUserUpdate      = Endpoint{"user.update", http.MethodPost, "/cgi-bin/user/update", nil, true}
	epUserDelete      = Endpoint{"user.delete", http.MethodGet, "/cgi-bin/user/delete", []string{"userid"}, false}
	epUserBatchDelete = Endpoint{"user.batchdelete", http.MethodPost, "/cgi-bin/user/batchdelete", nil, true}
	epUserSimpleList  = Endpoint{"user.simplelist", http.MethodGet, "/cgi-bin/user/simplelist", []string{"department_id", "fetch_child"}, false}
	epUserList        = Endpoint{"user.list", http.MethodGet, "/cgi-bin/user/list", []string{"department_id", "fetch_child"}, false}
	epUserToOpenid    = Endpoint{"user.convert_to_openid", http.MethodPost, "/cgi-bin/user/convert_to_openid", nil, true}
	epUserToUserid    = Endpoint{"user.convert_to_userid", http.MethodPost, "/cgi-bin/user/convert_to_userid", nil, true}
	epUserAuthSucc    = Endpoint{"user.authsucc", http.MethodGet, "/cgi-bin/user/authsucc", []string{"userid"}, false}
	epBatchInvite     = Endpoint{"batch.invite", http.MethodPost, "/cgi-bin/batch/invite", nil, true}
	epCorpJoinQrcode  = Endpoint{"corp.get_join_qrcode", http.MethodGet, "/cgi-bin/corp/get_join_qrcode", []string{"size_type"}, false}
	epUserGetByMobile = Endpoint{"user.getuserid", http.MethodPost, "/cgi-bin/user/getuserid", nil, true}
	epUserGetByEmail  = Endpoint{"user.get_userid_by_email", http.MethodPost, "/cgi-bin/user/get_userid_by_email", nil, true}
	epUserListID      = Endpoint{"user.list_id", http.MethodPost, "/cgi-bin/user/list_id", nil, true}
	epDeptCreate      = Endpoint{"department.create", http.MethodPost, "/cgi-bin/department/create", nil, true}
	epDeptUpdate      = Endpoint{"department.update", http.MethodPost, "/cgi-bin/department/update", nil, true}
	epDeptDelete      = Endpoint{"department.delete", http.MethodGet, "/cgi-bin/department/delete", []string{"id"}, false}
	epDeptList        = Endpoint{"department.list", http.MethodGet, "/cgi-bin/department/list", []string{"id"}, false}
	epDeptSimpleList  = Endpoint{"department.simplelist", http.MethodGet, "/cgi-bin/department/simplelist", []string{"id"}, false}
	epDeptGet         = Endpoint{"department.get", http.MethodGet, "/cgi-bin/department/get", []string{"id"}, false}
	epTagCreate       = Endpoint{"tag.create", http.MethodPost, "/cgi-bin/tag/create", nil, true}
	epTagUpdate       = Endpoint{"tag.update", http.MethodPost, "/cgi-bin/tag/update", nil, true}
	epTagDelete       = Endpoint{"tag.delete", http.MethodGet, "/cgi-bin/tag/delete", []string{"tagid"}, false}
	epTagGet          = Endpoint{"tag.get", http.MethodGet, "/cgi-bin/tag/get", []string{"tagid"}, false}
	epTagAddTagUsers  = Endpoint{"tag.addtagusers", http.MethodPost, "/cgi-bin/tag/addtagusers", nil, true}
	epTagDelTagUsers  = Endpoint{"tag.deltagusers", http.MethodPost, "/cgi-bin/tag/deltagusers", nil, true}
	epTagList         = Endpoint{"tag.list", http.MethodGet, "/cgi-bin/tag/list", nil, false}
)

// CreateUser 创建成员（body 字段见官方文档：userid/name/mobile/department 等）
func (c *Client) CreateUser(ctx context.Context, user map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epUserCreate, nil, user)
}

// GetUser 读取成员
func (c *Client) GetUser(ctx context.Context, userid string) (map[string]any, error) {
	return c.invoke(ctx, epUserGet, map[string]string{"userid": userid}, nil)
}

// UpdateUser 更新成员
func (c *Client) UpdateUser(ctx context.Context, user map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epUserUpdate, nil, user)
}

// DeleteUser 删除成员
func (c *Client) DeleteUser(ctx context.Context, userid string) (map[string]any, error) {
	return c.invoke(ctx, epUserDelete, map[string]string{"userid": userid}, nil)
}

// BatchDeleteUser 批量删除成员（useridlist）
func (c *Client) BatchDeleteUser(ctx context.Context, useridList []string) (map[string]any, error) {
	return c.invoke(ctx, epUserBatchDelete, nil, map[string]any{"useridlist": useridList})
}

// SimpleListUser 获取部门成员（userid/name 摘要）；fetchChild 递归子部门
func (c *Client) SimpleListUser(ctx context.Context, departmentID int, fetchChild bool) (map[string]any, error) {
	return c.invoke(ctx, epUserSimpleList, queryDept(departmentID, fetchChild), nil)
}

// ListUser 获取部门成员详情；fetchChild 递归子部门
func (c *Client) ListUser(ctx context.Context, departmentID int, fetchChild bool) (map[string]any, error) {
	return c.invoke(ctx, epUserList, queryDept(departmentID, fetchChild), nil)
}

// ConvertToOpenid userid 转 openid
func (c *Client) ConvertToOpenid(ctx context.Context, userid string) (map[string]any, error) {
	return c.invoke(ctx, epUserToOpenid, nil, map[string]any{"userid": userid})
}

// ConvertToUserid openid 转 userid
func (c *Client) ConvertToUserid(ctx context.Context, openid string) (map[string]any, error) {
	return c.invoke(ctx, epUserToUserid, nil, map[string]any{"openid": openid})
}

// AuthSucc 登录二次验证（通讯录同步助手）
func (c *Client) AuthSucc(ctx context.Context, userid string) (map[string]any, error) {
	return c.invoke(ctx, epUserAuthSucc, map[string]string{"userid": userid}, nil)
}

// InviteUser 邀请成员（body：user/party/tag 三选一数组）
func (c *Client) InviteUser(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epBatchInvite, nil, body)
}

// GetJoinQrcode 获取加入企业二维码；sizeType 空串时不注入（默认尺寸）
func (c *Client) GetJoinQrcode(ctx context.Context, sizeType string) (map[string]any, error) {
	query := map[string]string{}
	if sizeType != "" {
		query["size_type"] = sizeType
	}
	return c.invoke(ctx, epCorpJoinQrcode, query, nil)
}

// GetUserIdByMobile 手机号获取 userid
func (c *Client) GetUserIdByMobile(ctx context.Context, mobile string) (map[string]any, error) {
	return c.invoke(ctx, epUserGetByMobile, nil, map[string]any{"mobile": mobile})
}

// GetUserIdByEmail 邮箱获取 userid；emailType 1=企业邮箱 2-4=个人邮箱
func (c *Client) GetUserIdByEmail(ctx context.Context, email string, emailType int) (map[string]any, error) {
	return c.invoke(ctx, epUserGetByEmail, nil, map[string]any{"email": email, "email_type": emailType})
}

// ListUserId 获取成员 ID 列表（cursor/limit 分页）；cursor 空串时首页
func (c *Client) ListUserId(ctx context.Context, cursor string, limit int) (map[string]any, error) {
	body := map[string]any{"limit": limit}
	if cursor != "" {
		body["cursor"] = cursor
	}
	return c.invoke(ctx, epUserListID, nil, body)
}

// CreateDepartment 创建部门（body：name/parentid/order 等）
func (c *Client) CreateDepartment(ctx context.Context, dept map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epDeptCreate, nil, dept)
}

// UpdateDepartment 更新部门
func (c *Client) UpdateDepartment(ctx context.Context, dept map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epDeptUpdate, nil, dept)
}

// DeleteDepartment 删除部门
func (c *Client) DeleteDepartment(ctx context.Context, id int) (map[string]any, error) {
	return c.invoke(ctx, epDeptDelete, map[string]string{"id": strconv.Itoa(id)}, nil)
}

// ListDepartment 获取部门列表；id 0 时取全量根架构
func (c *Client) ListDepartment(ctx context.Context, id int) (map[string]any, error) {
	return c.invoke(ctx, epDeptList, queryID(id), nil)
}

// SimpleListDepartment 获取子部门 ID 列表；id 0 时取全量
func (c *Client) SimpleListDepartment(ctx context.Context, id int) (map[string]any, error) {
	return c.invoke(ctx, epDeptSimpleList, queryID(id), nil)
}

// GetDepartment 获取单个部门详情
func (c *Client) GetDepartment(ctx context.Context, id int) (map[string]any, error) {
	return c.invoke(ctx, epDeptGet, map[string]string{"id": strconv.Itoa(id)}, nil)
}

// CreateTag 创建标签；tagID 0 时由企微分配
func (c *Client) CreateTag(ctx context.Context, tagName string, tagID int) (map[string]any, error) {
	body := map[string]any{"tagname": tagName}
	if tagID != 0 {
		body["tagid"] = tagID
	}
	return c.invoke(ctx, epTagCreate, nil, body)
}

// UpdateTag 更新标签名字
func (c *Client) UpdateTag(ctx context.Context, tagID int, tagName string) (map[string]any, error) {
	return c.invoke(ctx, epTagUpdate, nil, map[string]any{"tagid": tagID, "tagname": tagName})
}

// DeleteTag 删除标签
func (c *Client) DeleteTag(ctx context.Context, tagID int) (map[string]any, error) {
	return c.invoke(ctx, epTagDelete, map[string]string{"tagid": strconv.Itoa(tagID)}, nil)
}

// GetTag 获取标签成员
func (c *Client) GetTag(ctx context.Context, tagID int) (map[string]any, error) {
	return c.invoke(ctx, epTagGet, map[string]string{"tagid": strconv.Itoa(tagID)}, nil)
}

// AddTagUsers 增加标签成员（userlist/partylist 至少一项）
func (c *Client) AddTagUsers(ctx context.Context, tagID int, userList []string, partyList []int) (map[string]any, error) {
	return c.invoke(ctx, epTagAddTagUsers, nil, tagUsersBody(tagID, userList, partyList))
}

// DelTagUsers 删除标签成员
func (c *Client) DelTagUsers(ctx context.Context, tagID int, userList []string, partyList []int) (map[string]any, error) {
	return c.invoke(ctx, epTagDelTagUsers, nil, tagUsersBody(tagID, userList, partyList))
}

// ListTag 获取标签列表
func (c *Client) ListTag(ctx context.Context) (map[string]any, error) {
	return c.invoke(ctx, epTagList, nil, nil)
}

func queryDept(departmentID int, fetchChild bool) map[string]string {
	return map[string]string{
		"department_id": strconv.Itoa(departmentID),
		"fetch_child":   boolTo01(fetchChild),
	}
}

func queryID(id int) map[string]string {
	if id == 0 {
		return nil
	}
	return map[string]string{"id": strconv.Itoa(id)}
}

func tagUsersBody(tagID int, userList []string, partyList []int) map[string]any {
	body := map[string]any{"tagid": tagID}
	if len(userList) > 0 {
		body["userlist"] = userList
	}
	if len(partyList) > 0 {
		body["partylist"] = partyList
	}
	return body
}

func boolTo01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

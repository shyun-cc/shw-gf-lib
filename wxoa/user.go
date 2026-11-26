package wxoa

import (
	"context"
	"net/http"
)

// 用户管理表项（标签 8 + 用户 7；官方取证 unionid 页 2026-08-24）
var (
	epTagsCreate   = Endpoint{"user.tag_create", http.MethodPost, "/cgi-bin/tags/create", nil, true}
	epTagsUpdate   = Endpoint{"user.tag_update", http.MethodPost, "/cgi-bin/tags/update", nil, true}
	epTagsDelete   = Endpoint{"user.tag_delete", http.MethodPost, "/cgi-bin/tags/delete", nil, true}
	epTagsGet      = Endpoint{"user.tags_get", http.MethodGet, "/cgi-bin/tags/get", nil, false}
	epTagUsers     = Endpoint{"user.tag_users", http.MethodPost, "/cgi-bin/user/tag/get", nil, true}
	epBatchTagging = Endpoint{"user.batchtagging", http.MethodPost, "/cgi-bin/tags/members/batchtagging", nil, true}
	epBatchUntag   = Endpoint{"user.batchuntagging", http.MethodPost, "/cgi-bin/tags/members/batchuntagging", nil, true}
	epUserTagIDs   = Endpoint{"user.tagids", http.MethodPost, "/cgi-bin/tags/getidlist", nil, true}

	epUserInfo    = Endpoint{"user.info", http.MethodGet, "/cgi-bin/user/info", []string{"openid", "lang"}, false}
	epUserInfoBat = Endpoint{"user.info_batchget", http.MethodPost, "/cgi-bin/user/info/batchget", nil, true}
	epUserList    = Endpoint{"user.list", http.MethodGet, "/cgi-bin/user/get", []string{"next_openid"}, false}
	epBlackAdd    = Endpoint{"user.blacklist_add", http.MethodPost, "/cgi-bin/tags/members/batchblacklist", nil, true}
	epBlackDel    = Endpoint{"user.blacklist_del", http.MethodPost, "/cgi-bin/tags/members/batchunblacklist", nil, true}
	epBlackGet    = Endpoint{"user.blacklist_get", http.MethodPost, "/cgi-bin/tags/members/getblacklist", nil, true}
	epUserRemark  = Endpoint{"user.remark", http.MethodPost, "/cgi-bin/user/info/updateremark", nil, true}
)

// CreateTag 创建标签（name ≤30 字符；最多 100 个）。
func (s *WxOAService) CreateTag(ctx context.Context, name string) (map[string]any, error) {
	return s.oaPost(ctx, epTagsCreate, map[string]any{"tag": map[string]any{"name": name}})
}

// UpdateTag 编辑标签。
func (s *WxOAService) UpdateTag(ctx context.Context, tagID int, name string) (map[string]any, error) {
	return s.oaPost(ctx, epTagsUpdate, map[string]any{"tag": map[string]any{"id": tagID, "name": name}})
}

// DeleteTag 删除标签。
func (s *WxOAService) DeleteTag(ctx context.Context, tagID int) (map[string]any, error) {
	return s.oaPost(ctx, epTagsDelete, map[string]any{"tag": map[string]any{"id": tagID}})
}

// GetTags 获取标签列表（GET）。
func (s *WxOAService) GetTags(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epTagsGet, nil)
}

// GetTagUsers 获取标签下粉丝列表（tagid + next_openid 翻页）。
func (s *WxOAService) GetTagUsers(ctx context.Context, tagID int, nextOpenID string) (map[string]any, error) {
	body := map[string]any{"tagid": tagID}
	if nextOpenID != "" {
		body["next_openid"] = nextOpenID
	}
	return s.oaPost(ctx, epTagUsers, body)
}

// BatchTagging 批量为用户打标签（openid_list ≤50）。
func (s *WxOAService) BatchTagging(ctx context.Context, tagID int, openidList []string) (map[string]any, error) {
	return s.oaPost(ctx, epBatchTagging, map[string]any{"tagid": tagID, "openid_list": openidList})
}

// BatchUntagging 批量为用户取消标签。
func (s *WxOAService) BatchUntagging(ctx context.Context, tagID int, openidList []string) (map[string]any, error) {
	return s.oaPost(ctx, epBatchUntag, map[string]any{"tagid": tagID, "openid_list": openidList})
}

// GetUserTagIDs 获取用户的标签列表。
func (s *WxOAService) GetUserTagIDs(ctx context.Context, openid string) (map[string]any, error) {
	return s.oaPost(ctx, epUserTagIDs, map[string]any{"openid": openid})
}

// GetUserInfo 获取用户基本信息（2021-12-27 后不再输出头像昵称；lang zh_CN 默认）。
func (s *WxOAService) GetUserInfo(ctx context.Context, openid, lang string) (map[string]any, error) {
	if lang == "" {
		lang = "zh_CN"
	}
	return s.oaGet(ctx, epUserInfo, map[string]string{"openid": openid, "lang": lang})
}

// BatchGetUserInfo 批量获取用户基本信息（openid_list ≤100）。
func (s *WxOAService) BatchGetUserInfo(ctx context.Context, openidList []string) (map[string]any, error) {
	list := make([]map[string]any, 0, len(openidList))
	for _, id := range openidList {
		list = append(list, map[string]any{"openid": id, "lang": "zh_CN"})
	}
	return s.oaPost(ctx, epUserInfoBat, map[string]any{"user_list": list})
}

// GetUserList 获取关注用户列表（next_openid 翻页，一次 ≤10000）。
func (s *WxOAService) GetUserList(ctx context.Context, nextOpenID string) (map[string]any, error) {
	extra := map[string]string{}
	if nextOpenID != "" {
		extra["next_openid"] = nextOpenID
	}
	return s.oaGet(ctx, epUserList, extra)
}

// BatchBlacklist 拉黑用户（openid_list ≤20）。
func (s *WxOAService) BatchBlacklist(ctx context.Context, openidList []string) (map[string]any, error) {
	return s.oaPost(ctx, epBlackAdd, map[string]any{"openid_list": openidList})
}

// BatchUnblacklist 取消拉黑。
func (s *WxOAService) BatchUnblacklist(ctx context.Context, openidList []string) (map[string]any, error) {
	return s.oaPost(ctx, epBlackDel, map[string]any{"openid_list": openidList})
}

// GetBlacklist 获取黑名单列表（begin_openid 翻页）。
func (s *WxOAService) GetBlacklist(ctx context.Context, beginOpenID string) (map[string]any, error) {
	body := map[string]any{}
	if beginOpenID != "" {
		body["begin_openid"] = beginOpenID
	}
	return s.oaPost(ctx, epBlackGet, body)
}

// UpdateUserRemark 设置用户备注名（仅认证服务号；remark ≤30 字符）。
func (s *WxOAService) UpdateUserRemark(ctx context.Context, openid, remark string) (map[string]any, error) {
	return s.oaPost(ctx, epUserRemark, map[string]any{"openid": openid, "remark": remark})
}

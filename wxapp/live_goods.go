package wxapp

import (
	"context"
	"net/http"
	"strconv"
)

// 小程序直播·商品库 7 + 成员管理 3 + 长期订阅 2 表项（官方取证见 change wxapp-live proposal）
var (
	epLiveGoodsAdd     = Endpoint{"live.goods_add", http.MethodPost, "/wxaapi/broadcast/goods/add", []string{"access_token"}, true}
	epLiveGoodsAudit   = Endpoint{"live.goods_audit", http.MethodPost, "/wxaapi/broadcast/goods/audit", []string{"access_token"}, true}
	epLiveGoodsWhse    = Endpoint{"live.goods_warehouse", http.MethodPost, "/wxa/business/getgoodswarehouse", []string{"access_token"}, true}
	epLiveGoodsReset   = Endpoint{"live.goods_resetaudit", http.MethodPost, "/wxaapi/broadcast/goods/resetaudit", []string{"access_token"}, true}
	epLiveGoodsUpdate  = Endpoint{"live.goods_update", http.MethodPost, "/wxaapi/broadcast/goods/update", []string{"access_token"}, true}
	epLiveGoodsApprove = Endpoint{"live.goods_approved", http.MethodGet, "/wxaapi/broadcast/goods/getapproved", []string{"access_token", "offset", "limit", "status"}, false}
	epLiveGoodsDelete  = Endpoint{"live.goods_delete", http.MethodPost, "/wxaapi/broadcast/goods/delete", []string{"access_token"}, true}

	epLiveRoleAdd    = Endpoint{"live.role_add", http.MethodPost, "/wxaapi/broadcast/role/addrole", []string{"access_token"}, true}
	epLiveRoleDelete = Endpoint{"live.role_delete", http.MethodPost, "/wxaapi/broadcast/role/deleterole", []string{"access_token"}, true}
	epLiveRoleList   = Endpoint{"live.role_list", http.MethodGet, "/wxaapi/broadcast/role/getrolelist", []string{"access_token", "offset", "limit", "keyword"}, false}

	epLivePushMsg   = Endpoint{"live.sub_push", http.MethodPost, "/wxa/business/push_message", []string{"access_token"}, true}
	epLiveFollowers = Endpoint{"live.followers", http.MethodPost, "/wxa/business/getfollowers", []string{"access_token"}, true}
)

// AddLiveGoods 添加并提审商品（goodsInfo：coverImgUrl ≤300x300、name ≤14 汉字、priceType
// 1 一口价/2 区间/3 折扣、price2 条件必传、url 带参 encode；返回 goodsId+auditId 须保存；
// 与重提审共享日 500 次）。
func (s *WxAppService) AddLiveGoods(ctx context.Context, goodsInfo map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsAdd, map[string]string{"access_token": token}, map[string]any{"goodsInfo": goodsInfo})
}

// ResubmitLiveGoodsAudit 重新提交商品审核（仅可对已撤回提审的商品）。
func (s *WxAppService) ResubmitLiveGoodsAudit(ctx context.Context, goodsID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsAudit, map[string]string{"access_token": token}, map[string]any{"goodsId": goodsID})
}

// GetLiveGoodsAuditInfo 获取商品信息与审核状态（goods_ids ≤20 批量）。
func (s *WxAppService) GetLiveGoodsAuditInfo(ctx context.Context, goodsIDs []int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsWhse, map[string]string{"access_token": token}, map[string]any{"goods_ids": goodsIDs})
}

// ResetLiveGoodsAudit 撤回商品审核（goodsId+auditId；消耗的提审次数不返还）。
func (s *WxAppService) ResetLiveGoodsAudit(ctx context.Context, goodsID, auditID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsReset, map[string]string{"access_token": token}, map[string]any{"goodsId": goodsID, "auditId": auditID})
}

// UpdateLiveGoods 更新商品（审核通过仅可更新价格类型与价格；审核中不可更新；
// 未审核可全量——仅传需更新字段，goodsId 必传）。
func (s *WxAppService) UpdateLiveGoods(ctx context.Context, goodsInfo map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsUpdate, map[string]string{"access_token": token}, map[string]any{"goodsInfo": goodsInfo})
}

// ListApprovedLiveGoods 获取商品列表（GET；offset+status 必填、limit ≤100 默认 30；
// status 0 未审核/1 审核中/2 通过/3 驳回）。
func (s *WxAppService) ListApprovedLiveGoods(ctx context.Context, offset, limit, status int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsApprove, map[string]string{
		"access_token": token, "offset": strconv.Itoa(offset), "limit": strconv.Itoa(limit), "status": strconv.Itoa(status),
	}, nil)
}

// DeleteLiveGoods 删除商品（直播间已上架的同商品同步删除，不可恢复；日 1000 次）。
func (s *WxAppService) DeleteLiveGoods(ctx context.Context, goodsID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsDelete, map[string]string{"access_token": token}, map[string]any{"goodsId": goodsID})
}

// AddLiveRole 设置成员角色（role 1 管理员 ≤10/2 主播 ≤500/3 运营者 ≤500；超管设置无效；
// 主播未实名返回 codeurl 供先验证）。
func (s *WxAppService) AddLiveRole(ctx context.Context, username string, role int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveRoleAdd, map[string]string{"access_token": token}, map[string]any{"username": username, "role": role})
}

// DeleteLiveRole 移除成员角色（有未开播直播间的主播不可删除）。
func (s *WxAppService) DeleteLiveRole(ctx context.Context, username string, role int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveRoleDelete, map[string]string{"access_token": token}, map[string]any{"username": username, "role": role})
}

// ListLiveRoles 查询成员列表（GET；keyword 微信号/昵称模糊、limit ≤30）。
func (s *WxAppService) ListLiveRoles(ctx context.Context, offset, limit int, keyword string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]string{"access_token": token, "offset": strconv.Itoa(offset)}
	if limit > 0 {
		query["limit"] = strconv.Itoa(limit)
	}
	if keyword != "" {
		query["keyword"] = keyword
	}
	return s.invoke(ctx, epLiveRoleList, query, nil)
}

// PushLiveStartMessage 向长期订阅用户群发直播开始事件（user_openid ≤2000、日 5000 次；
// 返回 message_id 对应群发结果回调）。
func (s *WxAppService) PushLiveStartMessage(ctx context.Context, roomID int, userOpenIDs []string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLivePushMsg, map[string]string{"access_token": token}, map[string]any{"room_id": roomID, "user_openid": userOpenIDs})
}

// GetLiveFollowers 获取长期订阅用户（limit ≤2000 默认 200；page_break 翻页标记透传）。
func (s *WxAppService) GetLiveFollowers(ctx context.Context, limit int, pageBreak string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if limit > 0 {
		body["limit"] = limit
	}
	if pageBreak != "" {
		body["page_break"] = pageBreak
	}
	return s.invoke(ctx, epLiveFollowers, map[string]string{"access_token": token}, body)
}

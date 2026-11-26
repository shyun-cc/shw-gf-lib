package wxapp

import (
	"context"
	"net/http"
	"strconv"
)

// 小程序直播·直播间管理表项（官方取证见 change wxapp-live proposal；官方标 GET 但参数
// 写 Body 的四件按 GET+query 实现——微信 GET 接口惯例，proposal 留痕裁量）
var (
	epLiveRoomCreate     = Endpoint{"live.room_create", http.MethodPost, "/wxaapi/broadcast/room/create", []string{"access_token"}, true}
	epLiveRoomEdit       = Endpoint{"live.room_edit", http.MethodPost, "/wxaapi/broadcast/room/editroom", []string{"access_token"}, true}
	epLiveRoomDelete     = Endpoint{"live.room_delete", http.MethodPost, "/wxaapi/broadcast/room/deleteroom", []string{"access_token"}, true}
	epLiveInfo           = Endpoint{"live.getliveinfo", http.MethodPost, "/wxa/business/getliveinfo", []string{"access_token"}, true}
	epLivePushURL        = Endpoint{"live.push_url", http.MethodGet, "/wxaapi/broadcast/room/getpushurl", []string{"access_token", "roomId"}, false}
	epLiveSharedCode     = Endpoint{"live.shared_code", http.MethodGet, "/wxaapi/broadcast/room/getsharedcode", []string{"access_token", "roomId", "params"}, false}
	epLiveSubAnchorGet   = Endpoint{"live.subanchor_get", http.MethodGet, "/wxaapi/broadcast/room/getsubanchor", []string{"access_token", "roomId"}, false}
	epLiveSubAnchorMod   = Endpoint{"live.subanchor_modify", http.MethodPost, "/wxaapi/broadcast/room/modifysubanchor", []string{"access_token"}, true}
	epLiveSubAnchorDel   = Endpoint{"live.subanchor_delete", http.MethodPost, "/wxaapi/broadcast/room/deletesubanchor", []string{"access_token"}, true}
	epLiveSubAnchorAdd   = Endpoint{"live.subanchor_add", http.MethodPost, "/wxaapi/broadcast/room/addsubanchor", []string{"access_token"}, true}
	epLiveGoodsImport    = Endpoint{"live.goods_import", http.MethodPost, "/wxaapi/broadcast/room/addgoods", []string{"access_token"}, true}
	epLiveGoodsInRoomDel = Endpoint{"live.goods_inroom_delete", http.MethodPost, "/wxaapi/broadcast/goods/deleteInRoom", []string{"access_token"}, true}
	epLiveGoodsPush      = Endpoint{"live.goods_push", http.MethodPost, "/wxaapi/broadcast/goods/push", []string{"access_token"}, true}
	epLiveGoodsOnSale    = Endpoint{"live.goods_onsale", http.MethodPost, "/wxaapi/broadcast/goods/onsale", []string{"access_token"}, true}
	epLiveGoodsSort      = Endpoint{"live.goods_sort", http.MethodPost, "/wxaapi/broadcast/goods/sort", []string{"access_token"}, true}
	epLiveAstAdd         = Endpoint{"live.assistant_add", http.MethodPost, "/wxaapi/broadcast/room/addassistant", []string{"access_token"}, true}
	epLiveAstModify      = Endpoint{"live.assistant_modify", http.MethodPost, "/wxaapi/broadcast/room/modifyassistant", []string{"access_token"}, true}
	epLiveAstRemove      = Endpoint{"live.assistant_remove", http.MethodPost, "/wxaapi/broadcast/room/removeassistant", []string{"access_token"}, true}
	epLiveAstList        = Endpoint{"live.assistant_list", http.MethodGet, "/wxaapi/broadcast/room/getassistantlist", []string{"access_token", "roomId"}, false}
	epLiveComment        = Endpoint{"live.comment", http.MethodPost, "/wxaapi/broadcast/room/updatecomment", []string{"access_token"}, true}
	epLiveFeedPublic     = Endpoint{"live.feedpublic", http.MethodPost, "/wxaapi/broadcast/room/updatefeedpublic", []string{"access_token"}, true}
	epLiveKF             = Endpoint{"live.kf", http.MethodPost, "/wxaapi/broadcast/room/updatekf", []string{"access_token"}, true}
	epLiveReplay         = Endpoint{"live.replay", http.MethodPost, "/wxaapi/broadcast/room/updatereplay", []string{"access_token"}, true}
	epLiveGoodsVideo     = Endpoint{"live.goods_video", http.MethodPost, "/wxaapi/broadcast/goods/getVideo", []string{"access_token"}, true}
	epLiveGoodsKeySet    = Endpoint{"live.goodskey_set", http.MethodPost, "/wxaapi/broadcast/goods/setkey", []string{"access_token"}, true}
	epLiveGoodsKeyGet    = Endpoint{"live.goodskey_get", http.MethodGet, "/wxaapi/broadcast/goods/getkey", []string{"access_token"}, false}
)

// liveQuery 组 access_token+数字 roomId 的 GET query。
func liveQuery(token string, roomID int) map[string]string {
	return map[string]string{"access_token": token, "roomId": strconv.Itoa(roomID)}
}

// CreateLiveRoom 创建直播间（13 项必填：名称/图 mediaID/时间窗 10min-6mo/主播/三开关等；
// 返回 roomId 与 qrcode_url——主播未实名也会返回二维码供先验证）。
func (s *WxAppService) CreateLiveRoom(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveRoomCreate, map[string]string{"access_token": token}, body)
}

// EditLiveRoom 编辑直播间（id + 同创建必填族）。
func (s *WxAppService) EditLiveRoom(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveRoomEdit, map[string]string{"access_token": token}, body)
}

// DeleteLiveRoom 删除直播间（id 房间号）。
func (s *WxAppService) DeleteLiveRoom(ctx context.Context, id int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveRoomDelete, map[string]string{"access_token": token}, map[string]any{"id": id})
}

// GetLiveInfo 获取直播间列表和回放（action=get_replay 时 room_id 必填；回放源直播结束
// 10 分钟内生成；日 10 万次）。
func (s *WxAppService) GetLiveInfo(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveInfo, map[string]string{"access_token": token}, body)
}

// GetLivePushURL 获取直播间推流地址（rtmp）。
func (s *WxAppService) GetLivePushURL(ctx context.Context, roomID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLivePushURL, liveQuery(token, roomID), nil)
}

// GetLiveSharedCode 获取直播间分享二维码（params 自定义参数选填；返回 cdnUrl/pagePath/posterUrl）。
func (s *WxAppService) GetLiveSharedCode(ctx context.Context, roomID int, params string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := liveQuery(token, roomID)
	if params != "" {
		query["params"] = params
	}
	return s.invoke(ctx, epLiveSharedCode, query, nil)
}

// GetLiveSubAnchor 获取主播副号。
func (s *WxAppService) GetLiveSubAnchor(ctx context.Context, roomID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveSubAnchorGet, liveQuery(token, roomID), nil)
}

// ModifyLiveSubAnchor 修改主播副号（副号不能与主号重复）。
func (s *WxAppService) ModifyLiveSubAnchor(ctx context.Context, roomID int, username string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveSubAnchorMod, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "username": username})
}

// DeleteLiveSubAnchor 删除主播副号。
func (s *WxAppService) DeleteLiveSubAnchor(ctx context.Context, roomID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveSubAnchorDel, map[string]string{"access_token": token}, map[string]any{"roomId": roomID})
}

// AddLiveSubAnchor 添加主播副号（不能与主号重复、不可重复设置同一副号）。
func (s *WxAppService) AddLiveSubAnchor(ctx context.Context, roomID int, username string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveSubAnchorAdd, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "username": username})
}

// ImportLiveGoods 导入商品到直播间（ids 批量）。
func (s *WxAppService) ImportLiveGoods(ctx context.Context, roomID int, ids []int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsImport, map[string]string{"access_token": token}, map[string]any{"ids": ids, "roomId": roomID})
}

// DeleteLiveRoomGoods 删除直播间商品（已有商品推送中时稍后再试）。
func (s *WxAppService) DeleteLiveRoomGoods(ctx context.Context, roomID, goodsID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsInRoomDel, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "goodsId": goodsID})
}

// PushLiveGoods 推送商品到直播间（下架商品无法推送）。
func (s *WxAppService) PushLiveGoods(ctx context.Context, roomID, goodsID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsPush, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "goodsId": goodsID})
}

// OnSaleLiveGoods 上下架直播间商品（onSale 0 下架/1 上架；推送中不允许操作）。
func (s *WxAppService) OnSaleLiveGoods(ctx context.Context, roomID, goodsID, onSale int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsOnSale, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "goodsId": goodsID, "onSale": onSale})
}

// SortLiveGoods 直播间商品排序（goods 数组顺序即展示顺序）。
func (s *WxAppService) SortLiveGoods(ctx context.Context, roomID int, goods []map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsSort, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "goods": goods})
}

// AddLiveAssistant 添加直播间小助手（users[{username,nickname}]）。
func (s *WxAppService) AddLiveAssistant(ctx context.Context, roomID int, users []map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveAstAdd, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "users": users})
}

// ModifyLiveAssistant 修改直播间小助手昵称。
func (s *WxAppService) ModifyLiveAssistant(ctx context.Context, roomID int, username, nickname string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveAstModify, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "username": username, "nickname": nickname})
}

// RemoveLiveAssistant 删除直播间小助手。
func (s *WxAppService) RemoveLiveAssistant(ctx context.Context, roomID int, username string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveAstRemove, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "username": username})
}

// GetLiveAssistantList 查询直播间小助手列表（GET）。
func (s *WxAppService) GetLiveAssistantList(ctx context.Context, roomID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveAstList, liveQuery(token, roomID), nil)
}

// UpdateLiveComment 禁言管理（banComment 1 全局禁言/0 取消）。
func (s *WxAppService) UpdateLiveComment(ctx context.Context, roomID, banComment int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveComment, map[string]string{"access_token": token}, map[string]any{"id": roomID, "banComment": banComment})
}

// UpdateLiveFeedPublic 官方收录管理（isFeedsPublic 1 开/0 关）。
func (s *WxAppService) UpdateLiveFeedPublic(ctx context.Context, roomID, isFeedsPublic int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveFeedPublic, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "isFeedsPublic": isFeedsPublic})
}

// UpdateLiveKF 客服功能管理（closeKf 0 开/1 关）。
func (s *WxAppService) UpdateLiveKF(ctx context.Context, roomID, closeKf int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveKF, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "closeKf": closeKf})
}

// UpdateLiveReplay 回放功能管理（closeReplay 0 开/1 关）。
func (s *WxAppService) UpdateLiveReplay(ctx context.Context, roomID, closeReplay int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveReplay, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "closeReplay": closeReplay})
}

// GetLiveGoodsVideo 下载商品讲解视频（返回 url；未生成/无讲解视频有专属错误码）。
func (s *WxAppService) GetLiveGoodsVideo(ctx context.Context, roomID, goodsID int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsVideo, map[string]string{"access_token": token}, map[string]any{"roomId": roomID, "goodsId": goodsID})
}

// SetLiveGoodsKey 直播挂件设置全局 KEY（goodsKey 字符串数组；建议只设一次——重设会使
// 未显式指定 goodsKey 的既有商品映射失效；日 500 次）。
func (s *WxAppService) SetLiveGoodsKey(ctx context.Context, goodsKey []string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsKeySet, map[string]string{"access_token": token}, map[string]any{"goodsKey": goodsKey})
}

// GetLiveGoodsKey 直播挂件获取全局 KEY（无参；返回 vendorGoodsKey）。
func (s *WxAppService) GetLiveGoodsKey(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epLiveGoodsKeyGet, map[string]string{"access_token": token}, nil)
}

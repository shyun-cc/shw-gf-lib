package wxapp

import (
	"context"
	"net/http"
	"strconv"
)

// 交易保障表项（基础能力 + 交易评价管理；官方取证见 change wxapp-trade proposal；
// GET 件官方多标 GET 带 Body，按 GET+query 实现留痕）
var (
	epTGPenaltyList = Endpoint{"tg.penalty_list", http.MethodGet, "/wxaapi/wxamptrade/get_penalty_list", []string{"access_token", "offset", "limit"}, false}
	epTGGuarantee   = Endpoint{"tg.guarantee", http.MethodGet, "/wxaapi/wxamptrade/get_guarantee_status", []string{"access_token"}, false}
	epTGCommentList = Endpoint{"tg.comment_list", http.MethodGet, "/wxaapi/comment/mpcommentlist/get", []string{"access_token", "startTime", "endTime", "filterType", "offset", "limit"}, false}
	epTGReplyList   = Endpoint{"tg.reply_list", http.MethodGet, "/wxaapi/comment/replyandcommentreplylist/get", []string{"access_token", "commentId"}, false}
	epTGCommentInfo = Endpoint{"tg.comment_info", http.MethodGet, "/wxaapi/comment/commentinfo/get", []string{"access_token", "commentId"}, false}
	epTGReplyAdd    = Endpoint{"tg.reply_add", http.MethodPost, "/wxaapi/comment/reply/add", []string{"access_token"}, true}
	epTGReplyDel    = Endpoint{"tg.reply_delete", http.MethodPost, "/wxaapi/comment/reply/delete", []string{"access_token"}, true}
	epTGCReplyAdd   = Endpoint{"tg.creply_add", http.MethodPost, "/wxaapi/comment/commentreply/add", []string{"access_token"}, true}
	epTGCReplyDel   = Endpoint{"tg.creply_delete", http.MethodPost, "/wxaapi/comment/commentreply/delete", []string{"access_token"}, true}
	epTGKfQuota     = Endpoint{"tg.kfquota_reset", http.MethodPost, "/wxaapi/comment/apikfquota/reset", []string{"access_token"}, true}
	epTGCompromise  = Endpoint{"tg.compromise", http.MethodPost, "/wxaapi/comment/confirmcompromise", []string{"access_token"}, true}
)

// GetPenaltyList 获取交易体验分违规记录（status 2 扣分通过/4 申诉中/5 驳回/6 成功；
// 返回 currentScore 当前体验分）。
func (s *WxAppService) GetPenaltyList(ctx context.Context, offset, limit int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGPenaltyList, map[string]string{
		"access_token": token, "offset": strconv.Itoa(offset), "limit": strconv.Itoa(limit),
	}, nil)
}

// GetGuaranteeStatus 获取交易保障标状态（isActived + 未开通原因 reasons）。
func (s *WxAppService) GetGuaranteeStatus(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGGuarantee, map[string]string{"access_token": token}, nil)
}

// GetCommentList 查询评价列表（filterType 1 差评/2 好评/3 差评待处理/4 待回复/5 已改评/
// 6 全部含中评；score 每 100 分对应 1 星）。
func (s *WxAppService) GetCommentList(ctx context.Context, startTime, endTime string, filterType, offset, limit int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGCommentList, map[string]string{
		"access_token": token, "startTime": startTime, "endTime": endTime,
		"filterType": strconv.Itoa(filterType), "offset": strconv.Itoa(offset), "limit": strconv.Itoa(limit),
	}, nil)
}

// GetReplyList 查询评论列表（开发者创建的第一条回复即评论，其后均是对它的回复）。
func (s *WxAppService) GetReplyList(ctx context.Context, commentID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGReplyList, map[string]string{"access_token": token, "commentId": commentID}, nil)
}

// GetCommentInfo 查询评价详情。
func (s *WxAppService) GetCommentInfo(ctx context.Context, commentID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGCommentInfo, map[string]string{"access_token": token, "commentId": commentID}, nil)
}

// AddReply 创建评论（开发者对评价的第一条回复即评论）。
func (s *WxAppService) AddReply(ctx context.Context, commentID, content string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGReplyAdd, map[string]string{"access_token": token}, map[string]string{"commentId": commentID, "content": content})
}

// DeleteReply 删除评论（删除评论会连带删除基于它的全部回复）。
func (s *WxAppService) DeleteReply(ctx context.Context, commentID, replyID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGReplyDel, map[string]string{"access_token": token}, map[string]string{"commentId": commentID, "replyId": replyID})
}

// AddCommentReply 创建回复（对评论的回复）。
func (s *WxAppService) AddCommentReply(ctx context.Context, commentID, replyID, content string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGCReplyAdd, map[string]string{"access_token": token}, map[string]string{
		"commentId": commentID, "replyId": replyID, "content": content,
	})
}

// DeleteCommentReply 删除回复。
func (s *WxAppService) DeleteCommentReply(ctx context.Context, commentID, replyID, commentReplyID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGCReplyDel, map[string]string{"access_token": token}, map[string]string{
		"commentId": commentID, "replyId": replyID, "commentReplyId": commentReplyID,
	})
}

// ResetApiKfQuota 重置 API 客服 quota（差评客服会话配额，按评价维度）。
func (s *WxAppService) ResetApiKfQuota(ctx context.Context, commentID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGKfQuota, map[string]string{"access_token": token}, map[string]string{"commentId": commentID})
}

// ConfirmCompromise 确认和解（仅差评可和解；picList 为和解图片 mediaId 集合）。
func (s *WxAppService) ConfirmCompromise(ctx context.Context, commentID string, picList []string, content string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epTGCompromise, map[string]string{"access_token": token}, map[string]any{
		"commentId": commentID, "picList": picList, "content": content,
	})
}

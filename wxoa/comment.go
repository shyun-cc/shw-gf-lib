package wxoa

import (
	"context"
	"net/http"
)

// 留言管理 + 发布能力 + 草稿箱表项（官方取证 comments/publish/draft 三页 2026-08-24）
var (
	epCommentOpen     = Endpoint{"comment.open", http.MethodPost, "/cgi-bin/comment/open", nil, true}
	epCommentList     = Endpoint{"comment.list", http.MethodPost, "/cgi-bin/comment/list", nil, true}
	epCommentClose    = Endpoint{"comment.close", http.MethodPost, "/cgi-bin/comment/close", nil, true}
	epCommentElect    = Endpoint{"comment.markelect", http.MethodPost, "/cgi-bin/comment/markelect", nil, true}
	epCommentUnelect  = Endpoint{"comment.unmarkelect", http.MethodPost, "/cgi-bin/comment/unmarkelect", nil, true}
	epCommentDelete   = Endpoint{"comment.delete", http.MethodPost, "/cgi-bin/comment/delete", nil, true}
	epCommentReplyAdd = Endpoint{"comment.reply_add", http.MethodPost, "/cgi-bin/comment/reply/add", nil, true}
	epCommentReplyDel = Endpoint{"comment.reply_delete", http.MethodPost, "/cgi-bin/comment/reply/delete", nil, true}

	epPublishSubmit  = Endpoint{"publish.submit", http.MethodPost, "/cgi-bin/freepublish/submit", nil, true}
	epPublishStatus  = Endpoint{"publish.get", http.MethodPost, "/cgi-bin/freepublish/get", nil, true}
	epPublishBatch   = Endpoint{"publish.batchget", http.MethodPost, "/cgi-bin/freepublish/batchget", nil, true}
	epPublishArticle = Endpoint{"publish.getarticle", http.MethodPost, "/cgi-bin/freepublish/getarticle", nil, true}
	epPublishDelete  = Endpoint{"publish.delete", http.MethodPost, "/cgi-bin/freepublish/delete", nil, true}

	epDraftSwitch = Endpoint{"draft.switch", http.MethodPost, "/cgi-bin/draft/switch", nil, true}
	epDraftAdd    = Endpoint{"draft.add", http.MethodPost, "/cgi-bin/draft/add", nil, true}
	epDraftGet    = Endpoint{"draft.get", http.MethodPost, "/cgi-bin/draft/get", nil, true}
	epDraftBatch  = Endpoint{"draft.batchget", http.MethodPost, "/cgi-bin/draft/batchget", nil, true}
	epDraftCount  = Endpoint{"draft.count", http.MethodGet, "/cgi-bin/draft/count", nil, false}
	epDraftDelete = Endpoint{"draft.delete", http.MethodPost, "/cgi-bin/draft/delete", nil, true}
	epDraftUpdate = Endpoint{"draft.update", http.MethodPost, "/cgi-bin/draft/update", nil, true}
)

// OpenComment 打开已群发文章评论（msg_data_id + index 多图文位）。
func (s *WxOAService) OpenComment(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epCommentOpen, body)
}

// ListComments 查看指定文章的评论数据（begin/count 分页 + type 0 全部/1 精选）。
func (s *WxOAService) ListComments(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epCommentList, body)
}

// CloseComment 关闭已群发文章评论。
func (s *WxOAService) CloseComment(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epCommentClose, body)
}

// MarkElect 评论标记精选。
func (s *WxOAService) MarkElect(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epCommentElect, body)
}

// UnmarkElect 评论取消精选。
func (s *WxOAService) UnmarkElect(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epCommentUnelect, body)
}

// DeleteComment 删除评论。
func (s *WxOAService) DeleteComment(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epCommentDelete, body)
}

// AddCommentReply 回复评论（content ≤300 字）。
func (s *WxOAService) AddCommentReply(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epCommentReplyAdd, body)
}

// DeleteCommentReply 删除回复。
func (s *WxOAService) DeleteCommentReply(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epCommentReplyDel, body)
}

// SubmitPublish 发布草稿（返回 publish_id；2025-07 起个人/未认证账号权限回收）。
func (s *WxOAService) SubmitPublish(ctx context.Context, mediaID string) (map[string]any, error) {
	return s.oaPost(ctx, epPublishSubmit, map[string]any{"media_id": mediaID})
}

// GetPublishStatus 发布状态轮询（publish_id；发布完成返回 article_id 与 article_detail）。
func (s *WxOAService) GetPublishStatus(ctx context.Context, publishID string) (map[string]any, error) {
	return s.oaPost(ctx, epPublishStatus, map[string]any{"publish_id": publishID})
}

// BatchGetPublished 获取已发布消息列表（offset/count ≤20 翻页；仅已发布成功）。
func (s *WxOAService) BatchGetPublished(ctx context.Context, offset, count int) (map[string]any, error) {
	return s.oaPost(ctx, epPublishBatch, map[string]any{"offset": offset, "count": count})
}

// GetPublishedArticle 获取已发布图文信息（article_id 或 publish+index 二选一）。
func (s *WxOAService) GetPublishedArticle(ctx context.Context, body map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epPublishArticle, body)
}

// DeletePublish 删除发布文章（仅「已发布」状态可删；不可逆。articleID 为发布返回的
// article_id 字符串；index 为多图文内文章位，0 表示删除全部）。
func (s *WxOAService) DeletePublish(ctx context.Context, articleID string, index int) (map[string]any, error) {
	return s.oaPost(ctx, epPublishDelete, map[string]any{"article_id": articleID, "index": index})
}

// SetDraftSwitch 草稿箱开关设置（check_only 校验模式）。
func (s *WxOAService) SetDraftSwitch(ctx context.Context, checkOnly bool) (map[string]any, error) {
	return s.oaPost(ctx, epDraftSwitch, map[string]any{"check_only": checkOnly})
}

// AddDraft 新增草稿（articles 数组 ≤8 篇图文）。
func (s *WxOAService) AddDraft(ctx context.Context, articles []map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epDraftAdd, map[string]any{"articles": articles})
}

// GetDraft 获取草稿详情（media_id）。
func (s *WxOAService) GetDraft(ctx context.Context, mediaID string) (map[string]any, error) {
	return s.oaPost(ctx, epDraftGet, map[string]any{"media_id": mediaID})
}

// BatchGetDrafts 获取草稿列表（offset/count ≤20；no_content 免正文加速）。
func (s *WxOAService) BatchGetDrafts(ctx context.Context, offset, count int, noContent bool) (map[string]any, error) {
	body := map[string]any{"offset": offset, "count": count}
	if noContent {
		body["no_content"] = 1
	}
	return s.oaPost(ctx, epDraftBatch, body)
}

// GetDraftCount 获取草稿总数（GET）。
func (s *WxOAService) GetDraftCount(ctx context.Context) (map[string]any, error) {
	return s.oaGet(ctx, epDraftCount, nil)
}

// DeleteDraft 删除草稿。
func (s *WxOAService) DeleteDraft(ctx context.Context, mediaID string) (map[string]any, error) {
	return s.oaPost(ctx, epDraftDelete, map[string]any{"media_id": mediaID})
}

// UpdateDraft 更新草稿（media_id + articles 全量替换指定位）。
func (s *WxOAService) UpdateDraft(ctx context.Context, mediaID string, index int, articles map[string]any) (map[string]any, error) {
	return s.oaPost(ctx, epDraftUpdate, map[string]any{
		"media_id": mediaID, "index": index, "articles": articles,
	})
}

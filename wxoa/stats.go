package wxoa

import (
	"context"
	"net/http"
)

// 数据统计表项（官方 datacube 页取证 2026-08-24；21 件统一日期窗 body yyyymmdd）
var (
	epStatsUsersummary          = Endpoint{"stats.usersummary", http.MethodPost, "/datacube/getusersummary", nil, true}
	epStatsUsercumulate         = Endpoint{"stats.usercumulate", http.MethodPost, "/datacube/getusercumulate", nil, true}
	epStatsArticlesummary       = Endpoint{"stats.articlesummary", http.MethodPost, "/datacube/getarticlesummary", nil, true}
	epStatsUserread             = Endpoint{"stats.userread", http.MethodPost, "/datacube/getuserread", nil, true}
	epStatsUserreadhour         = Endpoint{"stats.userreadhour", http.MethodPost, "/datacube/getuserreadhour", nil, true}
	epStatsArticletotal         = Endpoint{"stats.articletotal", http.MethodPost, "/datacube/getarticletotal", nil, true}
	epStatsUsershare            = Endpoint{"stats.usershare", http.MethodPost, "/datacube/getusershare", nil, true}
	epStatsUsersharehour        = Endpoint{"stats.usersharehour", http.MethodPost, "/datacube/getusersharehour", nil, true}
	epStatsArticleread          = Endpoint{"stats.articleread", http.MethodPost, "/datacube/getarticleread", nil, true}
	epStatsArticleshare         = Endpoint{"stats.articleshare", http.MethodPost, "/datacube/getarticleshare", nil, true}
	epStatsBizsummary           = Endpoint{"stats.bizsummary", http.MethodPost, "/datacube/getbizsummary", nil, true}
	epStatsArticletotaldetail   = Endpoint{"stats.articletotaldetail", http.MethodPost, "/datacube/getarticletotaldetail", nil, true}
	epStatsUpstreammsg          = Endpoint{"stats.upstreammsg", http.MethodPost, "/datacube/getupstreammsg", nil, true}
	epStatsUpstreammsghour      = Endpoint{"stats.upstreammsghour", http.MethodPost, "/datacube/getupstreammsghour", nil, true}
	epStatsUpstreammsgweek      = Endpoint{"stats.upstreammsgweek", http.MethodPost, "/datacube/getupstreammsgweek", nil, true}
	epStatsUpstreammsgmonth     = Endpoint{"stats.upstreammsgmonth", http.MethodPost, "/datacube/getupstreammsgmonth", nil, true}
	epStatsUpstreammsgdist      = Endpoint{"stats.upstreammsgdist", http.MethodPost, "/datacube/getupstreammsgdist", nil, true}
	epStatsUpstreammsgdistweek  = Endpoint{"stats.upstreammsgdistweek", http.MethodPost, "/datacube/getupstreammsgdistweek", nil, true}
	epStatsUpstreammsgdistmonth = Endpoint{"stats.upstreammsgdistmonth", http.MethodPost, "/datacube/getupstreammsgdistmonth", nil, true}
	epStatsInterfacesummary     = Endpoint{"stats.interfacesummary", http.MethodPost, "/datacube/getinterfacesummary", nil, true}
	epStatsInterfacesummaryhour = Endpoint{"stats.interfacesummaryhour", http.MethodPost, "/datacube/getinterfacesummaryhour", nil, true}
)

// statsRange 数据统计统一调用（begin/end 日期 yyyymmdd；各接口时间跨度限制见官方详情页）。
func (s *WxOAService) statsRange(ctx context.Context, ep Endpoint, beginDate, endDate string) (map[string]any, error) {
	return s.oaPost(ctx, ep, map[string]any{"begin_date": beginDate, "end_date": endDate})
}

// GetUserSummary 获取用户增减数据。
func (s *WxOAService) GetUserSummary(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUsersummary, beginDate, endDate)
}

// GetUserCumulate 获取累计用户数据。
func (s *WxOAService) GetUserCumulate(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUsercumulate, beginDate, endDate)
}

// GetArticleSummary 获取图文群发每日数据。
func (s *WxOAService) GetArticleSummary(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsArticlesummary, beginDate, endDate)
}

// GetUserRead 获取图文阅读概况数据。
func (s *WxOAService) GetUserRead(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUserread, beginDate, endDate)
}

// GetUserReadHour 获取图文阅读分时数据。
func (s *WxOAService) GetUserReadHour(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUserreadhour, beginDate, endDate)
}

// GetArticleTotal 获取图文群发总数据。
func (s *WxOAService) GetArticleTotal(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsArticletotal, beginDate, endDate)
}

// GetUserShare 获取图文转发概况数据。
func (s *WxOAService) GetUserShare(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUsershare, beginDate, endDate)
}

// GetUserShareHour 获取图文转发分时数据。
func (s *WxOAService) GetUserShareHour(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUsersharehour, beginDate, endDate)
}

// GetArticleRead 获取发表内容每日阅读数据。
func (s *WxOAService) GetArticleRead(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsArticleread, beginDate, endDate)
}

// GetArticleShare 获取发表内容每日分享数据。
func (s *WxOAService) GetArticleShare(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsArticleshare, beginDate, endDate)
}

// GetBizSummary 获取发表内容概况总数据。
func (s *WxOAService) GetBizSummary(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsBizsummary, beginDate, endDate)
}

// GetArticleTotalDetail 获取发表内容发表详细数据。
func (s *WxOAService) GetArticleTotalDetail(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsArticletotaldetail, beginDate, endDate)
}

// GetUpstreamMsg 获取消息发送概况数据。
func (s *WxOAService) GetUpstreamMsg(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUpstreammsg, beginDate, endDate)
}

// GetUpstreamMsgHour 获取消息发送分时数据。
func (s *WxOAService) GetUpstreamMsgHour(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUpstreammsghour, beginDate, endDate)
}

// GetUpstreamMsgWeek 获取消息发送周数据。
func (s *WxOAService) GetUpstreamMsgWeek(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUpstreammsgweek, beginDate, endDate)
}

// GetUpstreamMsgMonth 获取消息发送月数据。
func (s *WxOAService) GetUpstreamMsgMonth(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUpstreammsgmonth, beginDate, endDate)
}

// GetUpstreamMsgDist 获取消息发送分布数据。
func (s *WxOAService) GetUpstreamMsgDist(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUpstreammsgdist, beginDate, endDate)
}

// GetUpstreamMsgDistWeek 获取消息分布周数据。
func (s *WxOAService) GetUpstreamMsgDistWeek(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUpstreammsgdistweek, beginDate, endDate)
}

// GetUpstreamMsgDistMonth 获取消息分布月数据。
func (s *WxOAService) GetUpstreamMsgDistMonth(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsUpstreammsgdistmonth, beginDate, endDate)
}

// GetInterfaceSummary 获取被动回复概要数据。
func (s *WxOAService) GetInterfaceSummary(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsInterfacesummary, beginDate, endDate)
}

// GetInterfaceSummaryHour 获取被动回复分布数据。
func (s *WxOAService) GetInterfaceSummaryHour(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.statsRange(ctx, epStatsInterfacesummaryhour, beginDate, endDate)
}

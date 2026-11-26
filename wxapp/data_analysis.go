package wxapp

import (
	"context"
	"net/http"
)

// 数据分析表项（官方取证见 change wxapp-security-nearby-data proposal；留存/趋势/概况/
// 页面/画像/分布十件日期参数统一 yyyymmdd，性能数据走独立 path）
var (
	epRetainDaily   = Endpoint{"data.retain_daily", http.MethodPost, "/datacube/getweanalysisappiddailyretaininfo", []string{"access_token"}, true}
	epRetainWeekly  = Endpoint{"data.retain_weekly", http.MethodPost, "/datacube/getweanalysisappidweeklyretaininfo", []string{"access_token"}, true}
	epRetainMonthly = Endpoint{"data.retain_monthly", http.MethodPost, "/datacube/getweanalysisappidmonthlyretaininfo", []string{"access_token"}, true}
	epTrendDaily    = Endpoint{"data.trend_daily", http.MethodPost, "/datacube/getweanalysisappiddailyvisittrend", []string{"access_token"}, true}
	epTrendWeekly   = Endpoint{"data.trend_weekly", http.MethodPost, "/datacube/getweanalysisappidweeklyvisittrend", []string{"access_token"}, true}
	epTrendMonthly  = Endpoint{"data.trend_monthly", http.MethodPost, "/datacube/getweanalysisappidmonthlyvisittrend", []string{"access_token"}, true}
	epSummaryDaily  = Endpoint{"data.summary_daily", http.MethodPost, "/datacube/getweanalysisappiddailysummarytrend", []string{"access_token"}, true}
	epVisitPage     = Endpoint{"data.visit_page", http.MethodPost, "/datacube/getweanalysisappidvisitpage", []string{"access_token"}, true}
	epUserPortrait  = Endpoint{"data.user_portrait", http.MethodPost, "/datacube/getweanalysisappiduserportrait", []string{"access_token"}, true}
	epVisitDist     = Endpoint{"data.visit_dist", http.MethodPost, "/datacube/getweanalysisappidvisitdistribution", []string{"access_token"}, true}
	epPerformance   = Endpoint{"data.performance", http.MethodPost, "/wxa/business/performance/boot", []string{"access_token"}, true}
)

// dataRange 数据分析统一调用（begin/end 日期限制由各方法注释载明，调用方保证）。
func (s *WxAppService) dataRange(ctx context.Context, ep Endpoint, beginDate, endDate string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, ep, map[string]string{"access_token": token}, map[string]string{
		"begin_date": beginDate, "end_date": endDate,
	})
}

// GetDailyRetain 日留存（单日数据，end ≤昨日）。
func (s *WxAppService) GetDailyRetain(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epRetainDaily, beginDate, endDate)
}

// GetWeeklyRetain 周留存（一个自然周：begin 周一、end 周日）。
func (s *WxAppService) GetWeeklyRetain(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epRetainWeekly, beginDate, endDate)
}

// GetMonthlyRetain 月留存（一个自然月：月初至月末）。
func (s *WxAppService) GetMonthlyRetain(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epRetainMonthly, beginDate, endDate)
}

// GetDailyVisitTrend 日趋势（begin 与 end 同一天，≤昨日）。
func (s *WxAppService) GetDailyVisitTrend(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epTrendDaily, beginDate, endDate)
}

// GetWeeklyVisitTrend 周趋势（自然周）。
func (s *WxAppService) GetWeeklyVisitTrend(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epTrendWeekly, beginDate, endDate)
}

// GetMonthlyVisitTrend 月趋势（自然月，最新为上一个自然月）。
func (s *WxAppService) GetMonthlyVisitTrend(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epTrendMonthly, beginDate, endDate)
}

// GetDailySummary 访问数据概况（单日 ≤昨日）。
func (s *WxAppService) GetDailySummary(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epSummaryDaily, beginDate, endDate)
}

// GetVisitPage 访问页面数据（单日 ≤昨日；按 pv 排序 top200）。
func (s *WxAppService) GetVisitPage(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epVisitPage, beginDate, endDate)
}

// GetUserPortrait 用户画像分布（begin/end 相差 0/6/29 天对应近 1/7/30 天，end ≤昨日）。
func (s *WxAppService) GetUserPortrait(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epUserPortrait, beginDate, endDate)
}

// GetVisitDistribution 访问分布数据（单日 ≤昨日）。
func (s *WxAppService) GetVisitDistribution(ctx context.Context, beginDate, endDate string) (map[string]any, error) {
	return s.dataRange(ctx, epVisitDist, beginDate, endDate)
}

// GetPerformanceData 小程序性能数据（module+time{跨度 ≤30 天}+params 查询条件）。
func (s *WxAppService) GetPerformanceData(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epPerformance, map[string]string{"access_token": token}, body)
}

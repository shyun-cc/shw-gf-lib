package wxapp

import (
	"context"
	"net/http"
	"strconv"
)

// 付费管理表项（官方取证见 change wxapp-trade proposal）
var (
	epChargeUsageDetail = Endpoint{"charge.usage_detail", http.MethodGet, "/wxa/charge/usage/get", []string{"access_token", "spuId", "offset", "limit"}, false}
	epChargeRecentAvg   = Endpoint{"charge.recent_avg", http.MethodGet, "/wxa/charge/usage/get_recent_average", []string{"access_token", "spuId"}, false}
)

// GetChargeUsageDetail 查询购买资源包的用量情况（limit ≤20；返回累计用量/总量/资源包明细）。
func (s *WxAppService) GetChargeUsageDetail(ctx context.Context, spuID string, offset, limit int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epChargeUsageDetail, map[string]string{
		"access_token": token, "spuId": spuID, "offset": strconv.Itoa(offset), "limit": strconv.Itoa(limit),
	}, nil)
}

// GetRecentAverageUsage 获取付费能力最近用量（averageData 为模糊化月均——返回 50 表示
// ≤50 次/月，非精确值）。
func (s *WxAppService) GetRecentAverageUsage(ctx context.Context, spuID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epChargeRecentAvg, map[string]string{"access_token": token, "spuId": spuID}, nil)
}

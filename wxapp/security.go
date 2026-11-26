package wxapp

import (
	"context"
	"net/http"
)

// 小程序安全 + 附近小程序表项（官方取证见 change wxapp-security-nearby-data proposal）
var (
	epMsgSecCheck    = Endpoint{"sec.msg_check", http.MethodPost, "/wxa/msg_sec_check", []string{"access_token"}, true}
	epMediaCheck     = Endpoint{"sec.media_check", http.MethodPost, "/wxa/media_check_async", []string{"access_token"}, true}
	epUserRiskRank   = Endpoint{"sec.user_risk_rank", http.MethodPost, "/wxa/getuserriskrank", []string{"access_token"}, true}
	epNearbyPoiAdd   = Endpoint{"nearby.poi_add", http.MethodPost, "/wxa/addnearbypoi", []string{"access_token"}, true}
	epNearbyPoiDel   = Endpoint{"nearby.poi_del", http.MethodPost, "/wxa/delnearbypoi", []string{"access_token"}, true}
	epNearbyPoiList  = Endpoint{"nearby.poi_list", http.MethodPost, "/wxa/getnearbypoilist", []string{"access_token"}, true}
	epNearbyPoiStats = Endpoint{"nearby.poi_status", http.MethodPost, "/wxa/setnearbypoishowstatus", []string{"access_token"}, true}
)

// MsgSecCheck 文本内容安全识别 v2（content ≤2500 UTF-8 + version=2 + scene 1 资料/2 评论/
// 3 论坛/4 社交日志 + openid 须近 2 小时访问过小程序；4000 次/分）。
func (s *WxAppService) MsgSecCheck(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epMsgSecCheck, map[string]string{"access_token": token}, body)
}

// MediaCheckAsync 多媒体内容安全识别（异步：结果 30 分钟内经 wxa_media_check 事件推送；
// media_type 1 音频/2 图片；单文件 ≤10M；回调事件常量挂起待接线）。
func (s *WxAppService) MediaCheckAsync(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epMediaCheck, map[string]string{"access_token": token}, body)
}

// GetUserRiskRank 获取用户安全等级（scene 0 注册/1 营销作弊/2 UGC；返回 risk_rank 0-4 越高越风险）。
func (s *WxAppService) GetUserRiskRank(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epUserRiskRank, map[string]string{"access_token": token}, body)
}

// AddNearbyPoi 添加地点（11 项必填：门店/资质/图片 json/服务标签等；不支持并发，建议间隔
// 1 秒串行调用；返回 audit_id 与 poi_id）。
func (s *WxAppService) AddNearbyPoi(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epNearbyPoiAdd, map[string]string{"access_token": token}, body)
}

// DelNearbyPoi 删除地点。
func (s *WxAppService) DelNearbyPoi(ctx context.Context, poiID string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epNearbyPoiDel, map[string]string{"access_token": token}, map[string]string{"poi_id": poiID})
}

// GetNearbyPoiList 查看地点列表（page 从 1 起、pageRows ≤1000；返回可添加数与审核状态）。
func (s *WxAppService) GetNearbyPoiList(ctx context.Context, page, pageRows int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epNearbyPoiList, map[string]string{"access_token": token}, map[string]any{
		"page": page, "page_rows": pageRows,
	})
}

// SetNearbyPoiShowStatus 设置地点展示状态（status 0 不展示/1 展示）。
func (s *WxAppService) SetNearbyPoiShowStatus(ctx context.Context, poiID string, status int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epNearbyPoiStats, map[string]string{"access_token": token}, map[string]any{
		"poi_id": poiID, "status": status,
	})
}

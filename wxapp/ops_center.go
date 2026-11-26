package wxapp

import (
	"context"
	"net/http"
	"strconv"
)

// 运维中心表项（官方取证见 change wxapp-hardware-ops-plugin proposal；rt_log_search 官方
// GET 带 Body 按 GET+query 实现留痕；feedback_media 应答图片二进制走 invokeRaw）
var (
	epOpsDomainInfo   = Endpoint{"ops.domain_info", http.MethodPost, "/wxa/getwxadevinfo", []string{"access_token"}, true}
	epOpsPerformance  = Endpoint{"ops.performance", http.MethodPost, "/wxaapi/log/get_performance", []string{"access_token"}, true}
	epOpsSceneList    = Endpoint{"ops.scene_list", http.MethodGet, "/wxaapi/log/get_scene", []string{"access_token"}, false}
	epOpsVersionList  = Endpoint{"ops.version_list", http.MethodGet, "/wxaapi/log/get_client_version", []string{"access_token"}, false}
	epOpsRtLogSearch  = Endpoint{"ops.rt_log_search", http.MethodGet, "/wxaapi/userlog/userlog_search", []string{"access_token", "date", "begintime", "endtime", "start", "limit", "traceId", "url", "id", "filterMsg", "level"}, false}
	epOpsFeedbackList = Endpoint{"ops.feedback_list", http.MethodGet, "/wxaapi/feedback/list", []string{"access_token", "type", "page", "num"}, false}
	epOpsFeedbackImg  = Endpoint{"ops.feedback_media", http.MethodGet, "/cgi-bin/media/getfeedbackmedia", []string{"access_token", "record_id", "media_id"}, false}
	epOpsJsErrDetail  = Endpoint{"ops.jserr_detail", http.MethodPost, "/wxaapi/log/jserr_detail", []string{"access_token"}, true}
	epOpsJsErrList    = Endpoint{"ops.jserr_list", http.MethodPost, "/wxaapi/log/jserr_list", []string{"access_token"}, true}
	epOpsGrayRelease  = Endpoint{"ops.gray_release", http.MethodGet, "/wxa/getgrayreleaseplan", []string{"access_token"}, false}
)

// GetDomainInfo 查询小程序域名配置（action 空=全部 / getbizdomain 业务域名 /
// getserverdomain 服务器域名）。
func (s *WxAppService) GetDomainInfo(ctx context.Context, action string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if action != "" {
		body["action"] = action
	}
	return s.invoke(ctx, epOpsDomainInfo, map[string]string{"access_token": token}, body)
}

// GetOpsPerformance 获取运维性能数据（cost_time_type 1 启动总耗时/2 下载/3 初次渲染；
// scene 须先经 GetSceneList 获取——与数据分析 GetPerformanceData 是不同端点，勿混用）。
func (s *WxAppService) GetOpsPerformance(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epOpsPerformance, map[string]string{"access_token": token}, body)
}

// GetSceneList 获取访问来源列表（GET 无参；值为 GetOpsPerformance 的 scene 入参）。
func (s *WxAppService) GetSceneList(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epOpsSceneList, map[string]string{"access_token": token}, nil)
}

// GetVersionList 获取客户端版本列表（GET 无参）。
func (s *WxAppService) GetVersionList(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epOpsVersionList, map[string]string{"access_token": token}, nil)
}

// SearchRealtimeLog 查询实时日志（date 仅近 7 天、begintime/endtime 须为当天；
// traceId 可查单次启动全程；level 2 Info/4 Warn/8 Error 取 ≥；50 次/分）。
func (s *WxAppService) SearchRealtimeLog(ctx context.Context, params map[string]string) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	query := map[string]string{"access_token": token}
	for k, v := range params {
		query[k] = v
	}
	return s.invoke(ctx, epOpsRtLogSearch, query, nil)
}

// GetFeedbackList 获取用户反馈列表（page 从 1 起；type 1-8 反馈类型选填 0 全部）。
func (s *WxAppService) GetFeedbackList(ctx context.Context, feedbackType, page, num int) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epOpsFeedbackList, map[string]string{
		"access_token": token, "type": strconv.Itoa(feedbackType), "page": strconv.Itoa(page), "num": strconv.Itoa(num),
	}, nil)
}

// GetFeedbackMedia 获取反馈图片（record_id 来自 GetFeedbackList；成功应答图片二进制，
// 失败 JSON errcode → BizError）。
func (s *WxAppService) GetFeedbackMedia(ctx context.Context, recordID int, mediaID string) ([]byte, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invokeRaw(ctx, epOpsFeedbackImg, map[string]string{
		"access_token": token, "record_id": strconv.Itoa(recordID), "media_id": mediaID,
	}, nil)
}

// GetJsErrDetail 查询 js 错误详情（errorMsgMd5/errorStackMd5 双键来自 GetJsErrList 结果；
// 版本四件 "0" 代表全部）。
func (s *WxAppService) GetJsErrDetail(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epOpsJsErrDetail, map[string]string{"access_token": token}, body)
}

// GetJsErrList 查询错误列表（errType 0 全部/1 业务/2 插件/3 系统框架；keyword/openid
// 可传空串；orderby uv|pv；limit ≤30）。
func (s *WxAppService) GetJsErrList(ctx context.Context, body map[string]any) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epOpsJsErrList, map[string]string{"access_token": token}, body)
}

// GetGrayReleasePlan 获取分阶段发布详情（status 0-4：执行中/执行完毕等；含当前灰度比例）。
func (s *WxAppService) GetGrayReleasePlan(ctx context.Context) (map[string]any, error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoke(ctx, epOpsGrayRelease, map[string]string{"access_token": token}, nil)
}

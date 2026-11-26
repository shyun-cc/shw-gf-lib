package wecom

import (
	"context"
	"net/http"
)

// 会议域子域⑤表项·录制管理 10 + 会议高级功能账号 5（官方清点见 change
// wecom-meeting-layout-record proposal；vip 族为异步任务形态：submit job + 查询 result，
// 与邮件域 exmail/vip 不同 path）
var (
	epMtgRecList       = Endpoint{"meeting.record.list", http.MethodPost, "/cgi-bin/meeting/record/list", nil, true}
	epMtgRecStats      = Endpoint{"meeting.record.get_statistics", http.MethodPost, "/cgi-bin/meeting/record/get_statistics", nil, true}
	epMtgRecShareUpd   = Endpoint{"meeting.record.update_sharing_config", http.MethodPost, "/cgi-bin/meeting/record/update_sharing_config", nil, true}
	epMtgRecDelete     = Endpoint{"meeting.record.delete", http.MethodPost, "/cgi-bin/meeting/record/delete", nil, true}
	epMtgRecFileDelete = Endpoint{"meeting.record.delete_file", http.MethodPost, "/cgi-bin/meeting/record/delete_file", nil, true}
	epMtgRecFileGet    = Endpoint{"meeting.record.get_file", http.MethodPost, "/cgi-bin/meeting/record/get_file", nil, true}
	epMtgRecFileList   = Endpoint{"meeting.record.get_file_list", http.MethodPost, "/cgi-bin/meeting/record/get_file_list", nil, true}
	epMtgTsParagraphs  = Endpoint{"meeting.record.transcript.get_paragraph_list", http.MethodPost, "/cgi-bin/meeting/record/transcript/get_paragraph_list", nil, true}
	epMtgTsDetail      = Endpoint{"meeting.record.transcript.get_detail", http.MethodPost, "/cgi-bin/meeting/record/transcript/get_detail", nil, true}
	epMtgTsSearch      = Endpoint{"meeting.record.transcript.search", http.MethodPost, "/cgi-bin/meeting/record/transcript/search", nil, true}
	epMtgVipAddJob     = Endpoint{"meeting.vip.submit_batch_add_job", http.MethodPost, "/cgi-bin/meeting/vip/submit_batch_add_job", nil, true}
	epMtgVipAddResult  = Endpoint{"meeting.vip.batch_add_job_result", http.MethodPost, "/cgi-bin/meeting/vip/batch_add_job_result", nil, true}
	epMtgVipDelJob     = Endpoint{"meeting.vip.submit_batch_del_job", http.MethodPost, "/cgi-bin/meeting/vip/submit_batch_del_job", nil, true}
	epMtgVipDelResult  = Endpoint{"meeting.vip.batch_del_job_result", http.MethodPost, "/cgi-bin/meeting/vip/batch_del_job_result", nil, true}
	epMtgVipList       = Endpoint{"meeting.vip.list", http.MethodPost, "/cgi-bin/meeting/vip/list", nil, true}
)

// ListMeetingRecords 获取会议录制列表（start/end_time 必填且区间 ≤31 天；
// meetingid/meeting_code/userid 三选一选填，全不传查成员全部会议录制；limit ≤20 翻页）。
func (c *Client) ListMeetingRecords(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgRecList, nil, body)
}

// GetMeetingRecordStatistics 获取录制文件访问统计（按天维度；时间窗默认近 31 天且 ≤31 天）。
func (c *Client) GetMeetingRecordStatistics(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgRecStats, nil, body)
}

// UpdateMeetingRecordSharingConfig 修改会议录制共享设置（sharing_auth_type 0-5；
// 密码/有效期开关与值联动必传；4/5 类型不支持 Rooms）。
func (c *Client) UpdateMeetingRecordSharingConfig(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgRecShareUpd, nil, body)
}

// DeleteMeetingRecord 删除会议录制（删除该录制 ID 对应的全部云录制文件）。
func (c *Client) DeleteMeetingRecord(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgRecDelete, nil, body)
}

// DeleteMeetingRecordFile 删除单个录制文件。
func (c *Client) DeleteMeetingRecordFile(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgRecFileDelete, nil, body)
}

// GetMeetingRecordFile 获取单个录制文件详情（含录制文件与会议纪要、播放/下载地址）。
func (c *Client) GetMeetingRecordFile(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgRecFileGet, nil, body)
}

// ListMeetingRecordFiles 获取会议录制地址（meeting_record_id 维度的播放/下载地址列表）。
func (c *Client) ListMeetingRecordFiles(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgRecFileList, nil, body)
}

// ListMeetingTranscriptParagraphs 获取录制转写段落信息（段落 ID 与起止毫秒；声纹识别状态）。
func (c *Client) ListMeetingTranscriptParagraphs(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgTsParagraphs, nil, body)
}

// GetMeetingTranscriptDetail 获取录制转写详情（pid 起始段落 + limit 分页，默认全量）。
func (c *Client) GetMeetingTranscriptDetail(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgTsDetail, nil, body)
}

// SearchMeetingTranscript 搜索录制转写文本（返回命中段落/句子偏移与时间轴预览）。
func (c *Client) SearchMeetingTranscript(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgTsSearch, nil, body)
}

// SubmitMeetingVIPAddJob 分配会议高级功能账号（userid_list ≤100；异步任务，返回 jobid
// 与 invalid_userid_list，结果经 GetMeetingVIPAddJobResult 查询）。
func (c *Client) SubmitMeetingVIPAddJob(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgVipAddJob, nil, body)
}

// GetMeetingVIPAddJobResult 查询分配高级功能账号结果（succ 列表含已是高级账号者）。
func (c *Client) GetMeetingVIPAddJobResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgVipAddResult, nil, body)
}

// SubmitMeetingVIPDelJob 取消会议高级功能账号（异步任务形态同分配）。
func (c *Client) SubmitMeetingVIPDelJob(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgVipDelJob, nil, body)
}

// GetMeetingVIPDelJobResult 查询取消高级功能账号结果。
func (c *Client) GetMeetingVIPDelJobResult(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgVipDelResult, nil, body)
}

// ListMeetingVIPAccounts 获取会议高级功能账号列表（cursor/limit ≤200；以 has_more 判断续拉）。
func (c *Client) ListMeetingVIPAccounts(ctx context.Context, body map[string]any) (map[string]any, error) {
	return c.invoke(ctx, epMtgVipList, nil, body)
}

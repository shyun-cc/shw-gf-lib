package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// SmsTemplateAuditInfo 模板审核信息，时间保持厂商原始字符串。
type SmsTemplateAuditInfo struct {
	RejectInfo string
	AuditDate  string
}

// SmsTemplateResult 模板查询结果。TemplateStatus 保留厂商原值（含未知新状态）。
// VariableAttribute 保留厂商 JSON 字符串，不静默丢弃无法解析的规则。
// Success 表示查询成功，不表示模板审核通过。
type SmsTemplateResult struct {
	Success           bool
	RequestID         string
	ErrorCode         string
	ErrorMessage      string
	TemplateName      string
	TemplateCode      string
	TemplateContent   string
	TemplateType      string
	TemplateStatus    string
	RelatedSignName   string
	VariableAttribute string
	AuditInfo         SmsTemplateAuditInfo
}

// GetSmsTemplate 查询模板详情，不改变本地模板或项目审核状态。
// 厂商拒绝返回 Success=false 的结果；网络、解码失败返回 Go error。
func (s *Sms) GetSmsTemplate(ctx context.Context, templateCode string) (*SmsTemplateResult, error) {
	resp, err := s.client.Do(ctx, Request{Method: "POST", Host: smsHost, Path: smsPath, Action: "GetSmsTemplate", Version: smsVersion, Query: url.Values{"TemplateCode": {templateCode}}})
	if err != nil {
		return nil, err
	}
	result := &SmsTemplateResult{Success: resp.Success, RequestID: resp.RequestID, ErrorCode: resp.ErrorCode, ErrorMessage: resp.ErrorMessage}
	if !resp.Success {
		return result, nil
	}
	data, err := json.Marshal(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("encode SMS template response: %w", err)
	}
	if err = json.Unmarshal(data, result); err != nil {
		return nil, fmt.Errorf("decode SMS template response: %w", err)
	}
	return result, nil
}

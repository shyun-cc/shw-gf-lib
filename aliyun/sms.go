package aliyun

import (
	"context"
	"encoding/json"
	"strconv"
)

// jsonMarshal 模板参数序列化
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Sms 阿里云短信（dysmsapi，RPC 风格链）。
//
// 差异语义（对齐 PHP AliYunSmsService）：POST 业务参数走 URL query、body 为空；
// 成功判定宽容：HTTP 2xx 且 Code 为空或 "OK"。
type Sms struct {
	client *Client
}

const (
	smsHost    = "dysmsapi.aliyuncs.com"
	smsVersion = "2017-05-25"
	smsPath    = "/"
)

// NewSms 构造短信服务
func NewSms(c *Client) *Sms { return &Sms{client: c} }

// SmsResult SendSms 结果
type SmsResult struct {
	Success      bool
	MessageID    string // BizId
	RequestID    string
	ErrorCode    string
	ErrorMessage string
}

// SendSms 发送单条短信。templateParam 可为 nil（无模板变量时不传）。
func (s *Sms) SendSms(ctx context.Context, phone, signName, templateCode string, templateParam map[string]any) (*SmsResult, error) {
	query := make(map[string][]string)
	query["PhoneNumbers"] = []string{phone}
	query["SignName"] = []string{signName}
	query["TemplateCode"] = []string{templateCode}
	if len(templateParam) > 0 {
		encoded, err := jsonMarshal(templateParam)
		if err != nil {
			return nil, err
		}
		query["TemplateParam"] = []string{encoded}
	}

	resp, err := s.client.Do(ctx, Request{
		Method:  "POST",
		Host:    smsHost,
		Path:    smsPath,
		Action:  "SendSms",
		Version: smsVersion,
		Query:   query,
	})
	if err != nil {
		return nil, err
	}
	result := &SmsResult{
		Success:      resp.Success,
		RequestID:    resp.RequestID,
		ErrorCode:    resp.ErrorCode,
		ErrorMessage: resp.ErrorMessage,
	}
	if resp.Body != nil {
		if bizID, ok := resp.Body["BizId"].(string); ok {
			result.MessageID = bizID
		}
	}
	return result, nil
}

// QuerySendDetail 明细的 SendStatus 取值（对齐厂商枚举）
const (
	SmsStatusWaiting = 1 // 等待回执
	SmsStatusFailed  = 2 // 发送失败
	SmsStatusSuccess = 3 // 发送成功
)

// QuerySendDetail 发送明细单条（QuerySendDetails 响应 DTO 映射）
type QuerySendDetail struct {
	Phone        string // PhoneNum
	SendDate     string // 发送时间（yyyy-MM-dd HH:mm:ss，厂商响应格式）
	ReceiveDate  string // 接收时间（回执到达；未回执为空）
	SendStatus   int
	ErrCode      string // 运营商状态码，成功为 DELIVERED
	TemplateCode string // 测试模板发送时厂商不返回
	Content      string
}

// QuerySendResult QuerySendDetails 结果
type QuerySendResult struct {
	Success      bool
	TotalCount   int
	Details      []QuerySendDetail
	RequestID    string
	ErrorCode    string
	ErrorMessage string
}

// QuerySendDetails 查询短信发送明细（dysmsapi QuerySendDetails，与 SendSms 同链同构：
// ACS3 签名、POST 业务参数在 URL query、body 空、宽容成功判定）。
//
// 厂商契约：sendDate 为发送日期，请求格式 yyyyMMdd（如 20260901——与响应明细
// SendDate 的 yyyy-MM-dd HH:mm:ss 不对称，拼错会被厂商拒绝或静默查空）；bizID
// 为 SendSms 返回的 MessageID，空串表示不限流水；仅支持单手机号、单 BizId、
// 查询最近 30 天。pageSize/currentPage 原样透传（int 转 string），不做范围校验
// 与默认值填补，越界由厂商拒绝并经 ErrorCode 带回。
//
// 错误心智与 SendSms 一致：Go error 仅用于网络/本地失败；厂商拒绝时返回
// Success=false 的 Result（ErrorCode/ErrorMessage 带回）。明细终态以 SendStatus
// 与 SmsStatusWaiting/SmsStatusFailed/SmsStatusSuccess 常量比对。TotalCount 为
// 提交维度计数（非计费维度）。
func (s *Sms) QuerySendDetails(ctx context.Context, phone, sendDate, bizID string, pageSize, currentPage int) (*QuerySendResult, error) {
	query := make(map[string][]string)
	query["PhoneNumber"] = []string{phone}
	query["SendDate"] = []string{sendDate}
	query["PageSize"] = []string{strconv.Itoa(pageSize)}
	query["CurrentPage"] = []string{strconv.Itoa(currentPage)}
	if bizID != "" {
		query["BizId"] = []string{bizID}
	}

	resp, err := s.client.Do(ctx, Request{
		Method:  "POST",
		Host:    smsHost,
		Path:    smsPath,
		Action:  "QuerySendDetails",
		Version: smsVersion,
		Query:   query,
	})
	if err != nil {
		return nil, err
	}
	result := &QuerySendResult{
		Success:      resp.Success,
		Details:      make([]QuerySendDetail, 0),
		RequestID:    resp.RequestID,
		ErrorCode:    resp.ErrorCode,
		ErrorMessage: resp.ErrorMessage,
	}
	if resp.Body != nil {
		result.TotalCount = anyToInt(resp.Body["TotalCount"])
		if dtos, ok := resp.Body["SmsSendDetailDTOs"].(map[string]any); ok {
			if list, ok := dtos["SmsSendDetailDTO"].([]any); ok {
				result.Details = make([]QuerySendDetail, 0, len(list))
				for _, item := range list {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					result.Details = append(result.Details, QuerySendDetail{
						Phone:        anyToString(m["PhoneNum"]),
						SendDate:     anyToString(m["SendDate"]),
						ReceiveDate:  anyToString(m["ReceiveDate"]),
						SendStatus:   anyToInt(m["SendStatus"]),
						ErrCode:      anyToString(m["ErrCode"]),
						TemplateCode: anyToString(m["TemplateCode"]),
						Content:      anyToString(m["Content"]),
					})
				}
			}
		}
	}
	return result, nil
}

// anyToInt 双形态取整：厂商 TotalCount 文档标注 string，而 Client 的 json 解码
// 把 number 解为 float64——string 走 strconv.Atoi、number 走 float64，其余形态
// （含缺失/解析失败）容错为零值。TotalCount 与 SendStatus 共用。
func anyToInt(v any) int {
	switch x := v.(type) {
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0
		}
		return n
	case float64:
		return int(x)
	default:
		return 0
	}
}

// anyToString map 值宽容取串（缺失或非 string 形态返回零值）
func anyToString(v any) string {
	s, _ := v.(string)
	return s
}

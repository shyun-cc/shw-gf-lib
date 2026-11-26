package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
)

// Sts 阿里云临时凭证服务（sts.aliyuncs.com，ROA 风格链）。
//
// 差异语义（对齐 PHP AliYunStsService）：POST 参数在 JSON body；
// 成功判定严格：HTTP 2xx 且响应不得带 Code 字段。
type Sts struct {
	client *Client
}

const (
	stsHost    = "sts.aliyuncs.com"
	stsVersion = "2015-04-01"
	stsPath    = "/"
)

// NewSts 构造 STS 服务
func NewSts(c *Client) *Sts { return &Sts{client: c} }

// StsCredentials AssumeRole 返回的临时凭证四字段
type StsCredentials struct {
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
	Expiration      string
}

// StsResult AssumeRole 结果
type StsResult struct {
	Success      bool
	Credentials  StsCredentials
	RequestID    string
	ErrorCode    string
	ErrorMessage string
}

// AssumeRole 获取临时凭证。policy 可为 nil（不附加内联策略）；expireSeconds 默认 3600。
func (s *Sts) AssumeRole(ctx context.Context, roleArn, roleSessionName string, policy map[string]any, expireSeconds int) (*StsResult, error) {
	if expireSeconds <= 0 {
		expireSeconds = 3600
	}
	body := map[string]any{
		"RoleArn":         roleArn,
		"RoleSessionName": roleSessionName,
		"DurationSeconds": expireSeconds,
	}
	region := s.client.Creds.RegionID
	if region == "" {
		region = "cn-hangzhou"
	}
	body["RegionId"] = region
	if len(policy) > 0 {
		encoded, err := json.Marshal(policy)
		if err != nil {
			return nil, fmt.Errorf("policy 序列化失败: %w", err)
		}
		body["Policy"] = string(encoded)
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.Do(ctx, Request{
		Method:  "POST",
		Host:    stsHost,
		Path:    stsPath,
		Action:  "AssumeRole",
		Version: stsVersion,
		Query:   nil,
		Body:    bodyJSON,
	})
	if err != nil {
		return nil, err
	}

	// 严格判定：响应带 Code 即失败（Do 的宽容 Code=OK 分支不会命中——Sts 成功响应无 Code）
	result := &StsResult{
		Success:      resp.Success && resp.ErrorCode == "",
		RequestID:    resp.RequestID,
		ErrorCode:    resp.ErrorCode,
		ErrorMessage: resp.ErrorMessage,
	}
	if resp.Body != nil {
		if creds, ok := resp.Body["Credentials"].(map[string]any); ok {
			result.Credentials.AccessKeyID, _ = creds["AccessKeyId"].(string)
			result.Credentials.AccessKeySecret, _ = creds["AccessKeySecret"].(string)
			result.Credentials.SecurityToken, _ = creds["SecurityToken"].(string)
			result.Credentials.Expiration, _ = creds["Expiration"].(string)
		}
	}
	return result, nil
}

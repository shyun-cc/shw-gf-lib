package sms

import (
	"context"
	"fmt"

	"github.com/shyun-cc/shw-gf-lib/aliyun"
)

// AliyunConfig 阿里云 Provider 配置
type AliyunConfig struct {
	Client    *aliyun.Client    // 已构造的 aliyun 客户端（凭证/BaseURL/HTTPClient 在此注入）
	SignName  string            // 默认签名（消息级 SignName 为空时使用）
	Templates map[string]string // 场景名 → 模板 Code（key 为 Scene 字符串值）
}

// aliyunProvider 阿里云短信 Provider（实现 sms.Provider）
type aliyunProvider struct {
	client *aliyun.Client
	sign   string
	tpls   map[string]string
}

// NewAliyunProvider 创建阿里云短信 Provider
func NewAliyunProvider(cfg AliyunConfig) Provider {
	return &aliyunProvider{
		client: cfg.Client,
		sign:   cfg.SignName,
		tpls:   cfg.Templates,
	}
}

func (p *aliyunProvider) Name() string { return "aliyun" }

func (p *aliyunProvider) Send(ctx context.Context, msg Message) (*Result, error) {
	templateCode, ok := p.tpls[string(msg.Scene)]
	if !ok || templateCode == "" {
		return nil, fmt.Errorf("未配置场景模板: %s", msg.Scene)
	}
	signName := msg.SignName
	if signName == "" {
		signName = p.sign
	}

	res, err := aliyun.NewSms(p.client).SendSms(ctx, msg.Phone, signName, templateCode, msg.Params)
	if err != nil {
		return nil, err
	}
	return &Result{
		RequestID:    res.RequestID,
		ErrorCode:    res.ErrorCode,
		Success:      res.Success,
		MessageID:    res.MessageID,
		Provider:     "aliyun",
		ErrorMessage: res.ErrorMessage,
	}, nil
}

package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

const defaultRobotAPI = "https://qyapi.weixin.qq.com"

// RobotClient 群机器人 webhook 客户端：仅凭 key（无 access_token），POST 消息到
// qyapi.weixin.qq.com/cgi-bin/webhook/send?key=KEY。
type RobotClient struct {
	Key        string
	BaseURL    string // 默认 https://qyapi.weixin.qq.com；测试桩注入用
	HTTPClient *http.Client
}

// NewRobotClient 构造（key 为群机器人 webhook 的 key 参数）
func NewRobotClient(key string) *RobotClient {
	return &RobotClient{Key: key}
}

func (r *RobotClient) send(ctx context.Context, msgType string, payload map[string]any) error {
	body := map[string]any{"msgtype": msgType}
	for k, v := range payload {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return &InfraError{Op: "encode", Err: err}
	}
	base := r.BaseURL
	if base == "" {
		base = defaultRobotAPI
	}
	target := base + "/cgi-bin/webhook/send?key=" + r.Key
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return &InfraError{Op: "http", Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return &InfraError{Op: "http", Err: err}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return &InfraError{Op: "decode", Err: err}
	}
	if code := errcodeOf(decoded); code != 0 {
		errmsg, _ := decoded["errmsg"].(string)
		return &BizError{Errcode: code, Errmsg: errmsg}
	}
	return nil
}

// SendRobotText 文本消息
func (r *RobotClient) SendRobotText(ctx context.Context, content string, mentionedList, mentionedMobileList []string) error {
	text := map[string]any{"content": content}
	if len(mentionedList) > 0 {
		text["mentioned_list"] = mentionedList
	}
	if len(mentionedMobileList) > 0 {
		text["mentioned_mobile_list"] = mentionedMobileList
	}
	return r.send(ctx, "text", map[string]any{"text": text})
}

// SendRobotMarkdown markdown 消息
func (r *RobotClient) SendRobotMarkdown(ctx context.Context, content string) error {
	return r.send(ctx, "markdown", map[string]any{"markdown": map[string]any{"content": content}})
}

// SendRobotImage 图片消息（base64 与 md5 由调用方预计算）
func (r *RobotClient) SendRobotImage(ctx context.Context, imageBase64, imageMD5 string) error {
	return r.send(ctx, "image", map[string]any{"image": map[string]any{"base64": imageBase64, "md5": imageMD5}})
}

// SendRobotNews 图文消息（articles 元素含 title/description/url/picurl）
func (r *RobotClient) SendRobotNews(ctx context.Context, articles []map[string]any) error {
	return r.send(ctx, "news", map[string]any{"news": map[string]any{"articles": articles}})
}

// SendRobotFile 文件消息
func (r *RobotClient) SendRobotFile(ctx context.Context, mediaID string) error {
	return r.send(ctx, "file", map[string]any{"file": map[string]any{"media_id": mediaID}})
}

// SendRobotTemplateCard 模版卡片消息
func (r *RobotClient) SendRobotTemplateCard(ctx context.Context, card map[string]any) error {
	return r.send(ctx, "template_card", map[string]any{"template_card": card})
}

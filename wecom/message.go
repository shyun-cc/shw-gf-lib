package wecom

import "encoding/json"

// 九种消息体：序列化键对齐 PHP toArray（msgtype + 对应内容字段）。
//
// 实现注意：包装结构体的内容字段用「影子类型」（无自定义 MarshalJSON 的普通结构体），
// 避免外层 MarshalJSON → 内容字段 MarshalJSON 的无限递归。

type textShadow struct {
	Content string `json:"content"`
}
type msgText struct {
	MsgType string     `json:"msgtype"`
	Text    textShadow `json:"text"`
}

// TextMsg 文本消息
type TextMsg struct {
	Content string
}

// MarshalJSON 输出 {"msgtype":"text","text":{"content":...}}
func (m TextMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgText{MsgType: "text", Text: textShadow{Content: m.Content}})
}

type markdownShadow struct {
	Content string `json:"content"`
}
type msgMarkdown struct {
	MsgType  string         `json:"msgtype"`
	Markdown markdownShadow `json:"markdown"`
}

// MarkdownMsg Markdown 消息
type MarkdownMsg struct {
	Content string
}

func (m MarkdownMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgMarkdown{MsgType: "markdown", Markdown: markdownShadow{Content: m.Content}})
}

type imageShadow struct {
	MediaID string `json:"media_id"`
}
type msgImage struct {
	MsgType string      `json:"msgtype"`
	Image   imageShadow `json:"image"`
}

// ImageMsg 图片消息
type ImageMsg struct {
	MediaID string
}

func (m ImageMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgImage{MsgType: "image", Image: imageShadow{MediaID: m.MediaID}})
}

type voiceShadow struct {
	MediaID string `json:"media_id"`
}
type msgVoice struct {
	MsgType string      `json:"msgtype"`
	Voice   voiceShadow `json:"voice"`
}

// VoiceMsg 语音消息
type VoiceMsg struct {
	MediaID string
}

func (m VoiceMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgVoice{MsgType: "voice", Voice: voiceShadow{MediaID: m.MediaID}})
}

type videoShadow struct {
	MediaID     string `json:"media_id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}
type msgVideo struct {
	MsgType string      `json:"msgtype"`
	Video   videoShadow `json:"video"`
}

// VideoMsg 视频消息
type VideoMsg struct {
	MediaID     string
	Title       string
	Description string
}

func (m VideoMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgVideo{MsgType: "video", Video: videoShadow{
		MediaID: m.MediaID, Title: m.Title, Description: m.Description,
	}})
}

type fileShadow struct {
	MediaID string `json:"media_id"`
}
type msgFile struct {
	MsgType string     `json:"msgtype"`
	File    fileShadow `json:"file"`
}

// FileMsg 文件消息
type FileMsg struct {
	MediaID string
}

func (m FileMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgFile{MsgType: "file", File: fileShadow{MediaID: m.MediaID}})
}

type newsShadow struct {
	Articles []map[string]any `json:"articles"`
}
type msgNews struct {
	MsgType string     `json:"msgtype"`
	News    newsShadow `json:"news"`
}

// NewsMsg 图文消息（articles 以 map 透传业务字段，键与 PHP articles 元素一致）
type NewsMsg struct {
	Articles []map[string]any
}

func (m NewsMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgNews{MsgType: "news", News: newsShadow{Articles: m.Articles}})
}

type mpNewsShadow struct {
	Articles []map[string]any `json:"articles"`
}
type msgMpNews struct {
	MsgType string       `json:"msgtype"`
	MpNews  mpNewsShadow `json:"mpnews"`
}

// MpNewsMsg 图文消息（mpnews）
type MpNewsMsg struct {
	Articles []map[string]any
}

func (m MpNewsMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgMpNews{MsgType: "mpnews", MpNews: mpNewsShadow{Articles: m.Articles}})
}

type textCardShadow struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Btntxt      string `json:"btntxt,omitempty"`
}
type msgTextCard struct {
	MsgType  string         `json:"msgtype"`
	Textcard textCardShadow `json:"textcard"`
}

// TextCardMsg 文本卡片消息
type TextCardMsg struct {
	Title       string
	Description string
	URL         string
	Btntxt      string
}

func (m TextCardMsg) MarshalJSON() ([]byte, error) {
	return json.Marshal(msgTextCard{MsgType: "textcard", Textcard: textCardShadow{
		Title: m.Title, Description: m.Description, URL: m.URL, Btntxt: m.Btntxt,
	}})
}

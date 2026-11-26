// Package sms 短信发送抽象（对应 PHP Sms）。
//
// Scene 场景 + Provider 接口（Send 返回 Result）+ Manager 多供应商按名切换 +
// 发送事件（events 包派发 sms.Sent / sms.Failed）。默认日志驱动 Provider 供开发/测试。
package sms

import (
	"context"
	"fmt"

	"github.com/shyun-cc/shw-gf-lib/events"

	"github.com/gogf/gf/v2/frame/g"
)

// 事件名（events 派发；载荷 SendEventPayload）
const (
	EventSent   = "sms.Sent"
	EventFailed = "sms.Failed"
)

// Scene 短信场景
type Scene string

const (
	SceneLoginCode         Scene = "login_code"
	SceneResetPasswordCode Scene = "reset_password_code"
	SceneNotify            Scene = "notify"
	SceneGeneric           Scene = "generic"
)

// Message 短信消息结构体（对应 PHP SmsMessageStruct）
type Message struct {
	Phone    string
	Scene    Scene
	Params   map[string]any
	SignName string // 签名覆盖，空字符串使用默认
}

// Result 发送结果（对应 PHP SmsResultStruct）
type Result struct {
	RequestID    string // 厂商请求 ID；未提供时为空
	ErrorCode    string // 厂商响应码，成功时也可能为 OK
	Success      bool
	MessageID    string
	Provider     string
	ErrorMessage string
}

// SendEventPayload 事件载荷
type SendEventPayload struct {
	Phone  string
	Scene  Scene
	Result Result
}

// Provider 短信发送 Provider 接口（项目侧实现对接具体平台）
type Provider interface {
	// Name 供应商标识（Manager 注册与 Result.Provider 用）
	Name() string
	Send(ctx context.Context, msg Message) (*Result, error)
}

// logProvider 默认日志驱动 Provider（开发/测试环境用）
type logProvider struct{}

func (p *logProvider) Name() string { return "log" }

func (p *logProvider) Send(ctx context.Context, msg Message) (*Result, error) {
	code := ""
	if msg.Params != nil {
		if c, ok := msg.Params["code"]; ok {
			code = fmt.Sprintf("%v", c)
		}
	}
	g.Log().Infof(ctx, "[SMS LOG] phone=%s scene=%s code=%s", msg.Phone, msg.Scene, code)
	return &Result{Success: true, Provider: "log"}, nil
}

// Service 单 Provider 短信服务（简单场景直用；多供应商用 Manager）
type Service struct {
	provider   Provider
	dispatcher *events.Dispatcher // 事件派发器，nil 走 events.Default()
}

// New 创建短信服务实例（默认日志驱动，事件走全局分发器）
func New() *Service {
	return &Service{provider: &logProvider{}}
}

// NewWithProvider 创建带自定义 Provider 的短信服务
func NewWithProvider(p Provider) *Service {
	return &Service{provider: p}
}

// SetDispatcher 注入独立事件分发器（测试或事件域隔离用；不设走 events.Default()）
func (s *Service) SetDispatcher(d *events.Dispatcher) {
	s.dispatcher = d
}

// Send 发送短信并派发事件（成功 sms.Sent / 失败 sms.Failed）
func (s *Service) Send(ctx context.Context, msg Message) (*Result, error) {
	return sendWithEvents(ctx, s.provider, s.dispatcher, msg)
}

// Manager 多供应商管理器：按名注册、按名切换
type Manager struct {
	providers  map[string]Provider
	dispatcher *events.Dispatcher
}

// NewManager 创建多供应商管理器
func NewManager() *Manager {
	return &Manager{providers: make(map[string]Provider)}
}

// SetDispatcher 注入独立事件分发器
func (m *Manager) SetDispatcher(d *events.Dispatcher) {
	m.dispatcher = d
}

// Register 注册 Provider（按 Name() 存储，重名覆盖）
func (m *Manager) Register(p Provider) {
	m.providers[p.Name()] = p
}

// Provider 按名取出 Provider；未注册返回明确错误
func (m *Manager) Provider(name string) (Provider, error) {
	p, ok := m.providers[name]
	if !ok {
		return nil, fmt.Errorf("未注册的短信 Provider: %s（已注册: %v）", name, m.names())
	}
	return p, nil
}

// Send 经指定名称的 Provider 发送并派发事件
func (m *Manager) Send(ctx context.Context, name string, msg Message) (*Result, error) {
	p, err := m.Provider(name)
	if err != nil {
		return nil, err
	}
	return sendWithEvents(ctx, p, m.dispatcher, msg)
}

func (m *Manager) names() []string {
	ns := make([]string, 0, len(m.providers))
	for n := range m.providers {
		ns = append(ns, n)
	}
	return ns
}

// sendWithEvents 统一发送 + 事件派发（成功/失败各恰好一次；dispatcher nil 走全局）
func sendWithEvents(ctx context.Context, p Provider, dispatcher *events.Dispatcher, msg Message) (*Result, error) {
	dispatch := func(name string, payload SendEventPayload) {
		if dispatcher != nil {
			dispatcher.Dispatch(ctx, name, payload)
			return
		}
		events.Dispatch(ctx, name, payload)
	}

	res, err := p.Send(ctx, msg)
	if err != nil {
		res = &Result{Success: false, Provider: p.Name(), ErrorMessage: err.Error()}
		dispatch(EventFailed, SendEventPayload{Phone: msg.Phone, Scene: msg.Scene, Result: *res})
		return res, err
	}
	if res.Provider == "" {
		res.Provider = p.Name()
	}
	event := EventSent
	if !res.Success {
		event = EventFailed
	}
	dispatch(event, SendEventPayload{Phone: msg.Phone, Scene: msg.Scene, Result: *res})
	return res, nil
}

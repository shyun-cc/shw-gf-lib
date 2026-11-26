package sms

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/aliyun"
	"github.com/shyun-cc/shw-gf-lib/events"
)

// mockProvider 记录型测试替身：捕获 Send 收到的 Message 与入参 ctx，供调用方逐项断言。
type mockProvider struct {
	name        string
	called      int             // Send 被调用次数
	received    Message         // 最近一次收到的消息
	receivedCtx context.Context // 最近一次收到的 ctx
	retErr      error           // Send 的返回错误（可注入）
	retResult   *Result         // Send 的返回 Result（nil 时返回成功）
}

func (m *mockProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock"
}

func (m *mockProvider) Send(ctx context.Context, msg Message) (*Result, error) {
	m.called++
	m.received = msg
	m.receivedCtx = ctx
	if m.retErr != nil {
		return nil, m.retErr
	}
	if m.retResult != nil {
		return m.retResult, nil
	}
	return &Result{Success: true, Provider: m.Name()}, nil
}

// TestNewDefaultLogProvider 默认 logProvider：New() 创建的服务 Send 成功，Result.Provider=log。
func TestNewDefaultLogProvider(t *testing.T) {
	svc := New()

	res, err := svc.Send(context.Background(), Message{
		Phone:  "10800138000",
		Scene:  SceneLoginCode,
		Params: map[string]any{"code": "123456"},
	})
	if err != nil {
		t.Fatalf("默认 logProvider Send 应成功，实际返回: %v", err)
	}
	if !res.Success || res.Provider != "log" {
		t.Fatalf("Result 应成功且 Provider=log: %+v", res)
	}
}

// TestNewWithProviderMessagePassThrough 自定义 Provider 注入：Send 后 mock 收到的
// Message 字段（Phone/Scene/Params/SignName）与 ctx 应逐项透传，不被篡改。
func TestNewWithProviderMessagePassThrough(t *testing.T) {
	mock := &mockProvider{}
	svc := NewWithProvider(mock)

	ctx := context.WithValue(context.Background(), struct{ k string }{k: "trace"}, "test-trace")
	msg := Message{
		Phone:    "10900139000",
		Scene:    SceneResetPasswordCode,
		Params:   map[string]any{"code": "654321", "ttl": 300},
		SignName: "自定义签名",
	}

	if _, err := svc.Send(ctx, msg); err != nil {
		t.Fatalf("mock Provider 未注入错误，Send 应成功，实际返回: %v", err)
	}

	if mock.called != 1 {
		t.Fatalf("Provider.Send 应被调用 1 次，实际 %d 次", mock.called)
	}
	if mock.received.Phone != msg.Phone {
		t.Errorf("Phone 透传失败：期望 %q，实际 %q", msg.Phone, mock.received.Phone)
	}
	if mock.received.Scene != msg.Scene {
		t.Errorf("Scene 透传失败：期望 %q，实际 %q", msg.Scene, mock.received.Scene)
	}
	if mock.received.SignName != msg.SignName {
		t.Errorf("SignName 透传失败：期望 %q，实际 %q", msg.SignName, mock.received.SignName)
	}
	if len(mock.received.Params) != len(msg.Params) {
		t.Fatalf("Params 透传失败：期望长度 %d，实际 %d", len(msg.Params), len(mock.received.Params))
	}
	for k, want := range msg.Params {
		if got, ok := mock.received.Params[k]; !ok {
			t.Errorf("Params 缺少键 %q", k)
		} else if got != want {
			t.Errorf("Params[%q] 透传失败：期望 %v，实际 %v", k, want, got)
		}
	}
	// ctx 应原样透传到 Provider（携带的 trace 值可取回）
	if v, ok := mock.receivedCtx.Value(struct{ k string }{k: "trace"}).(string); !ok || v != "test-trace" {
		t.Errorf("ctx 透传失败：期望携带 trace=test-trace，实际 %v", mock.receivedCtx)
	}
}

// TestSendErrorPropagation Provider 返回错误时，Service.Send 应透传错误且 Result 标记失败。
func TestSendErrorPropagation(t *testing.T) {
	wantErr := errors.New("上游短信平台超时")
	mock := &mockProvider{retErr: wantErr}
	svc := NewWithProvider(mock)

	res, err := svc.Send(context.Background(), Message{Phone: "10800138000", Scene: SceneNotify})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Send 应透传 Provider 错误 %v，实际返回: %v", wantErr, err)
	}
	if res.Success || res.ErrorMessage != wantErr.Error() {
		t.Fatalf("失败 Result 应带错误信息: %+v", res)
	}
}

// TestLogProviderNilParams logProvider 对 Params 为 nil 的消息不 panic，正常成功。
func TestLogProviderNilParams(t *testing.T) {
	svc := New()

	if _, err := svc.Send(context.Background(), Message{Phone: "10700137000", Scene: SceneGeneric, Params: nil}); err != nil {
		t.Fatalf("Params 为 nil 时 Send 应正常成功，实际返回: %v", err)
	}
	if _, err := svc.Send(context.Background(), Message{Phone: "10700137000", Scene: SceneGeneric, Params: map[string]any{"expire": "5min"}}); err != nil {
		t.Fatalf("Params 无 code 键时 Send 应正常成功，实际返回: %v", err)
	}
}

// TestManager_RegisterAndSwitch 双 Provider 按名注册与切换
func TestManager_RegisterAndSwitch(t *testing.T) {
	m := NewManager()
	a := &mockProvider{name: "a"}
	b := &mockProvider{name: "b"}
	m.Register(a)
	m.Register(b)

	if _, err := m.Send(context.Background(), "a", Message{Phone: "1", Scene: SceneGeneric}); err != nil {
		t.Fatalf("经 a 发送失败: %v", err)
	}
	if _, err := m.Send(context.Background(), "b", Message{Phone: "2", Scene: SceneGeneric}); err != nil {
		t.Fatalf("经 b 发送失败: %v", err)
	}
	if a.called != 1 || b.called != 1 {
		t.Fatalf("各 Provider 应各被调 1 次: a=%d b=%d", a.called, b.called)
	}
	if a.received.Phone != "1" || b.received.Phone != "2" {
		t.Fatalf("消息应落到对应 Provider: a=%s b=%s", a.received.Phone, b.received.Phone)
	}

	// 未注册名明确报错
	if _, err := m.Provider("ghost"); err == nil {
		t.Fatal("未注册名应返回明确错误")
	}
}

// TestSendEvents 成功/失败事件各恰好一次，载荷字段完整
func TestSendEvents(t *testing.T) {
	d := events.New()
	var mu sync.Mutex
	var sentPayloads, failedPayloads []SendEventPayload

	d.Subscribe(EventSent, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(SendEventPayload); ok {
			mu.Lock()
			sentPayloads = append(sentPayloads, p)
			mu.Unlock()
		}
	})
	d.Subscribe(EventFailed, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(SendEventPayload); ok {
			mu.Lock()
			failedPayloads = append(failedPayloads, p)
			mu.Unlock()
		}
	})

	okProvider := &mockProvider{name: "ok"}
	failProvider := &mockProvider{name: "fail", retResult: &Result{Success: false, Provider: "fail", ErrorMessage: "限流"}}
	svc := NewWithProvider(okProvider)
	svc.SetDispatcher(d)
	if _, err := svc.Send(context.Background(), Message{Phone: "10800000001", Scene: SceneLoginCode}); err != nil {
		t.Fatal(err)
	}
	svc2 := NewWithProvider(failProvider)
	svc2.SetDispatcher(d)
	if _, err := svc2.Send(context.Background(), Message{Phone: "10800000002", Scene: SceneNotify}); err != nil {
		t.Fatal(err)
	}

	if len(sentPayloads) != 1 {
		t.Fatalf("成功事件应恰好一次，实际 %d", len(sentPayloads))
	}
	if sentPayloads[0].Phone != "10800000001" || sentPayloads[0].Scene != SceneLoginCode || !sentPayloads[0].Result.Success {
		t.Fatalf("成功载荷不符: %+v", sentPayloads[0])
	}
	if len(failedPayloads) != 1 {
		t.Fatalf("失败事件应恰好一次，实际 %d", len(failedPayloads))
	}
	if failedPayloads[0].Result.ErrorMessage != "限流" {
		t.Fatalf("失败载荷不符: %+v", failedPayloads[0])
	}
}

// aliyunCapture 捕获 SendSms 请求的 httptest server
func aliyunCapture(t *testing.T, status int, reply string) (*httptest.Server, *capturedQuery) {
	t.Helper()
	c := &capturedQuery{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.query = r.URL.Query()
		c.body = string(body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

type capturedQuery struct {
	query url.Values
	body  string
}

// TestAliyunProvider_TemplateAndSign 阿里云 Provider：模板映射 + 默认签名/消息级覆盖
func TestAliyunProvider_TemplateAndSign(t *testing.T) {
	srv, cap := aliyunCapture(t, 200, `{"Code":"OK","BizId":"B001","RequestId":"R1"}`)

	client := aliyun.NewClient(aliyun.Credentials{AccessKeyID: "ak", AccessKeySecret: "sk"})
	client.BaseURL = srv.URL
	p := NewAliyunProvider(AliyunConfig{
		Client:   client,
		SignName: "默认签名",
		Templates: map[string]string{
			string(SceneLoginCode): "SMS_100",
		},
	})

	res, err := p.Send(context.Background(), Message{Phone: "10811112222", Scene: SceneLoginCode, Params: map[string]any{"code": "998877"}})
	if err != nil {
		t.Fatalf("Send 失败: %v", err)
	}
	if !res.Success || res.MessageID != "B001" || res.Provider != "aliyun" {
		t.Fatalf("Result 不符: %+v", res)
	}
	if cap.query.Get("TemplateCode") != "SMS_100" {
		t.Fatalf("TemplateCode 应为场景映射值 SMS_100: %v", cap.query)
	}
	if cap.query.Get("SignName") != "默认签名" {
		t.Fatalf("未显式传签名应用默认签名: %q", cap.query.Get("SignName"))
	}
	if cap.query.Get("TemplateParam") == "" {
		t.Fatal("模板参数应传递")
	}

	// 消息级签名覆盖
	if _, err := p.Send(context.Background(), Message{Phone: "10811112222", Scene: SceneLoginCode, SignName: "覆盖签名"}); err != nil {
		t.Fatal(err)
	}
	if cap.query.Get("SignName") != "覆盖签名" {
		t.Fatalf("消息级签名应覆盖默认: %q", cap.query.Get("SignName"))
	}
}

// TestAliyunProvider_UnmappedSceneError 未配置场景返回明确错误且不发 HTTP
func TestAliyunProvider_UnmappedSceneError(t *testing.T) {
	srv, cap := aliyunCapture(t, 200, `{}`)

	client := aliyun.NewClient(aliyun.Credentials{AccessKeyID: "ak", AccessKeySecret: "sk"})
	client.BaseURL = srv.URL
	p := NewAliyunProvider(AliyunConfig{Client: client, SignName: "s", Templates: map[string]string{}})

	_, err := p.Send(context.Background(), Message{Phone: "1", Scene: "unmapped_scene"})
	if err == nil {
		t.Fatal("未配置场景应报错")
	}
	if cap.query != nil {
		t.Fatal("未配置场景不应发出 HTTP 请求")
	}
}

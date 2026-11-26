package amqp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
)

// mustV7 生成 uuidv7 文本（契约形态；v1.6 的 NewV7 返回双值，测试侧吞错）
func mustV7() string {
	u, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return u.String()
}

// fakePublishCall fakePublisher 记录的一次发布调用
type fakePublishCall struct {
	exchange   string
	routingKey string
	body       []byte
	opts       []rabbitmq.PublishOption
}

// fakePublisher Publisher 接口 fake（不依赖真 broker，记录发布参数）
type fakePublisher struct {
	calls []fakePublishCall
	err   error // 非 nil 时 Publish 恒失败
}

func (f *fakePublisher) Publish(_ context.Context, exchange, routingKey string, body []byte, opts ...rabbitmq.PublishOption) error {
	f.calls = append(f.calls, fakePublishCall{exchange: exchange, routingKey: routingKey, body: body, opts: opts})
	return f.err
}

// unpackContentType 把收到的 PublishOption 依次应用到零值选项结构上，读出
// 最终生效的 ContentType（reflect 读未导出字段合法；PublishOption 的参数
// 类型未导出，这是唯一的外部探测面）。
func unpackContentType(opts []rabbitmq.PublishOption) string {
	v := reflect.New(reflect.TypeOf(rabbitmq.WithContentType("")).In(0).Elem())
	for _, fn := range opts {
		reflect.ValueOf(fn).Call([]reflect.Value{v})
	}
	return v.Elem().FieldByName("contentType").String()
}

// TestProducerPublishContract 发布契约全量断言：注入的 exchange/routingKey、
// 载荷 JSON 形态（{"task_id":"<uuid>"}）、ContentType application/json、
// persistent（推理链：opts 仅含 ContentType 一个选项 ⇒ 未传 WithTransient ⇒
// rabbitmq.Producer 默认 persistent=true 生效）
func TestProducerPublishContract(t *testing.T) {
	fp := &fakePublisher{}
	p := NewProducer(fp, "task.ex", "task.run")
	id := mustV7()

	if err := p.Publish(context.Background(), id); err != nil {
		t.Fatalf("发布应成功: %v", err)
	}
	if len(fp.calls) != 1 {
		t.Fatalf("应恰发布一次, 实际 %d 次", len(fp.calls))
	}
	call := fp.calls[0]
	if call.exchange != "task.ex" || call.routingKey != "task.run" {
		t.Fatalf("应发布到构造注入的 exchange/key: got %s/%s", call.exchange, call.routingKey)
	}

	// 载荷契约：原文即 {"task_id":"<id>"}（紧凑单字段 JSON）
	if want := `{"task_id":"` + id + `"}`; string(call.body) != want {
		t.Fatalf("载荷应为 %s, 实际 %s", want, call.body)
	}
	var m message
	if err := json.Unmarshal(call.body, &m); err != nil || m.TaskID != id {
		t.Fatalf("载荷反序列化应还原 task_id=%s: %v %+v", id, err, m)
	}

	// ContentType 契约
	if got := unpackContentType(call.opts); got != "application/json" {
		t.Fatalf("ContentType 应为 application/json, 实际 %q", got)
	}
	// persistent 推理链：唯一选项是 ContentType ⇒ 未传 WithTransient ⇒ 默认持久化
	if len(call.opts) != 1 {
		t.Fatalf("应仅传 ContentType 一个选项（persistent 走默认）, 实际 %d 个", len(call.opts))
	}
}

// TestProducerPublishError 发布失败如实透传（包装前缀 + 底层错误，不静默）
func TestProducerPublishError(t *testing.T) {
	fp := &fakePublisher{err: errors.New("mq down")}
	p := NewProducer(fp, "ex", "key")

	err := p.Publish(context.Background(), uuid.NewString())
	if err == nil {
		t.Fatal("发布失败应返回错误")
	}
	if !strings.Contains(err.Error(), "task/amqp") || !strings.Contains(err.Error(), "mq down") {
		t.Fatalf("错误应含 task/amqp 前缀与底层错误: %v", err)
	}
}

// TestProducerNotConnectedPassthrough 真实未连接的 rabbitmq.Producer：
// 断开期间立即返回 ErrNotConnected（绝不静默缓存重发），经 errors.Is 可判别
func TestProducerNotConnectedPassthrough(t *testing.T) {
	client := rabbitmq.NewClient(rabbitmq.DefaultConfig()) // 不 Connect
	p := NewProducer(client.NewProducer(), "ex", "key")

	err := p.Publish(context.Background(), uuid.NewString())
	if err == nil {
		t.Fatal("未连接发布应失败")
	}
	if !errors.Is(err, rabbitmq.ErrNotConnected) {
		t.Fatalf("应可判别 ErrNotConnected: %v", err)
	}
}

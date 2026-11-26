// logging 包测试。覆盖点：
//   - jsonHandler 纯函数五形态：纯格式串 / kv 对 / 奇数 Values 首个并入 mg /
//     无 traceId 省略 id / 含 Stack 输出 st
//   - 毒值防御：value 为 chan 时降级字符串不 panic
//   - 字段序断言（id < lv < dt < mg < ct < st）与空 ct 为 {}
//   - Setup 幂等（连调两次无 panic）
//   - 端到端：独立 glog 实例 SetHandlers + buffer writer 输出单行 JSON；
//     Setup 后 g.Log()（gins 单例）走 defaultHandler 兜底（尾部恢复 writer 防污染）
package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"

	"github.com/shyun-cc/shw-gf-lib/tracex"
)

// newInput 构造最小 HandlerInput：Buffer 就绪、Time 固定（dt 可精确断言），其余按需填
func newInput() *glog.HandlerInput {
	return &glog.HandlerInput{
		Buffer: bytes.NewBuffer(nil),
		Time:   time.Date(2026, 8, 24, 12, 30, 45, 123456000, time.Local),
	}
}

// runHandler 跑一次 jsonHandler，返回 Buffer 内容（去尾部换行）
func runHandler(in *glog.HandlerInput) string {
	jsonHandler(context.Background(), in)
	return strings.TrimSuffix(in.Buffer.String(), "\n")
}

// unmarshalLine 断言单行 JSON 合法并反序列化为 map；失败时 t.Fatal
func unmarshalLine(t *testing.T, line string) map[string]any {
	t.Helper()
	var e map[string]any
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatalf("应输出合法 JSON，实际 %q：%v", line, err)
	}
	return e
}

// ctMap 取 ct 字段并断言为 JSON 对象
func ctMap(t *testing.T, e map[string]any) map[string]any {
	t.Helper()
	ct, ok := e["ct"].(map[string]any)
	if !ok {
		t.Fatalf("ct 应为 JSON 对象，实际 %T(%v)", e["ct"], e["ct"])
	}
	return ct
}

// TestJSONHandler_PlainContent 场景①：纯格式串（Content 有值、Values 空）——
// mg 承载内容、ct 为 {}、无 id 无 st、lv/dt 原样
func TestJSONHandler_PlainContent(t *testing.T) {
	in := newInput()
	in.LevelFormat = "INFO"
	in.Content = "订单已创建"
	line := runHandler(in)

	e := unmarshalLine(t, line)
	if e["mg"] != "订单已创建" {
		t.Fatalf("mg = %v，期望 %q", e["mg"], "订单已创建")
	}
	if e["lv"] != "INFO" {
		t.Fatalf("lv = %v，期望 %q", e["lv"], "INFO")
	}
	if e["dt"] != "2026-08-24 12:30:45.123456" {
		t.Fatalf("dt = %v，期望微秒格式 %q", e["dt"], "2026-08-24 12:30:45.123456")
	}
	if len(ctMap(t, e)) != 0 {
		t.Fatalf("Values 空时 ct 应为 {}，实际 %v", e["ct"])
	}
	if strings.Contains(line, `"id"`) {
		t.Fatalf("无 traceId 时不应输出 id 字段：%q", line)
	}
	if strings.Contains(line, `"st"`) {
		t.Fatalf("无堆栈时不应输出 st 字段：%q", line)
	}
}

// TestJSONHandler_KeyValuePairs 场景②：kv 对 Values——ct 对象成立、id/mg 各归其位
func TestJSONHandler_KeyValuePairs(t *testing.T) {
	in := newInput()
	in.TraceId = "tid-001"
	in.LevelFormat = "INFO"
	in.Content = "登录成功"
	in.Values = []any{"phone", "10800138000", "scene", "login"}
	line := runHandler(in)

	e := unmarshalLine(t, line)
	if e["id"] != "tid-001" {
		t.Fatalf("id = %v，期望 %q", e["id"], "tid-001")
	}
	if e["mg"] != "登录成功" {
		t.Fatalf("kv 对不应污染 mg，实际 %v", e["mg"])
	}
	ct := ctMap(t, e)
	if ct["phone"] != "10800138000" || ct["scene"] != "login" {
		t.Fatalf("ct 键值不符，实际 %v", ct)
	}
	if len(ct) != 2 {
		t.Fatalf("ct 应恰有 2 个键，实际 %d：%v", len(ct), ct)
	}
}

// TestJSONHandler_OddValues 场景③：奇数个（3 个）Values——首个转字符串以空格并入 mg、
// 余下恰成一对 ct
func TestJSONHandler_OddValues(t *testing.T) {
	in := newInput()
	in.LevelFormat = "ERRO"
	in.Content = "msg"
	in.Values = []any{"首段", "phone", "138"}
	line := runHandler(in)

	e := unmarshalLine(t, line)
	if e["mg"] != "msg 首段" {
		t.Fatalf("奇数首个应空格并入 mg，实际 %q", e["mg"])
	}
	ct := ctMap(t, e)
	if len(ct) != 1 || ct["phone"] != "138" {
		t.Fatalf("余下应恰成一对 ct {phone:138}，实际 %v", ct)
	}
}

// TestJSONHandler_OddValuesEmptyContent 补充：Content 为空时奇数首个直接作 mg（无前导空格）
func TestJSONHandler_OddValuesEmptyContent(t *testing.T) {
	in := newInput()
	in.Values = []any{"孤立值"}
	line := runHandler(in)

	e := unmarshalLine(t, line)
	if e["mg"] != "孤立值" {
		t.Fatalf("Content 空时奇数首个应直接作 mg，实际 %q", e["mg"])
	}
}

// TestJSONHandler_NoTraceIdOmitID 场景④：TraceId 空——输出无 id 字段，其余字段不受影响
func TestJSONHandler_NoTraceIdOmitID(t *testing.T) {
	in := newInput()
	in.LevelFormat = "WARN"
	in.Content = "无链路"
	in.Values = []any{"k", "v"}
	line := runHandler(in)

	if strings.Contains(line, `"id"`) {
		t.Fatalf("TraceId 为空时不应输出 id 字段（绝不伪造）：%q", line)
	}
	e := unmarshalLine(t, line)
	if e["mg"] != "无链路" || e["lv"] != "WARN" {
		t.Fatalf("其余字段应不受影响，实际 %v", e)
	}
	if ctMap(t, e)["k"] != "v" {
		t.Fatalf("ct 应正常配对，实际 %v", e["ct"])
	}
}

// TestJSONHandler_StackOutput 场景⑤：Stack 非空——st 原样输出
func TestJSONHandler_StackOutput(t *testing.T) {
	in := newInput()
	in.LevelFormat = "PANI"
	in.Content = "panic 了"
	in.Stack = "goroutine 1 [running]:\nmain.main()\n\t/main.go:10"
	line := runHandler(in)

	e := unmarshalLine(t, line)
	if e["st"] != in.Stack {
		t.Fatalf("st 应原样输出堆栈，实际 %q", e["st"])
	}
}

// TestJSONHandler_PoisonValue 场景⑥：毒值（value 为 chan int）——降级为字符串入 ct，
// 不 panic、不丢整条日志
func TestJSONHandler_PoisonValue(t *testing.T) {
	in := newInput()
	in.LevelFormat = "INFO"
	in.Content = "带毒值"
	in.Values = []any{"ch", make(chan int), "ok", "正常值"}
	line := runHandler(in) // 若 panic 测试直接失败

	e := unmarshalLine(t, line)
	ct := ctMap(t, e)
	s, ok := ct["ch"].(string)
	if !ok || s == "" {
		t.Fatalf("毒值应降级为非空字符串，实际 %T(%v)", ct["ch"], ct["ch"])
	}
	if ct["ok"] != "正常值" {
		t.Fatalf("同条正常值不应受毒值牵连丢失：%v", ct)
	}
}

// TestJSONHandler_FieldOrder 场景⑦：字段序——JSON 字符串中 id 在 lv 前、lv 在 dt 前、
// dt 在 mg 前、mg 在 ct 前、ct 在 st 前（struct 声明序即字段序）
func TestJSONHandler_FieldOrder(t *testing.T) {
	in := newInput()
	in.TraceId = "tid-order"
	in.LevelFormat = "INFO"
	in.Content = "字段序"
	in.Values = []any{"k", "v"}
	in.Stack = "stack-text"
	line := runHandler(in)

	last := -1
	for _, key := range []string{`"id":`, `"lv":`, `"dt":`, `"mg":`, `"ct":`, `"st":`} {
		idx := strings.Index(line, key)
		if idx < 0 {
			t.Fatalf("输出缺少字段 %s：%q", key, line)
		}
		if idx <= last {
			t.Fatalf("字段序错乱：%q", line)
		}
		last = idx
	}
}

// TestSetup_Idempotent 场景⑧：Setup 连调两次无 panic，defaultHandler 已注册
func TestSetup_Idempotent(t *testing.T) {
	Setup()
	Setup() // 幂等：重复调用无副作用即通过
	if glog.GetDefaultHandler() == nil {
		t.Fatal("Setup 后 defaultHandler 应已注册")
	}
}

// TestEndToEnd_StandaloneInstance 端到端（独立实例）：glog.New() + SetHandlers(jsonHandler)
// + SetWriter buffer——Infof 后 buf 为单行 JSON（同步输出，glog 默认非异步），
// 可 json.Unmarshal，tracex 注入的 traceId 落入 id 字段
func TestEndToEnd_StandaloneInstance(t *testing.T) {
	l := glog.New()
	var buf bytes.Buffer
	l.SetWriter(&buf)
	l.SetHandlers(jsonHandler)
	l.SetStdoutPrint(false) // 输出只进 buffer，不污染测试输出

	ctx, tid := tracex.Ensure(context.Background())
	l.Infof(ctx, "e2e-%s", "standalone")

	out := buf.String()
	if out == "" {
		t.Fatal("同步输出：Infof 返回后 buffer 应有内容")
	}
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("应输出恰好一行（JSON + 单个换行），实际 %q", out)
	}
	e := unmarshalLine(t, strings.TrimSuffix(out, "\n"))
	if e["id"] != tid {
		t.Fatalf("id = %v，期望 ctx 注入的 traceId %q", e["id"], tid)
	}
	if e["lv"] != "INFO" {
		t.Fatalf("lv = %v，期望 %q", e["lv"], "INFO")
	}
	if e["mg"] != "e2e-standalone" {
		t.Fatalf("mg = %v，期望 %q", e["mg"], "e2e-standalone")
	}
	if len(ctMap(t, e)) != 0 {
		t.Fatalf("无 kv 时 ct 应为 {}，实际 %v", e["ct"])
	}
}

// TestSetupDefaultHandler 端到端（defaultHandler 兜底，任务 1.3）：Setup() 后
// g.Log()（gins 单例）未显式 SetHandlers，自动走包级 defaultHandler 输出 JSON，
// 且 tracex 注入 ctx 后 id 字段等于注入的 traceId；尾部恢复 writer 防污染本包其他测试
func TestSetupDefaultHandler(t *testing.T) {
	Setup()
	var buf bytes.Buffer
	g.Log().SetWriter(&buf)

	ctx, tid := tracex.Ensure(context.Background())
	g.Log().Info(ctx, "default-handler-check")

	out := buf.String()
	e := unmarshalLine(t, strings.TrimSpace(out))
	if e["id"] != tid {
		t.Fatalf("id = %v，期望 ctx 注入的 traceId %q", e["id"], tid)
	}
	if e["lv"] != "INFO" {
		t.Fatalf("lv = %v，期望 %q", e["lv"], "INFO")
	}
	if e["mg"] != "default-handler-check" {
		t.Fatalf("mg = %v，期望 %q", e["mg"], "default-handler-check")
	}
	if len(ctMap(t, e)) != 0 {
		t.Fatalf("无 kv 时 ct 应为 {}，实际 %v", e["ct"])
	}

	g.Log().SetWriter(os.Stdout) // 恢复 gins 单例 writer，防污染其他测试
}

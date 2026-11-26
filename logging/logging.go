// Package logging 公司统一 JSON 单行日志组件——把 glog 输出收敛为结构化 JSON，
// 供日志采集侧（ES/Loki 等）按字段检索，替代文本日志的人肉 grep。
//
// 接入两行：boot 层 logging.Setup() 一次；HTTP 入口挂 middleware.Trace 注 traceId。
//
// # 字段契约
//
// 每条日志输出单行 JSON，logEntry struct 声明序即字段序（id,lv,dt,mg,ct,st）：
//
//	id  string         `json:"id,omitempty"` — traceId，取 glog HandlerInput.TraceId
//	                                   （源自 ctx 的 OpenTelemetry SpanContext）；
//	                                   无则省略，绝不伪造
//	lv  string         `json:"lv"`        — 日志级别，glog 原生 4 字符缩写原样透传
//	                                   （DEBU/INFO/NOTI/WARN/ERRO/CRIT/PANI/FATA），
//	                                   不做全称映射
//	dt  string         `json:"dt"`        — 日志时间，微秒精度
//	                                   "2006-01-02 15:04:05.000000"
//	mg  string         `json:"mg"`        — 消息体：格式化后的 Content；Values 为奇数个
//	                                   时首个 Value 转字符串以空格并入（对齐 glog 内置
//	                                   HandlerStructure 的兜底语义）
//	ct  map[string]any `json:"ct"`        — 结构化上下文：剩余偶数个 Values 奇偶配对
//	                                   （values[i] 为 key 转字符串、values[i+1] 为 value）；
//	                                   恒输出，无配对时为 {}
//	st  string         `json:"st,omitempty"` — 堆栈文本（glog StStatus 产出），
//	                                   非空才输出
//
// kv 用法：logger.Info(ctx, "登录成功", "phone", "10800138000", "scene", "login")
// 输出 mg="登录成功"、ct={"phone":"10800138000","scene":"login"}。
//
// # 序列化防御
//
// ct 的 value 在组装时逐个经 json.Marshal 探测：不可序列化的毒值（chan/func/NaN/
// 循环引用等）降级为 fmt.Sprintf("%v") 字符串，保证任何输入下单条日志不 panic、
// 不丢整条，最终 Marshal 必然成功——不重蹈 glog 内置 HandlerJson marshal 失败
// 直接 panic 的覆辙。
//
// # 机制
//
// jsonHandler 为 glog Handler：组装 logEntry → json.Marshal → 写 in.Buffer
// （JSON + 单个换行）→ in.Next(ctx)；glog 恒在链尾自动追加 doFinalPrint 落盘，
// Buffer 非空时直接输出 Buffer 内容。Setup() 经 glog.SetDefaultHandler 把
// jsonHandler 注册为包级默认 handler（sync.Once 幂等）——未显式 SetHandlers 的
// 日志实例（含 g.Log() 等 gins 单例）均自动走 JSON 格式；显式 SetHandlers 的
// 实例可自行组合 handler 链，格式与输出目的地正交。
package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gogf/gf/v2/os/glog"
)

// timeFormatMicro dt 字段时间格式：秒级日期时间 + 微秒（对齐日志采集侧高精度检索）
const timeFormatMicro = "2006-01-02 15:04:05.000000"

// logEntry 单行 JSON 日志条目。struct 声明序即 JSON 字段序（见包 doc 字段契约）
type logEntry struct {
	ID string         `json:"id,omitempty"` // traceId（无则省略，绝不伪造）
	LV string         `json:"lv"`           // glog 原生级别缩写（DEBU/INFO/ERRO…）
	DT string         `json:"dt"`           // 微秒精度时间
	MG string         `json:"mg"`           // 消息体（Content + 奇数 Values 首个并入）
	CT map[string]any `json:"ct"`           // 结构化上下文 kv（恒输出，空时为 {}）
	ST string         `json:"st,omitempty"` // 堆栈文本（非空才输出）
}

// setupOnce 保证 Setup 的注册动作进程内只执行一次
var setupOnce sync.Once

// Setup 把 jsonHandler 注册为 glog 包级默认 handler（幂等，重复调用无副作用）。
// boot 层调用一次后，所有未显式 SetHandlers 的日志实例（含 g.Log() 等 gins 单例）
// 自动输出本组件契约的单行 JSON。
func Setup() {
	setupOnce.Do(func() {
		glog.SetDefaultHandler(jsonHandler)
	})
}

// jsonHandler glog Handler 实现：把 HandlerInput 纯读组装为 logEntry，序列化为
// 单行 JSON 写入 in.Buffer 后调 in.Next 交由 glog 链尾 doFinalPrint 落盘。
// 任何输入下不 panic、不丢整条日志。
func jsonHandler(ctx context.Context, in *glog.HandlerInput) {
	entry := logEntry{
		ID: in.TraceId,
		LV: in.LevelFormat,
		DT: in.Time.Format(timeFormatMicro),
		MG: in.Content,
		CT: map[string]any{}, // 恒输出 {}，不为 null
		ST: in.Stack,
	}

	values := in.Values
	// 奇数个 Values：首个转字符串并入 mg（Content 非空时以空格连接），余下配对——
	// 对齐 glog 内置 HandlerStructure 的兜底语义
	if len(values)%2 == 1 {
		if entry.MG != "" {
			entry.MG += " "
		}
		entry.MG += valueToString(values[0])
		values = values[1:]
	}
	// 剩余偶数个 Values 奇偶配对：values[i] 为 key 转字符串、values[i+1] 为 value 经净化入 ct
	for i := 0; i+1 < len(values); i += 2 {
		entry.CT[valueToString(values[i])] = sanitizeJSONValue(values[i+1])
	}

	jsonBytes, err := json.Marshal(entry)
	if err != nil {
		// 理论不可达：ct 的 value 已逐个探测净化、其余字段均为 string。
		// 防御兜底：丢弃 ct 重新序列化（纯 string 字段必然成功），绝不 panic 丢整条日志
		entry.CT = map[string]any{}
		jsonBytes, _ = json.Marshal(entry)
	}

	in.Buffer.Write(jsonBytes)
	in.Buffer.WriteByte('\n')
	in.Next(ctx)
}

// valueToString 把任意值转为字符串（mg 并入奇数 Values 首值、ct 的 key 用）
func valueToString(v any) string {
	return fmt.Sprintf("%v", v)
}

// sanitizeJSONValue 对 ct 的 value 做 json.Marshal 探测：不可序列化的毒值
// （chan/func/NaN/循环引用等）降级为 "%v" 字符串，保证最终 Marshal 必然成功
func sanitizeJSONValue(v any) any {
	if _, err := json.Marshal(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return v
}

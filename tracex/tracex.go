// Package tracex traceId 上下文载体——以 OpenTelemetry SpanContext 承载 traceId，
// 与 GF glog / gtrace 原生取值机制同源（glog 从 ctx 的 SpanContext 取 TraceID 打印）。
//
// 请求场景：middleware.Trace 在入口 Ensure 注入后，response 出口与请求期间日志同值。
// 非 HTTP 场景（worker/命令）：Ensure 惰性生成独立 traceId；New 强制生成新值（周期任务每轮独立）。
package tracex

import (
	"context"
	"crypto/rand"

	"go.opentelemetry.io/otel/trace"
)

// FromCtx 读取 ctx 中的 traceId（纯读，不生成）；无则返回空串
func FromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	tid := trace.SpanContextFromContext(ctx).TraceID()
	if tid.IsValid() {
		return tid.String()
	}
	return ""
}

// Ensure 惰性保证：ctx 已有有效 traceId 则原样沿用（上游/网关注入场景），
// 否则合成注入新 SpanContext 并返回新 ctx
func Ensure(ctx context.Context) (context.Context, string) {
	if tid := FromCtx(ctx); tid != "" {
		return ctx, tid
	}
	tid, sid := newIDs()
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(ctx, sc), tid.String()
}

// New 强制生成新 traceId 注入 ctx（覆盖已有值）——周期任务每轮独立 trace 用
func New(ctx context.Context) context.Context {
	tid, sid := newIDs()
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(ctx, sc)
}

// newIDs 生成随机 TraceID(16B) 与 SpanID(8B)；熵源失败时 panic（crypto/rand 系统级故障，不可降级）
func newIDs() (trace.TraceID, trace.SpanID) {
	var tid trace.TraceID
	var sid trace.SpanID
	if _, err := rand.Read(tid[:]); err != nil {
		panic("tracex: crypto/rand 不可用: " + err.Error())
	}
	if _, err := rand.Read(sid[:]); err != nil {
		panic("tracex: crypto/rand 不可用: " + err.Error())
	}
	return tid, sid
}

// WithTraceID 将指定 traceId（32 位十六进制，OpenTelemetry 格式）注入 ctx 并生成新 SpanID，
// 用于把已落库的 traceId（如任务 attempt 的 trace_id）贯穿到后续执行链路与日志。
// 格式非法时原样返回 ctx 与 false，不生成新值。
func WithTraceID(ctx context.Context, traceID string) (context.Context, bool) {
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil || !tid.IsValid() {
		return ctx, false
	}
	_, sid := newIDs()
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(ctx, sc), true
}

package task

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/tracex"
)

type attemptTraceKey struct{}

// AttemptTraceID 返回 ctx 中本次 attempt 的 trace 标识（与 AttemptView.TraceID 相同）。
// Runtime/Executor 执行 Handler、Splitter 时注入；不在任务执行上下文中时返回空串。
func AttemptTraceID(ctx context.Context) string {
	v, _ := ctx.Value(attemptTraceKey{}).(string)
	return v
}

// withAttempt 把租约记录的 trace 标识注入 ctx：总是可经 AttemptTraceID 读取；
// 为合法 OpenTelemetry traceId 时同时注入 SpanContext，使 glog/gtrace 日志与管理页 attempt trace 对应。
func withAttempt(ctx context.Context, l *Lease) context.Context {
	if l == nil || l.TraceID == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, attemptTraceKey{}, l.TraceID)
	if traced, ok := tracex.WithTraceID(ctx, l.TraceID); ok {
		return traced
	}
	return ctx
}

func newTraceID() string { return tracex.FromCtx(tracex.New(context.Background())) }

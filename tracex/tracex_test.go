package tracex

import (
	"context"
	"regexp"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestEnsure_LazyAndStable(t *testing.T) {
	ctx := context.Background()
	nc1, tid1 := Ensure(ctx)
	_, tid2 := Ensure(nc1)

	if tid1 == "" {
		t.Fatal("Ensure 应生成非空 traceId")
	}
	if !hex32.MatchString(tid1) {
		t.Fatalf("traceId 应为 32 位小写 hex，得到 %q", tid1)
	}
	if tid1 != tid2 {
		t.Fatalf("同 ctx 二次 Ensure 应沿用同值: %q vs %q", tid1, tid2)
	}
	if FromCtx(nc1) != tid1 {
		t.Fatalf("FromCtx 应读回 Ensure 注入的值")
	}
	// 原 ctx 不被污染（Ensure 返回新 ctx）
	if FromCtx(ctx) != "" {
		t.Fatal("原 ctx 不应被写入 traceId")
	}
}

func TestEnsure_ReuseUpstream(t *testing.T) {
	tid, sid := newIDs()
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	})
	upstream := trace.ContextWithSpanContext(context.Background(), sc)

	nc, got := Ensure(upstream)
	if got != tid.String() {
		t.Fatalf("应沿用上游注入值: %q vs %q", got, tid.String())
	}
	if nc != upstream {
		t.Fatal("沿用场景应返回原 ctx（不重建）")
	}
}

func TestNew_AlwaysFresh(t *testing.T) {
	ctx1 := New(context.Background())
	ctx2 := New(context.Background())
	t1, t2 := FromCtx(ctx1), FromCtx(ctx2)

	if t1 == "" || t2 == "" {
		t.Fatal("New 应注入有效 traceId")
	}
	if t1 == t2 {
		t.Fatalf("两次 New 应互不相同: %q", t1)
	}

	// New 覆盖已有值
	fresh := New(ctx1)
	if FromCtx(fresh) == t1 {
		t.Fatal("New 应覆盖已有 traceId")
	}
}

func TestFromCtx_Empty(t *testing.T) {
	if FromCtx(context.Background()) != "" {
		t.Fatal("无注入时应返回空串")
	}
}

func TestWithTraceID(t *testing.T) {
	const id = "4bf92f3577b34da6a3ce929d0e0e4736"
	ctx, ok := WithTraceID(context.Background(), id)
	if !ok || FromCtx(ctx) != id {
		t.Fatalf("应注入指定 traceId: ok=%v got=%q", ok, FromCtx(ctx))
	}
	for _, bad := range []string{"", "not-hex", "00000000000000000000000000000000", "4bf92f35"} {
		c, ok := WithTraceID(context.Background(), bad)
		if ok || FromCtx(c) != "" {
			t.Fatalf("非法 traceId %q 不应注入", bad)
		}
	}
}

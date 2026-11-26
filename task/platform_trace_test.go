package task

import (
	"context"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/tracex"
)

func TestWithAttemptTrace(t *testing.T) {
	const otel = "4bf92f3577b34da6a3ce929d0e0e4736"
	ctx := withAttempt(context.Background(), &Lease{TraceID: otel})
	if AttemptTraceID(ctx) != otel || tracex.FromCtx(ctx) != otel {
		t.Fatalf("OTel trace must reach both readers: %q %q", AttemptTraceID(ctx), tracex.FromCtx(ctx))
	}
	custom := withAttempt(context.Background(), &Lease{TraceID: "biz-trace-1"})
	if AttemptTraceID(custom) != "biz-trace-1" || tracex.FromCtx(custom) != "" {
		t.Fatal("non-OTel trace only via AttemptTraceID")
	}
	if AttemptTraceID(withAttempt(context.Background(), &Lease{})) != "" || AttemptTraceID(context.Background()) != "" {
		t.Fatal("no trace outside attempts")
	}
	a, b := newTraceID(), newTraceID()
	if len(a) != 32 || a == b {
		t.Fatal("default attempt trace must be a fresh OTel id per attempt", a, b)
	}
	r := &Runtime{}
	if got := r.traceID(context.Background()); len(got) != 32 {
		t.Fatal("runtime without TraceID callback must generate one", got)
	}
}

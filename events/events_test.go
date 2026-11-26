package events

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestDispatcher_PriorityOrder(t *testing.T) {
	d := New()
	var order []string
	var mu sync.Mutex

	for _, p := range []int{1, 10, 100} {
		p := p
		d.Subscribe("evt", p, func(_ context.Context, _ Event) {
			mu.Lock()
			order = append(order, fmt.Sprintf("p%d", p))
			mu.Unlock()
		})
	}

	d.Dispatch(context.Background(), "evt", nil)

	want := []string{"p100", "p10", "p1"}
	if len(order) != len(want) {
		t.Fatalf("执行次数 %d != %d: %v", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("执行顺序不符（大值先执行）: %v", order)
		}
	}
}

func TestDispatcher_SamePriorityRegistrationOrder(t *testing.T) {
	d := New()
	var order []string
	d.Subscribe("evt", 5, func(_ context.Context, _ Event) { order = append(order, "first") })
	d.Subscribe("evt", 5, func(_ context.Context, _ Event) { order = append(order, "second") })

	d.Dispatch(context.Background(), "evt", nil)

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("同优先级应按注册顺序: %v", order)
	}
}

func TestDispatcher_PanicIsolation(t *testing.T) {
	d := New()
	var executed []string

	d.Subscribe("evt", 100, func(_ context.Context, _ Event) { panic("boom") })
	d.Subscribe("evt", 10, func(_ context.Context, _ Event) { executed = append(executed, "mid") })
	d.Subscribe("evt", 1, func(_ context.Context, _ Event) { executed = append(executed, "low") })

	// 不应 panic 冒泡到 Dispatch 调用方
	d.Dispatch(context.Background(), "evt", nil)

	if len(executed) != 2 || executed[0] != "mid" || executed[1] != "low" {
		t.Fatalf("panic 应被隔离，后续 handler 继续执行: %v", executed)
	}
}

func TestDispatcher_NoSubscriber(t *testing.T) {
	d := New()
	d.Dispatch(context.Background(), "nobody-listens", nil) // 不 panic 不报错即通过
}

func TestDispatcher_PayloadPassThrough(t *testing.T) {
	d := New()
	type sent struct{ Phone string }
	var got any

	d.Subscribe("sms.sent", 0, func(_ context.Context, e Event) { got = e.Payload })

	d.Dispatch(context.Background(), "sms.sent", sent{Phone: "10800000000"})

	s, ok := got.(sent)
	if !ok || s.Phone != "10800000000" {
		t.Fatalf("载荷应原样透传: %#v", got)
	}
}

func TestDispatcher_OnlyTargetEventHandlersRun(t *testing.T) {
	d := New()
	var calls int
	d.Subscribe("a", 0, func(_ context.Context, _ Event) { calls++ })
	d.Subscribe("b", 0, func(_ context.Context, _ Event) { calls++ })

	d.Dispatch(context.Background(), "a", nil)

	if calls != 1 {
		t.Fatalf("只应执行事件 a 的 handler，实际 %d 次", calls)
	}
}

func TestEvent_Fields(t *testing.T) {
	d := New()
	var e Event
	d.Subscribe("evt", 0, func(_ context.Context, ev Event) { e = ev })

	d.Dispatch(context.Background(), "evt", "x")

	if e.Name != "evt" || e.Payload != "x" || e.DispatchedAt.IsZero() {
		t.Fatalf("事件字段不完整: %+v", e)
	}
}

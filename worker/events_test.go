package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
)

// TestDispatchFallback 验证 dispatchEvent 的两条派发路径：
// (a) 注入实例非 nil 经实例派发；(b) d=nil 降级进程级 Default()。
// 派发同步执行，channel 缓冲 1 即可回收，select 带超时上限防挂死。
func TestDispatchFallback(t *testing.T) {
	const (
		nameInstance = "worker.test.fallback.instance"
		nameDefault  = "worker.test.fallback.default"
		timeout      = 2 * time.Second
	)

	t.Run("instance", func(t *testing.T) {
		d := events.New()
		got := make(chan events.Event, 1)
		d.Subscribe(nameInstance, 0, func(ctx context.Context, e events.Event) {
			got <- e
		})

		want := RoundEvent{Name: "w"}
		dispatchEvent(d, context.Background(), nameInstance, want)

		select {
		case e := <-got:
			if e.Name != nameInstance {
				t.Fatalf("事件名不符：want %q got %q", nameInstance, e.Name)
			}
			p, ok := e.Payload.(RoundEvent)
			if !ok {
				t.Fatalf("载荷类型不符：want RoundEvent got %T", e.Payload)
			}
			if p.Name != want.Name || p.Duration != 0 || p.Err != nil {
				t.Fatalf("载荷内容不符：got %+v", p)
			}
		case <-time.After(timeout):
			t.Fatal("超时：注入实例路径未收到事件")
		}
	})

	// d=nil 走全局 Default()。Dispatcher 无反订阅，该专用事件名全局残留无害——
	// 此后无人再向此名派发，不影响其他用例。
	t.Run("default", func(t *testing.T) {
		got := make(chan events.Event, 1)
		events.Default().Subscribe(nameDefault, 0, func(ctx context.Context, e events.Event) {
			got <- e
		})

		want := WorkerStoppedEvent{Name: "w"}
		dispatchEvent(nil, context.Background(), nameDefault, want)

		select {
		case e := <-got:
			if e.Name != nameDefault {
				t.Fatalf("事件名不符：want %q got %q", nameDefault, e.Name)
			}
			p, ok := e.Payload.(WorkerStoppedEvent)
			if !ok {
				t.Fatalf("载荷类型不符：want WorkerStoppedEvent got %T", e.Payload)
			}
			if p.Name != want.Name || p.Err != nil {
				t.Fatalf("载荷内容不符：got %+v", p)
			}
		case <-time.After(timeout):
			t.Fatal("超时：全局 Default() 路径未收到事件")
		}
	})
}

// TestEventPayloads 编译期锁定四个载荷结构体的字段形态：构造并赋值全部字段，
// 运行时再轻量校验字段取值回读一致。
func TestEventPayloads(t *testing.T) {
	started := WorkerStartedEvent{Names: []string{"a", "b"}}
	roundStart := RoundEvent{Name: "a"}
	roundDone := RoundEvent{Name: "a", Duration: 3 * time.Second, Err: errors.New("boom")}
	panicked := WorkerPanickedEvent{Name: "a", Recovered: "panic value"}
	stopped := WorkerStoppedEvent{Name: "a", Err: context.Canceled}

	if len(started.Names) != 2 || started.Names[0] != "a" || started.Names[1] != "b" {
		t.Fatalf("WorkerStartedEvent 字段不符：%+v", started)
	}
	if roundStart.Name != "a" || roundStart.Duration != 0 || roundStart.Err != nil {
		t.Fatalf("RoundEvent 零值不符：%+v", roundStart)
	}
	if roundDone.Duration != 3*time.Second || roundDone.Err == nil || roundDone.Err.Error() != "boom" {
		t.Fatalf("RoundEvent 赋值不符：%+v", roundDone)
	}
	if panicked.Recovered != "panic value" {
		t.Fatalf("WorkerPanickedEvent 字段不符：%+v", panicked)
	}
	if !errors.Is(stopped.Err, context.Canceled) {
		t.Fatalf("WorkerStoppedEvent 字段不符：%+v", stopped)
	}
}

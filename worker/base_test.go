// BaseWorker（裸循环底座）用例（task 3.1/3.2），覆盖四个行为场景：
//  1. panic 轮转：panic 轮不 re-panic、panicRound 被调、afterRound 不被调，次轮恢复正常；
//  2. Run 的 ctx.Done 退出：cancel 后 Run 返回 nil（handle 阻塞 channel 接收，避免 busy-loop 空转）；
//  3. 轮次事件时序：一轮恰好 Started→Completed 各一次且载荷正确，panic 轮有 Started 无 Completed；
//  4. beforeRound 钩子跳过：无事件派发、handle 不执行。
//
// 复用 events_round_test.go 的轮次事件收集辅助（subscribeRound/waitRound/drainRounds）
// 与 worker_test.go 的 testTimeout；事件断言依赖 events 同步派发。
package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
)

// TestBaseWorker_RunOnce_PanicRecovery panic 轮转：同一 worker 连续两轮 RunOnce，
// 首轮 handle panic、次轮正常——断言不抛出、panicRound 首轮被调、afterRound 首轮
// 不被调次轮被调（panic 轮与正常轮的收尾钩子分工）。
func TestBaseWorker_RunOnce_PanicRecovery(t *testing.T) {
	var panicRounds, afterRounds int

	first := true
	w := NewBaseWorker("base-panic", func(ctx context.Context) error {
		if first {
			first = false
			panic("boom")
		}
		return nil
	})
	w.panicRound = func(ctx context.Context, r any) {
		panicRounds++
		if r != "boom" {
			t.Errorf("panicRound 应收到 panic 值 %q，实际 %v", "boom", r)
		}
	}
	w.afterRound = func(ctx context.Context) { afterRounds++ }

	// 首轮 handle panic：RunOnce 内 recover 兜底不抛出；panicRound 收尾、afterRound 不调
	w.RunOnce(context.Background())
	if panicRounds != 1 {
		t.Fatalf("panic 轮后 panicRound 应被调 1 次，实际 %d", panicRounds)
	}
	if afterRounds != 0 {
		t.Fatalf("panic 轮不应调 afterRound，实际被调 %d 次", afterRounds)
	}

	// 次轮 handle 正常：轮转继续，afterRound 被调、panicRound 不再增加
	w.RunOnce(context.Background())
	if panicRounds != 1 {
		t.Fatalf("正常轮后 panicRound 不应增加，实际 %d", panicRounds)
	}
	if afterRounds != 1 {
		t.Fatalf("正常轮后 afterRound 应被调 1 次，实际 %d", afterRounds)
	}
}

// TestBaseWorker_Run_CtxDone Run 的 ctx.Done 退出：handle 阻塞 channel 接收
// （阻塞型 handle 的典型形态，Run 的无间隔循环靠它自然让出，避免测试空转），
// cancel 并解除阻塞后 Run 必须返回 nil。
func TestBaseWorker_Run_CtxDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 注入独立 dispatcher：不依赖也不污染 events.Default() 全局状态
	d := events.New()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	w := NewBaseWorker("base-ctx", func(ctx context.Context) error {
		select { // 非阻塞标记进入，仅首轮成功
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil
	}, WithBaseDispatcher(d))

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// 等待 Run 进入 handle（首轮已起，阻塞在 release）
	select {
	case <-entered:
	case <-time.After(testTimeout):
		t.Fatal("超时：Run 未进入 handle")
	}

	cancel()
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ctx 取消后 Run 应返回 nil，实际 %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("超时：cancel 后 Run 未返回")
	}
}

// TestBaseWorker_RunOnce_RoundEvents 轮次事件时序与载荷：
// 正常轮恰好 Started→Completed 各一次，Completed 携带 Name/Duration/Err（handle 返回值）；
// panic 轮有 Started 无 Completed。
func TestBaseWorker_RunOnce_RoundEvents(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 16)
	subscribeRound(d, ch)

	// 正常轮：handle 睡眠确保 Duration 有明确余量，返回 error 验证 Err 透传
	wantErr := errors.New("boom-err")
	w := NewBaseWorker("base-events", func(ctx context.Context) error {
		time.Sleep(2 * time.Millisecond)
		return wantErr
	}, WithBaseDispatcher(d))
	w.RunOnce(context.Background())

	s := waitRound(t, ch, EventRoundStarted)
	if s.payload.Name != "base-events" || s.payload.Duration != 0 || s.payload.Err != nil {
		t.Fatalf("RoundStarted 载荷不符（Duration/Err 应为零值）：%+v", s.payload)
	}
	c := waitRound(t, ch, EventRoundCompleted)
	if c.payload.Name != "base-events" {
		t.Fatalf("RoundCompleted.Name 不符：%+v", c.payload)
	}
	if !errors.Is(c.payload.Err, wantErr) {
		t.Fatalf("RoundCompleted.Err 应为 handle 返回值，实际 %v", c.payload.Err)
	}
	if c.payload.Duration < 2*time.Millisecond {
		t.Fatalf("RoundCompleted.Duration 应不小于 handle 内睡眠 2ms，实际 %v", c.payload.Duration)
	}

	// panic 轮：有 Started 无 Completed（同步派发，RunOnce 返回后事件已全部送达，
	// 剩余事件应为空）
	w2 := NewBaseWorker("base-events-panic", func(ctx context.Context) error {
		panic("kaboom")
	}, WithBaseDispatcher(d))
	w2.RunOnce(context.Background())

	s2 := waitRound(t, ch, EventRoundStarted)
	if s2.payload.Name != "base-events-panic" {
		t.Fatalf("panic 轮 RoundStarted.Name 不符：%+v", s2.payload)
	}
	if rest := drainRounds(ch); len(rest) != 0 {
		t.Fatalf("panic 轮不应派发 Completed，实际收到 %v", rest)
	}
}

// TestBaseWorker_RunOnce_BeforeRoundSkip beforeRound 返回 false 跳过该轮：
// 无事件派发、handle 不执行、afterRound 不调。
func TestBaseWorker_RunOnce_BeforeRoundSkip(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 16)
	subscribeRound(d, ch)

	handleCalled, afterCalled := false, false
	w := NewBaseWorker("base-skip", func(ctx context.Context) error {
		handleCalled = true
		return nil
	}, WithBaseDispatcher(d))
	w.beforeRound = func(ctx context.Context) bool { return false }
	w.afterRound = func(ctx context.Context) { afterCalled = true }

	w.RunOnce(context.Background())

	if handleCalled {
		t.Fatal("跳过轮 handle 不应执行")
	}
	if afterCalled {
		t.Fatal("跳过轮不应调 afterRound")
	}
	// 同步派发：跳过轮不产生任何轮次事件，channel 应为空
	if rest := drainRounds(ch); len(rest) != 0 {
		t.Fatalf("跳过轮不应派发事件，实际收到 %v", rest)
	}
}

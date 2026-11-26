package worker

// ProcWorker panic 上抛语义回归用例：handle panic 不被底座保护壳吞掉轮转，
// 而是经 panicRound 钩子 re-panic 传播出 RunOnce/Run——由 WorkerManager 捕获，
// 派发 worker.Panicked/worker.Stopped 生命周期事件、该 worker 停止
// （worker-events 契约：ProcWorker 的 panic 归生命周期事件管，区别于
// PeriodicWorker/BaseWorker 的每轮 recover 继续轮转）。

import (
	"context"
	"testing"
)

// TestProcWorker_PanicPropagates handle panic 从 RunOnce 传播出来：
// 不需锁的裸 ProcWorker，直接调 RunOnce，defer recover 捕获并断言非 nil
// 且值为 handle 抛出的原始 panic 值。
func TestProcWorker_PanicPropagates(t *testing.T) {
	w := NewProcWorker("proc-panic-up", 0, func(ctx context.Context) error {
		panic("boom-up")
	})

	var got any
	func() {
		defer func() { got = recover() }()
		w.RunOnce(context.Background())
	}()

	if got == nil {
		t.Fatal("handle panic 应从 RunOnce re-panic 上抛，实际被吞掉")
	}
	if got != "boom-up" {
		t.Fatalf("上抛值应为 handle 原始 panic 值 %q，实际 %v", "boom-up", got)
	}
}

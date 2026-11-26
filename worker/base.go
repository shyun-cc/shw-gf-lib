package worker

import (
	"context"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/tracex"

	"github.com/gogf/gf/v2/frame/g"
)

// BaseWorker 裸循环底座（对应 hyperf AbstractLoopProcess 裸循环层）：
// 只做「逐轮执行 handle + panic 轮转 + 轮次事件 + 轮前后钩子」，不带间隔、
// 分布式锁与进程保护——那些是上层（PeriodicWorker/ProcWorker）叠加的策略。
// 适用阻塞型 handle（MQ 消费等自阻塞任务，每轮在 handle 内自然让出）；
// 非阻塞轮询 handle 在此会 busy-loop 空转，必须改用 PeriodicWorker。
type BaseWorker struct {
	name   string
	handle func(ctx context.Context) error

	// dispatcher 事件分发器注入：轮次事件（RoundStarted/RoundCompleted）经其派发，
	// nil 降级 events.Default()（选择语义统一收敛在 events.go 的 dispatchEvent 辅助）
	dispatcher *events.Dispatcher

	// 非导出钩子（嵌入者同包覆写，外部不可见；零值直通）
	beforeRound func(ctx context.Context) bool // 轮前钩子；返回 false 跳过该轮（不发事件）
	afterRound  func(ctx context.Context)      // 正常完成钩子（Completed 之后）
	// panicRound panic 收尾钩子（recover 日志之后），r 为 recover 捕获的 panic 值；
	// 钩子可 re-panic 上抛（上抛即中断轮转、传播出 RunOnce，如 ProcWorker 的策略）
	panicRound func(ctx context.Context, r any)
}

// 编译期断言：BaseWorker 实现 Worker 接口
var _ Worker = (*BaseWorker)(nil)

// BaseOption BaseWorker 配置选项
type BaseOption func(*BaseWorker)

// WithBaseDispatcher 注入独立事件分发器（测试或事件域隔离用；不注入降级 events.Default()）
func WithBaseDispatcher(d *events.Dispatcher) BaseOption {
	return func(w *BaseWorker) {
		w.dispatcher = d
	}
}

// NewBaseWorker 创建裸循环底座 Worker。
// handle 为每轮执行的函数；阻塞型 handle 配 Run 的无间隔循环使用。
func NewBaseWorker(name string, handle func(ctx context.Context) error, opts ...BaseOption) *BaseWorker {
	w := &BaseWorker{
		name:   name,
		handle: handle,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Name 任务名（Worker 接口实现）
func (w *BaseWorker) Name() string { return w.name }

// Run Worker 接口实现：无间隔死循环，逐轮调 RunOnce，ctx 取消返回 nil。
// 适用阻塞型 handle（MQ 消费等自阻塞任务）；非阻塞轮询 handle 在此会
// busy-loop 空转（轮间无任何等待、CPU 打满），必须改用 PeriodicWorker。
func (w *BaseWorker) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce 单轮保护壳，行为对齐 PeriodicWorker.runOnce，逐序：
//  1. 每轮独立 traceId（tracex.New），非 HTTP 场景的执行链路互不混淆；
//  2. panic 轮转：recover 落错误日志后继续下一轮，不 re-panic（panicRound 钩子在
//     日志之后、携带 panic 值；钩子 re-panic 则上抛中断轮转，如 ProcWorker 的上抛策略）；
//  3. beforeRound 返回 false 跳过该轮：无事件派发、不调 afterRound；
//  4. 库内起算本轮时长并派发 RoundStarted；
//  5. handle 返回 err 时落错误日志（不中断轮次收尾）；
//  6. 派发 RoundCompleted（携带测量时长与 handle 返回值）；
//  7. afterRound 收尾（Completed 之后）。
//
// panic 轮有 Started 无 Completed（panic 直接走 recover 兜底，不执行 Completed 派发）。
func (w *BaseWorker) RunOnce(ctx context.Context) {
	// 每轮独立 traceId：非 HTTP 场景的执行链路互不混淆
	ctx = tracex.New(ctx)

	defer func() {
		if r := recover(); r != nil {
			// panic 轮转：落日志继续，不 re-panic（嵌入者可经 panicRound 钩子 re-panic 上抛，如 ProcWorker）
			g.Log().Errorf(ctx, "Worker %s 执行 panic: %v", w.name, r)
			if w.panicRound != nil {
				w.panicRound(ctx, r)
			}
		}
	}()

	// 轮前钩子：返回 false 跳过该轮（无事件无 after）
	if w.beforeRound != nil && !w.beforeRound(ctx) {
		return
	}

	// 轮次事件：Started 派发后起算；handle panic 的轮不会执行下面的 Completed 派发
	// （panic 直接走 defer recover 兜底，该轮有 Started 无 Completed）
	start := time.Now()
	dispatchEvent(w.dispatcher, ctx, EventRoundStarted, RoundEvent{Name: w.name})

	err := w.handle(ctx)
	if err != nil {
		g.Log().Errorf(ctx, "Worker %s 执行失败: %v", w.name, err)
	}
	// err 日志之后统一派发 Completed：Duration 为库内测量的本轮执行时长（handle 前起算）
	dispatchEvent(w.dispatcher, ctx, EventRoundCompleted, RoundEvent{
		Name:     w.name,
		Duration: time.Since(start),
		Err:      err,
	})

	if w.afterRound != nil {
		w.afterRound(ctx)
	}
}

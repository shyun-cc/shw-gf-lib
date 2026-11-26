package worker

import (
	"context"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
)

// worker 链路事件体系：事件名常量 + 载荷结构体 + 共享派发辅助。
// 事件名与载荷字段是对外契约（specs/worker-events），逐字不得偏离；
// manager / periodic / proc 各埋点统一经 dispatchEvent 派发，
// 注入实例与全局 Default() 的选择语义在此收敛为一份。

const (
	// EventWorkerStarted 事件名：WorkerManager 启动完成，载荷 WorkerStartedEvent
	EventWorkerStarted = "worker.Started"

	// EventRoundStarted 事件名：单个 worker 一轮执行开始，载荷 RoundEvent（Duration/Err 为零值）
	EventRoundStarted = "worker.RoundStarted"

	// EventRoundCompleted 事件名：单个 worker 一轮执行结束，载荷 RoundEvent（携带测量时长与 handle 返回值）
	EventRoundCompleted = "worker.RoundCompleted"

	// EventWorkerPanicked 事件名：单个 worker 的 Run 内 panic 被 manager recover，载荷 WorkerPanickedEvent
	EventWorkerPanicked = "worker.Panicked"

	// EventWorkerStopped 事件名：单个 worker 退出（Run 返回），载荷 WorkerStoppedEvent
	EventWorkerStopped = "worker.Stopped"
)

// WorkerStartedEvent EventWorkerStarted 的载荷：实际启动的 worker 名单
type WorkerStartedEvent struct {
	// Names 实际启动的 worker 名单（Name() 去重后的集合）
	Names []string
}

// RoundEvent EventRoundStarted / EventRoundCompleted 的载荷：单个 worker 的一轮执行。
// RoundStarted 时 Duration / Err 为零值；RoundCompleted 时携带库内测量的本轮时长与 handle 返回值。
type RoundEvent struct {
	// Name worker 名
	Name string
	// Duration 本轮执行时长（库内测量，RoundStarted 派发时为零值）
	Duration time.Duration
	// Err handle 返回值（RoundStarted 派发时为 nil）
	Err error
}

// WorkerPanickedEvent EventWorkerPanicked 的载荷：worker Run 内 panic 被 manager recover
type WorkerPanickedEvent struct {
	// Name worker 名
	Name string
	// Recovered recover 捕获的 panic 值
	Recovered any
}

// WorkerStoppedEvent EventWorkerStopped 的载荷：worker 的 Run 返回
type WorkerStoppedEvent struct {
	// Name worker 名
	Name string
	// Err Run 返回值，nil 表示正常退出
	Err error
}

// dispatchEvent worker 链路事件的共享派发辅助（manager/periodic/proc 埋点统一走这里）：
// d 非 nil 经注入实例派发，d 为 nil 降级 events.Dispatch（进程级 Default()）。
// 该降级形态照抄 sms 包 sendWithEvents 内 dispatch 闭包的先例（见 sms/sms.go:152-159）。
func dispatchEvent(d *events.Dispatcher, ctx context.Context, name string, payload any) {
	if d != nil {
		d.Dispatch(ctx, name, payload)
		return
	}
	events.Dispatch(ctx, name, payload)
}

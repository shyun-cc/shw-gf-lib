package task

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Execution 是 Handler 的运行上下文，Progress 的校验由 Store 执行。
type Execution struct {
	Lease *Lease
	Store *Store
}

func (e *Execution) Progress(ctx context.Context, value int, checkpoint []byte, fn Audit) error {
	return e.Store.Progress(ctx, e.Lease, value, checkpoint, fn)
}

// ManagedHandler 必须遵守取消，外部副作用仍需项目幂等与自动收敛。
type ManagedHandler func(context.Context, *Execution) (Completion, error)

// Executor 每个 RunOnce 执行一项；并发可由多个 goroutine 调用，数据库按 Instance.Capacity 限制。
// Handlers 的键是 Type，Version 必须与持久化能力声明一致。
type HandlerRegistration struct {
	Version string
	Handler ManagedHandler
}
type Executor struct {
	TraceID    func(context.Context) string // 可选，由项目提供本轮 trace 标识
	Store      *Store
	InstanceID string
	Handlers   map[string]HandlerRegistration
}

func (e *Executor) RunOnce(ctx context.Context, id string) (bool, error) {
	if e.Store == nil || e.InstanceID == "" {
		return false, fmt.Errorf("task: executor not configured")
	}
	if err := e.Store.HeartbeatInstance(ctx, e.InstanceID); err != nil {
		return false, err
	}
	traceID := ""
	if e.TraceID != nil {
		traceID = e.TraceID(ctx)
	}
	lease, err := e.Store.Claim(ctx, e.InstanceID, id, traceID)
	if err != nil || lease == nil {
		return false, err
	}
	h, ok := e.Handlers[lease.Run.Type]
	if !ok || h.Handler == nil || h.Version != lease.Run.Definition.HandlerVersion {
		err = e.Store.Complete(ctx, lease, Completion{Outcome: "unknown", ErrorCode: "handler_unavailable", ErrorSummary: "executor handler capability mismatch"}, nil)
		if err != nil {
			return true, err
		}
		return true, fmt.Errorf("task: handler unavailable")
	}
	// Handler 上下文携带本次 attempt 的 trace 标识（AttemptTraceID；合法 OTel traceId 时同时注入 SpanContext）。
	runCtx, cancel := context.WithCancel(withAttempt(ctx, lease))
	defer cancel()
	if lease.Deadline != nil {
		var deadlineCancel context.CancelFunc
		runCtx, deadlineCancel = context.WithTimeout(runCtx, lease.Deadline.Sub(e.Store.now()))
		defer deadlineCancel()
	}
	type result struct {
		c   Completion
		err error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if recover() != nil {
				r = result{Completion{Outcome: "unknown", ErrorCode: "handler_panic", ErrorSummary: "handler panicked"}, fmt.Errorf("task: handler panic")}
			}
			done <- r
		}()
		r.c, r.err = h.Handler(runCtx, &Execution{lease, e.Store})
	}()
	interval := lease.Run.Definition.Lease / 3
	if interval > 10*time.Second {
		interval = 10 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	finish := func(c Completion) error {
		writeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		return e.Store.Complete(writeCtx, lease, c, nil)
	}
	for {
		select {
		case r := <-done:
			if r.err != nil {
				if r.c.Outcome == "" || r.c.Outcome == OutcomeSuccess || r.c.Outcome == OutcomePartial || r.c.Outcome == OutcomeSkipped {
					r.c.Outcome = "failed"
				}
				if r.c.ErrorCode == "" {
					r.c.ErrorCode = "handler_error"
				}
				if r.c.ErrorSummary == "" {
					r.c.ErrorSummary = "handler returned an error"
				}
			}
			if r.c.Outcome == "" {
				r.c.Outcome = "success"
			}
			if err := finish(r.c); err != nil {
				return true, err
			}
			return true, r.err
		case <-tick.C:
			beatCtx, stop := context.WithTimeout(runCtx, lease.Run.Definition.Lease/3)
			err := e.Store.Renew(beatCtx, lease)
			if err == nil {
				err = e.Store.HeartbeatInstance(beatCtx, e.InstanceID)
			}
			stop()
			if errors.Is(err, ErrCancelRequested) {
				// 协作取消：租约仍有效，取消 Handler 后由 runCtx.Done 分支写回（落为 cancelled）。
				cancel()
				continue
			}
			if err != nil {
				cancel()
				return true, fmt.Errorf("%w: renewal failed", ErrLeaseLost)
			}
		case <-runCtx.Done():
			cancel()
			if runCtx.Err() == context.DeadlineExceeded {
				recoverCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_, _, err := e.Store.Recover(recoverCtx, 100)
				stop()
				if err != nil {
					return true, err
				}
				return true, context.DeadlineExceeded
			}
			err := finish(Completion{Outcome: "interrupted", ErrorCode: "cancelled", ErrorSummary: "executor context cancelled"})
			if err != nil {
				return true, err
			}
			return true, runCtx.Err()
		}
	}
}

// Run 单并发执行循环；立即扫描，短轮询只唤醒持久化任务，不决定周期时点。
func (e *Executor) Run(ctx context.Context, poll time.Duration) error {
	if e.Store == nil || e.InstanceID == "" {
		return fmt.Errorf("task: executor not configured")
	}
	if poll <= 0 || poll > 10*time.Second {
		return fmt.Errorf("task: poll interval must be (0,10s]")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = e.Store.StopInstance(c, e.InstanceID)
	}()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		worked, err := e.RunOnce(ctx, "")
		if err != nil {
			return err
		}
		if worked {
			continue
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

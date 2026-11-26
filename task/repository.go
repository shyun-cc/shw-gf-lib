package task

import (
	"context"
	"time"
)

// Repository 任务仓储接口（对齐 PHP TaskRepositoryInterface；PG 实现见 pg.go，项目可自实现）
type Repository interface {
	Find(ctx context.Context, id string) (*Task, error)
	Save(ctx context.Context, t *Task) error // ID 零值（空串）插入，否则更新

	// Claim 抢占一个到期 pending 任务（FOR UPDATE SKIP LOCKED 语义：并发仅一个成功）；
	// 抢到转 running；无任务返回 nil
	Claim(ctx context.Context, workerID string) (*Task, error)

	// CreateAttempt 为任务建执行租约（attempt_no = attempts 递增）
	CreateAttempt(ctx context.Context, t *Task, workerID string) (*TaskAttempt, error)
	// FindActiveAttempt 任务当前 running 的 attempt（无则 nil）
	FindActiveAttempt(ctx context.Context, taskID string) (*TaskAttempt, error)

	// MarkAttemptSucceeded 租约校验回写：attempt 仍 running 时置 succeeded + task 同步；返回 false=已被回收/已回写
	MarkAttemptSucceeded(ctx context.Context, a *TaskAttempt, result []byte) (bool, error)
	// MarkAttemptFailed 同上置 failed + 记 error
	MarkAttemptFailed(ctx context.Context, a *TaskAttempt, errMsg string) (bool, error)
	// MarkTimeout attempt 置 timeout（超时回收）
	MarkTimeout(ctx context.Context, a *TaskAttempt) error

	// FindTimedOutTasks running 超过 timeout 的任务（timeout>0）
	FindTimedOutTasks(ctx context.Context, limit int) ([]*Task, error)
	// FindFailedTasks 失败且 available_at 已到、未达 max_attempts 的任务（RetryScan 重试用）
	FindFailedTasks(ctx context.Context, limit int) ([]*Task, error)
	// FindExhaustedFailedTasks 失败且已达 max_attempts 的任务（RetryScan 转死用）
	FindExhaustedFailedTasks(ctx context.Context, limit int) ([]*Task, error)
	// FindStuckAttempts running 超 timeout 的 attempt（RetryScan 先回收）
	FindStuckAttempts(ctx context.Context, limit int) ([]*TaskAttempt, error)

	// RescheduleToPending failed→pending（attempts 已递增、available_at 已按退避推进）——仅 RetryScanner 调用
	RescheduleToPending(ctx context.Context, t *Task, availableAt time.Time) error
	// MarkDead 达 max_attempts 转 dead
	MarkDead(ctx context.Context, taskID string) error

	// BatchInsertChildren 父子拆分批量建子任务（继承由 Splitter 保证，本方法只插入）
	BatchInsertChildren(ctx context.Context, parent *Task, payloads [][]byte) ([]*Task, error)
	// AggregateChildStatus 子任务状态聚合（父状态查询时派生）
	AggregateChildStatus(ctx context.Context, parentID string) (*Aggregate, error)
}

// AtomicRepository 是旧 Worker 的可选增强接口，PG 实现默认提供。
type AtomicRepository interface {
	ClaimExecution(context.Context, string, string) (*Task, *TaskAttempt, error)
	ScanRecovery(context.Context, *ExponentialBackoffRetryPolicy, func(context.Context, *Task)) (int, int, int, error)
}

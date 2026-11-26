package task

import (
	"context"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

// Handler 任务执行器（项目实现业务逻辑）
type Handler func(ctx context.Context, t *Task) ([]byte, error)

// Dispatcher 提交与执行编排
type Dispatcher struct {
	Repo     Repository
	Producer QueueProducer // nil 时按 Null
	Retry    *ExponentialBackoffRetryPolicy
	// QueueTypes type→queue 模式注册表（nil 视为空：所有 type 仅按 ViaQueue 逐次决定）。
	// type 注册后 Submit 一律发消息；与 RetryScanner.QueueTypes 共用同一实例
	QueueTypes *TypeRegistry
}

// NewDispatcher 构造（producer nil → Null）
func NewDispatcher(repo Repository, producer QueueProducer) *Dispatcher {
	if producer == nil {
		producer = &NullProducer{}
	}
	return &Dispatcher{Repo: repo, Producer: producer, Retry: NewDefaultRetryPolicy()}
}

// Submit 提交任务：save 后决定是否发消息——ViaQueue 逐次指定，或 type 已注册
// queue 模式（QueueTypes.IsQueue），任一触发即发；两者皆否为 pull 模式不发
func (d *Dispatcher) Submit(ctx context.Context, opts CreationOptions) (*Task, error) {
	t := &Task{
		TaskNo: opts.TaskNo, Type: opts.Type, Payload: opts.Payload,
		Status: StatusPending, Priority: opts.Priority, AvailableAt: opts.AvailableAt,
		MaxAttempts: opts.MaxAttempts, Timeout: opts.Timeout, BizExt: opts.BizExt,
	}
	if t.MaxAttempts <= 0 {
		t.MaxAttempts = 3
	}
	if err := d.Repo.Save(ctx, t); err != nil {
		return nil, err
	}
	registered := d.QueueTypes != nil && d.QueueTypes.IsQueue(opts.Type)
	if opts.ViaQueue || registered {
		if err := d.Producer.Publish(ctx, t.ID); err != nil {
			return t, fmt.Errorf("task: 队列投递失败（任务已落表不回滚；注册为 queue 模式的 type 将由重试重投拉起，否则需 pull worker 或人工处置）: %w", err)
		}
		if opts.ViaQueue && !registered {
			// ViaQueue 是提交期一次性标志：本次发消息，但未注册的 type 重试不重投
			g.Log().Warningf(ctx, "task: type=%s 提交带 ViaQueue 但未注册 queue 模式（TypeRegistry.Register），重试不重投队列，需 pull worker 或人工处置", opts.Type)
		}
	}
	return t, nil
}

// SplitAndSubmit 父子拆分提交：建父（is_split）+ 批量子（继承父五字段）
func (d *Dispatcher) SplitAndSubmit(ctx context.Context, opts CreationOptions, payloads [][]byte) (*Task, []*Task, error) {
	// 父任务不逐次指定队列（未注册 type 时不发消息；type 已注册 queue 模式则 Submit 按注册表照发）；
	// 子任务由 pull/RetryScan 拉起
	opts.ViaQueue = false
	parent, err := d.Submit(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	children, err := d.Repo.BatchInsertChildren(ctx, parent, payloads)
	if err != nil {
		return parent, nil, err
	}
	return parent, children, nil
}

// Worker 执行器：四模式的公共底座（单例拉取/并发拉取走 RunOnce 循环；
// AMQP/Kafka 即时消费的项目 consumer 调 ExecuteByID）
type Worker struct {
	Name    string
	Repo    Repository
	Handler Handler
}

// RunOnce 拉取一轮：claim → 建租约 → 执行 → 回写（成功/失败）
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if repo, ok := w.Repo.(AtomicRepository); ok {
		t, a, err := repo.ClaimExecution(ctx, "", w.Name)
		if err != nil || t == nil {
			return false, err
		}
		return true, w.executeAttempt(ctx, t, a)
	}
	t, err := w.Repo.Claim(ctx, w.Name)
	if err != nil {
		return false, err
	}
	if t == nil {
		return false, nil
	}
	return true, w.executeTask(ctx, t)
}

// ExecuteByID 按 ID 执行（即时消费模式入口：consummer 收到 task_id 后调用）
func (w *Worker) ExecuteByID(ctx context.Context, taskID string) error {
	if repo, ok := w.Repo.(AtomicRepository); ok {
		t, a, err := repo.ClaimExecution(ctx, taskID, w.Name)
		if err != nil || t == nil {
			return err
		}
		return w.executeAttempt(ctx, t, a)
	}
	t, err := w.Repo.Find(ctx, taskID)
	if err != nil || t == nil {
		return err
	}
	if t.Status != StatusPending {
		return nil // 非待执行态幂等忽略
	}
	// 自定义旧 Repository 的兼容路径；并发保证由实现者承担。
	t.Status = StatusRunning
	t.Attempts++
	if err := w.Repo.Save(ctx, t); err != nil {
		return err
	}
	return w.executeTask(ctx, t)
}

func (w *Worker) executeTask(ctx context.Context, t *Task) error {
	attempt, err := w.Repo.CreateAttempt(ctx, t, w.Name)
	if err != nil {
		return err
	}
	return w.executeAttempt(ctx, t, attempt)
}

func (w *Worker) executeAttempt(ctx context.Context, t *Task, attempt *TaskAttempt) error {
	if t.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(t.Timeout)*time.Second)
		defer cancel()
	}
	result, execErr := w.Handler(ctx, t)
	if execErr == nil && ctx.Err() != nil {
		execErr = ctx.Err()
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	ctx = writeCtx
	if execErr != nil {
		if ok, err := w.Repo.MarkAttemptFailed(ctx, attempt, execErr.Error()); err != nil {
			return err
		} else if !ok {
			return ErrLeaseLost
		}
		return execErr
	}
	if ok, err := w.Repo.MarkAttemptSucceeded(ctx, attempt, result); err != nil {
		return err
	} else if !ok {
		return ErrLeaseLost
	}
	return nil
}

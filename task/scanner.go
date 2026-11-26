package task

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

// RetryScanner 重试独占扫描：重试只由本扫描触发（执行器崩溃不会导致重试失效）。
// 顺序：① 先回收 stuck（超时 running 的 attempt → timeout + task failed + 退避）；
// ② 再扫描 failed 到期任务：达 max_attempts 转 dead，否则按退避推 available_at 转回 pending；
// ③ 转回 pending 后，注册为 queue 模式的 type 经 Producer 重投消息拉起即时消费。
type RetryScanner struct {
	Repo  Repository
	Retry *ExponentialBackoffRetryPolicy
	// QueueTypes type→queue 模式注册表（nil 视为空：重试一律不重投）。
	// 与 Dispatcher.QueueTypes 共用同一实例——注册的 type 重试后重投
	QueueTypes *TypeRegistry
	// Producer 重投用队列生产者（nil 按 Null；与 Dispatcher.Producer 用同一项目实现）
	Producer QueueProducer
}

// NewRetryScanner 构造
func NewRetryScanner(repo Repository) *RetryScanner {
	return &RetryScanner{Repo: repo, Retry: NewDefaultRetryPolicy()}
}

// Scan 执行一轮扫描，返回 (回收数, 重试数, 死亡数, error)
func (s *RetryScanner) Scan(ctx context.Context) (stuck, retried, dead int, err error) {
	if repo, ok := s.Repo.(AtomicRepository); ok {
		return repo.ScanRecovery(ctx, s.Retry, s.republish)
	}
	// ① stuck attempt 回收
	stucks, err := s.Repo.FindStuckAttempts(ctx, 100)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, a := range stucks {
		if err := s.Repo.MarkTimeout(ctx, a); err != nil {
			g.Log().Errorf(ctx, "task: markTimeout 失败 attempt=%s: %v", a.ID, err)
			continue
		}
		// 对应任务置 failed 并按退避推 available_at（若未达上限）
		t, err := s.Repo.Find(ctx, a.TaskID)
		if err != nil || t == nil {
			continue
		}
		if s.Retry.ShouldRetry(t) {
			next := s.Retry.NextAvailableAt(t, time.Now())
			if err := s.recoverToFailed(ctx, t, next); err != nil {
				continue
			}
			retried++
		} else {
			if err := s.Repo.MarkDead(ctx, t.ID); err == nil {
				dead++
			}
		}
		stuck++
	}

	// ② 先把已达上限的 failed 转死
	exhausted, err := s.Repo.FindExhaustedFailedTasks(ctx, 100)
	if err != nil {
		return stuck, retried, dead, err
	}
	for _, t := range exhausted {
		if err := s.Repo.MarkDead(ctx, t.ID); err == nil {
			dead++
		}
	}

	// ③ failed 到期未达上限：按退避转回 pending；注册为 queue 模式的 type 重投消息
	failed, err := s.Repo.FindFailedTasks(ctx, 100)
	if err != nil {
		return stuck, retried, dead, err
	}
	for _, t := range failed {
		if !s.Retry.ShouldRetry(t) {
			continue // ②已处理
		}
		next := s.Retry.NextAvailableAt(t, time.Now())
		if err := s.Repo.RescheduleToPending(ctx, t, next); err != nil {
			g.Log().Errorf(ctx, "task: 重试转 pending 失败 task=%s: %v", t.ID, err)
			continue
		}
		s.republish(ctx, t)
		retried++
	}
	return stuck, retried, dead, nil
}

// republish 重试重投：注册为 queue 模式的 type 转回 pending 后重新发消息拉起即时消费。
// stuck 回收路径（recoverToFailed 置 failed）下轮走③，同样经此重投，无需在①重复。
// 重投失败（D8）：任务保持 pending——不回滚状态、不重试 Publish，仅记日志；
// pull worker 仍可拉取，纯 queue 项目需人工处置
func (s *RetryScanner) republish(ctx context.Context, t *Task) {
	if s.QueueTypes == nil || !s.QueueTypes.IsQueue(t.Type) {
		return
	}
	p := s.Producer
	if p == nil {
		p = &NullProducer{}
	}
	if err := p.Publish(ctx, t.ID); err != nil {
		g.Log().Errorf(ctx, "task: 重试重投失败（任务已转 pending 不回滚，需 pull worker 或人工处置）task=%s: %v", t.ID, err)
	}
}

// recoverToFailed 超时回收后任务先置 failed（等下轮 RetryScan 或本轮②重试）
func (s *RetryScanner) recoverToFailed(ctx context.Context, t *Task, availableAt time.Time) error {
	_, err := g.DB().Exec(ctx,
		`UPDATE task SET status = ?, available_at = ?, last_error = '执行超时', updated_at = NOW() WHERE id = ?`,
		StatusFailed, availableAt, t.ID)
	return err
}

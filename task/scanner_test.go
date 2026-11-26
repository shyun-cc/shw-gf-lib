package task

import (
	"context"
	"testing"
	"time"
)

// —— memRepo 扩展 scanner ③路径所需方法（①②路径返回空，不触 g.DB，纯逻辑可测）——

// FindStuckAttempts 无 stuck（不走①的 g.DB 回收路径）
func (r *memRepo) FindStuckAttempts(_ context.Context, _ int) ([]*TaskAttempt, error) {
	return nil, nil
}

// FindExhaustedFailedTasks 无已达上限任务
func (r *memRepo) FindExhaustedFailedTasks(_ context.Context, _ int) ([]*Task, error) {
	return nil, nil
}

// FindFailedTasks 返回内存中全部 failed 任务（等价到期：测试不涉时间过滤）
func (r *memRepo) FindFailedTasks(_ context.Context, _ int) ([]*Task, error) {
	var out []*Task
	for _, t := range r.tasks {
		if t.Status == StatusFailed {
			out = append(out, t)
		}
	}
	return out, nil
}

// RescheduleToPending 内存转回 pending（对齐 pg 语义：状态+available_at 推进）
func (r *memRepo) RescheduleToPending(_ context.Context, t *Task, availableAt time.Time) error {
	t.Status = StatusPending
	t.AvailableAt = availableAt
	r.tasks[t.ID] = t
	return nil
}

// newFailedTask 预置一条到期 failed 任务（attempts=1 max=3，ShouldRetry=true）
func newFailedTask(typ string) *Task {
	return &Task{ID: "f-" + typ, Type: typ, Status: StatusFailed, Attempts: 1, MaxAttempts: 3}
}

// TestScan_RepublishRegistered 注册 type 的 failed 任务转回 pending 后重投恰一次
func TestScan_RepublishRegistered(t *testing.T) {
	reg := NewTypeRegistry()
	reg.Register("job")
	repo := newMemRepo()
	tk := newFailedTask("job")
	repo.tasks[tk.ID] = tk
	prod := &countingProducer{}

	s := NewRetryScanner(repo)
	s.QueueTypes = reg
	s.Producer = prod

	_, retried, _, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if retried != 1 {
		t.Fatalf("应重试 1 个: %d", retried)
	}
	if tk.Status != StatusPending {
		t.Fatalf("任务应转回 pending: %s", tk.Status)
	}
	if len(prod.published) != 1 || prod.published[0] != tk.ID {
		t.Fatalf("重投应恰发一条 task_id 消息: %v", prod.published)
	}
}

// TestScan_NoRepublishUnregistered 未注册 type 转回 pending 但不重投
// （QueueTypes nil 与注册了其他 type 两种情形均不投）
func TestScan_NoRepublishUnregistered(t *testing.T) {
	repo := newMemRepo()
	tk := newFailedTask("pulljob")
	repo.tasks[tk.ID] = tk
	prod := &countingProducer{}

	// QueueTypes 未设（nil 视为空）
	s := NewRetryScanner(repo)
	s.Producer = prod
	if _, _, _, err := s.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tk.Status != StatusPending {
		t.Fatalf("任务应转回 pending: %s", tk.Status)
	}
	if len(prod.published) != 0 {
		t.Fatalf("QueueTypes nil 不应重投: %v", prod.published)
	}

	// 显式注册表但只注册了别的 type
	reg := NewTypeRegistry()
	reg.Register("other")
	tk2 := newFailedTask("pulljob")
	repo.tasks[tk2.ID] = tk2
	s2 := NewRetryScanner(repo)
	s2.QueueTypes = reg
	s2.Producer = prod
	if _, _, _, err := s2.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(prod.published) != 0 {
		t.Fatalf("未注册 type 不应重投: %v", prod.published)
	}
}

// TestScan_RepublishFailNotRolledBack 重投失败（D8）：任务保持 pending、
// Publish 仅尝试一次——不回滚状态、不重试 Publish
func TestScan_RepublishFailNotRolledBack(t *testing.T) {
	reg := NewTypeRegistry()
	reg.Register("job")
	repo := newMemRepo()
	tk := newFailedTask("job")
	repo.tasks[tk.ID] = tk
	prod := &failProducer{}

	s := NewRetryScanner(repo)
	s.QueueTypes = reg
	s.Producer = prod

	_, retried, _, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if retried != 1 {
		t.Fatalf("重投失败不应影响重试计数: %d", retried)
	}
	if tk.Status != StatusPending {
		t.Fatalf("重投失败不回滚，任务应保持 pending: %s", tk.Status)
	}
	if prod.calls != 1 {
		t.Fatalf("Publish 应仅尝试一次不重试: %d", prod.calls)
	}
}

// TestScan_PullTypeRetryCountUnchanged 重投不干扰重试主流程：未注册 type
// 转回 pending 后 retried 计数与状态推进正常
func TestScan_PullTypeRetryCountUnchanged(t *testing.T) {
	repo := newMemRepo()
	tk := newFailedTask("pulljob")
	repo.tasks[tk.ID] = tk

	s := NewRetryScanner(repo) // 不设 QueueTypes/Producer：纯 pull 项目形态
	_, retried, dead, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if retried != 1 || dead != 0 {
		t.Fatalf("pull 重试主流程应不受影响: retried=%d dead=%d", retried, dead)
	}
	if tk.Status != StatusPending {
		t.Fatalf("任务应转回 pending: %s", tk.Status)
	}
}

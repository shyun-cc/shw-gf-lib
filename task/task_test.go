package task

import (
	"context"
	"testing"
	"time"
)

// TestBackoffThreeSteps 退避三档：attempts=1/2/3 → 5/10/20s（指数取 attempts-1）
func TestBackoffThreeSteps(t *testing.T) {
	p := NewDefaultRetryPolicy()
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, 5 * time.Second},
		{2, 10 * time.Second},
		{3, 20 * time.Second},
		{0, 5 * time.Second}, // 边界：attempts-1<0 取 0
	}
	for _, c := range cases {
		got := p.NextBackoff(&Task{Attempts: c.attempts, MaxAttempts: 10})
		if got != c.want {
			t.Errorf("attempts=%d: backoff=%v want %v", c.attempts, got, c.want)
		}
	}
}

// TestBackoffCap 封顶 3600s
func TestBackoffCap(t *testing.T) {
	p := NewDefaultRetryPolicy()
	if got := p.NextBackoff(&Task{Attempts: 20}); got != 3600*time.Second {
		t.Fatalf("应封顶 3600s，实际 %v", got)
	}
}

// TestShouldRetry 达上限不再重试
func TestShouldRetry(t *testing.T) {
	p := NewDefaultRetryPolicy()
	if !p.ShouldRetry(&Task{Attempts: 2, MaxAttempts: 3}) {
		t.Fatal("2<3 应重试")
	}
	if p.ShouldRetry(&Task{Attempts: 3, MaxAttempts: 3}) {
		t.Fatal("3>=3 不应重试")
	}
}

// countingProducer 计数 fake（队列消息取证）
type countingProducer struct{ published []string }

func (p *countingProducer) Publish(_ context.Context, taskID string) error {
	p.published = append(p.published, taskID)
	return nil
}

// memRepo 内存仓储（拆分继承与提交模式取证用——仅实现用到的子集）
type memRepo struct {
	Repository
	tasks  map[string]*Task
	nextID int
}

func newMemRepo() *memRepo { return &memRepo{tasks: map[string]*Task{}} }

func (r *memRepo) Save(_ context.Context, t *Task) error {
	if t.ID == "" {
		r.nextID++
		t.ID = "id-" + itoa(r.nextID) // 测试替身用任意 string 形态即可
	}
	r.tasks[t.ID] = t
	return nil
}

func (r *memRepo) BatchInsertChildren(ctx context.Context, parent *Task, payloads [][]byte) ([]*Task, error) {
	// 复用 PgRepository 的继承语义：这里手写同规则
	var out []*Task
	for i, p := range payloads {
		child := &Task{
			TaskNo: parent.TaskNo + "-c" + itoa(i+1), Type: parent.Type, Payload: p,
			Status: StatusPending, Priority: parent.Priority, MaxAttempts: parent.MaxAttempts,
			Timeout: parent.Timeout, ParentID: &parent.ID, BizExt: parent.BizExt,
		}
		if err := r.Save(ctx, child); err != nil {
			return nil, err
		}
		out = append(out, child)
	}
	parent.IsSplit = true
	r.tasks[parent.ID] = parent
	return out, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestSubmit_QueueVsPull queue 模式发消息恰一次；pull 模式零消息
func TestSubmit_QueueVsPull(t *testing.T) {
	prod := &countingProducer{}
	d := NewDispatcher(newMemRepo(), prod)
	ctx := context.Background()

	qt, err := d.Submit(ctx, CreationOptions{Type: "job", ViaQueue: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(prod.published) != 1 || prod.published[0] != qt.ID {
		t.Fatalf("queue 模式应恰发一条 task_id 消息: %v", prod.published)
	}

	pt, err := d.Submit(ctx, CreationOptions{Type: "job", ViaQueue: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(prod.published) != 1 {
		t.Fatalf("pull 模式不应发消息: %v", prod.published)
	}
	if pt.Status != StatusPending {
		t.Fatalf("提交后应 pending: %s", pt.Status)
	}
}

// TestSplitInheritance 子任务继承父 type/priority/max_attempts/timeout/biz_ext
func TestSplitInheritance(t *testing.T) {
	d := NewDispatcher(newMemRepo(), nil)
	ctx := context.Background()

	parent, children, err := d.SplitAndSubmit(ctx, CreationOptions{
		TaskNo: "P1", Type: "import", Priority: 7, MaxAttempts: 5, Timeout: 120, BizExt: []byte(`{"k":"v"}`),
	}, [][]byte{[]byte(`{"i":1}`), []byte(`{"i":2}`), []byte(`{"i":3}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 3 {
		t.Fatalf("应 3 子: %d", len(children))
	}
	if !parent.IsSplit {
		t.Fatal("父应标记 is_split")
	}
	for _, c := range children {
		if c.Type != "import" || c.Priority != 7 || c.MaxAttempts != 5 || c.Timeout != 120 || string(c.BizExt) != `{"k":"v"}` {
			t.Fatalf("子任务继承不符: %+v", c)
		}
		if c.ParentID == nil || *c.ParentID != parent.ID {
			t.Fatalf("子任务 parent_id 不符: %+v", c)
		}
	}
	// 父状态不被子影响（仍 pending——聚合是查询时派生）
	if parent.Status != StatusPending {
		t.Fatalf("父行状态应保持 pending（聚合派生不回写）: %s", parent.Status)
	}
}

// TestNullProducer 空实现
func TestNullProducer(t *testing.T) {
	if err := (&NullProducer{}).Publish(context.Background(), "018f6b2e-0000-7000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
}

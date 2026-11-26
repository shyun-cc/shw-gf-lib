package amqp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
)

// memRepo 内存仓储（仅实现 ExecuteByID 路径用到的子集，其余嵌入接口 nil 兜底）
type memRepo struct {
	task.Repository
	tasks    map[string]*task.Task
	attempts map[string]*task.TaskAttempt
}

func newMemRepo() *memRepo {
	return &memRepo{tasks: map[string]*task.Task{}, attempts: map[string]*task.TaskAttempt{}}
}

func (r *memRepo) Find(_ context.Context, id string) (*task.Task, error) {
	t, ok := r.tasks[id]
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (r *memRepo) Save(_ context.Context, t *task.Task) error {
	cp := *t
	r.tasks[t.ID] = &cp
	return nil
}

func (r *memRepo) CreateAttempt(_ context.Context, t *task.Task, workerID string) (*task.TaskAttempt, error) {
	a := &task.TaskAttempt{ID: mustV7(), TaskID: t.ID, AttemptNo: t.Attempts, Status: task.AttemptRunning, WorkerID: workerID}
	r.attempts[a.ID] = a
	return a, nil
}

func (r *memRepo) MarkAttemptSucceeded(_ context.Context, a *task.TaskAttempt, result []byte) (bool, error) {
	a.Status = task.AttemptSucceeded
	a.Result = result
	r.tasks[a.TaskID].Status = task.StatusSucceeded
	return true, nil
}

func (r *memRepo) MarkAttemptFailed(_ context.Context, a *task.TaskAttempt, errMsg string) (bool, error) {
	a.Status = task.AttemptFailed
	a.Error = errMsg
	r.tasks[a.TaskID].Status = task.StatusFailed
	return true, nil
}

// fakeDelivery Delivery fake：Ack/Nack 计数取证
type fakeDelivery struct {
	body         []byte
	acks, nacks  int
	nackRequeues []bool
}

func (d *fakeDelivery) Body() []byte { return d.body }
func (d *fakeDelivery) Ack() error   { d.acks++; return nil }
func (d *fakeDelivery) Nack(requeue bool) error {
	d.nacks++
	d.nackRequeues = append(d.nackRequeues, requeue)
	return nil
}

// newWorker 建测试 Worker：calls 计数 handler 执行次数，execErr 控制成败
func newWorker(repo *memRepo, execErr error) (w *task.Worker, calls *int) {
	c := 0
	return &task.Worker{
		Name: "test-worker",
		Repo: repo,
		Handler: func(_ context.Context, _ *task.Task) ([]byte, error) {
			c++
			if execErr != nil {
				return nil, execErr
			}
			return []byte(`"ok"`), nil
		},
	}, &c
}

// seedPending 预置一个 pending 任务
func seedPending(repo *memRepo) *task.Task {
	tk := &task.Task{ID: mustV7(), Type: "job", Status: task.StatusPending, MaxAttempts: 3}
	repo.tasks[tk.ID] = tk
	return tk
}

// taskMsgBody 按契约构造消息载荷
func taskMsgBody(taskID string) []byte {
	b, _ := json.Marshal(message{TaskID: taskID})
	return b
}

// consumeLike 模拟 rabbitmq.Consumer 消费循环的处置链：骨架 handler 恒返回
// nil → applyPolicy 的 err==nil 分支一律 Ack。以 fakeDelivery 计数取证：
// Ack 恰一次、零 Nack——若将来 handler 破坏「恒 nil」契约，此处先于真
// broker 暴露。
func consumeLike(t *testing.T, h rabbitmq.HandlerFunc, d *fakeDelivery) {
	t.Helper()
	if err := h(context.Background(), d); err != nil {
		t.Fatalf("骨架 handler 应恒返回 nil（→ 消费侧 Ack）: %v", err)
	}
	d.Ack() // 模拟 applyPolicy 对 err==nil 的处置
	if d.acks != 1 || d.nacks != 0 {
		t.Fatalf("应恰 Ack 一次且零 Nack: acks=%d nacks=%d", d.acks, d.nacks)
	}
}

// TestHandlerSuccess 任务执行成功：handler 恒返回 nil（→ rabbitmq.Consumer
// 对 err==nil 一律 Ack），任务终态 succeeded
func TestHandlerSuccess(t *testing.T) {
	repo := newMemRepo()
	tk := seedPending(repo)
	w, calls := newWorker(repo, nil)
	h := NewHandler(w)

	consumeLike(t, h, &fakeDelivery{body: taskMsgBody(tk.ID)})
	if *calls != 1 {
		t.Fatalf("handler 应执行一次, 实际 %d", *calls)
	}
	if repo.tasks[tk.ID].Status != task.StatusSucceeded {
		t.Fatalf("任务应 succeeded, 实际 %s", repo.tasks[tk.ID].Status)
	}
}

// TestHandlerExecFailStillNil 任务执行失败：handler 仍返回 nil（骨架契约：
// 无论成败均 Ack，绝不 Nack/Requeue——失败补偿由 RetryScanner 独占），
// 任务终态 failed
func TestHandlerExecFailStillNil(t *testing.T) {
	repo := newMemRepo()
	tk := seedPending(repo)
	w, calls := newWorker(repo, context.DeadlineExceeded)
	h := NewHandler(w)

	consumeLike(t, h, &fakeDelivery{body: taskMsgBody(tk.ID)})
	if *calls != 1 {
		t.Fatalf("handler 应执行一次, 实际 %d", *calls)
	}
	if repo.tasks[tk.ID].Status != task.StatusFailed {
		t.Fatalf("任务应 failed, 实际 %s", repo.tasks[tk.ID].Status)
	}
}

// TestHandlerPoisonMessages 毒消息矩阵：非法 JSON / 缺 task_id / 非 uuid 形态
// 一律按成功返回（Ack 丢弃），不触达 ExecuteByID（attempts 零增长），循环不中断
func TestHandlerPoisonMessages(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"非法JSON截断", []byte(`{"task_id":`)},
		{"纯文本非JSON", []byte(`not json at all`)},
		{"空对象缺task_id", []byte(`{}`)},
		{"task_id为空串", []byte(`{"task_id":""}`)},
		{"非uuid形态", []byte(`{"task_id":"not-a-uuid"}`)},
		{"整数task_id", []byte(`{"task_id":123}`)},
		{"空body", []byte{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := newMemRepo()
			w, calls := newWorker(repo, nil)
			h := NewHandler(w)

			consumeLike(t, h, &fakeDelivery{body: c.body})
			if *calls != 0 {
				t.Fatalf("毒消息不得触达任务执行: handler 执行了 %d 次", *calls)
			}
			if len(repo.attempts) != 0 {
				t.Fatalf("毒消息不得产生执行租约: %d 条", len(repo.attempts))
			}
		})
	}
}

// TestHandlerRedeliveryIdempotent 重复投递幂等：非 pending 任务（已
// succeeded）重投 → ExecuteByID 幂等忽略（handler 不再执行、状态不变），
// 消息照常按成功返回（Ack）——at-least-once 投递安全
func TestHandlerRedeliveryIdempotent(t *testing.T) {
	repo := newMemRepo()
	w, calls := newWorker(repo, nil)
	h := NewHandler(w)

	// 第一次投递：执行成功
	tk := seedPending(repo)
	consumeLike(t, h, &fakeDelivery{body: taskMsgBody(tk.ID)})
	if *calls != 1 || repo.tasks[tk.ID].Status != task.StatusSucceeded {
		t.Fatalf("首次投递应执行成功: calls=%d status=%s", *calls, repo.tasks[tk.ID].Status)
	}

	// 重复投递同 task_id：幂等忽略，消息照常 Ack
	consumeLike(t, h, &fakeDelivery{body: taskMsgBody(tk.ID)})
	if *calls != 1 {
		t.Fatalf("非 pending 重复投递不应再执行, 实际执行 %d 次", *calls)
	}
	if repo.tasks[tk.ID].Status != task.StatusSucceeded {
		t.Fatalf("重复投递不得改变终态: %s", repo.tasks[tk.ID].Status)
	}
}

// TestNewConsumerAssemblesAlwaysAck 便捷装配：固定注入 AlwaysAck 策略
// （reflect 读私有字段取证——policy 私有，无公开访问面），队列名透传，
// 默认 worker 名对齐 rabbitmq 包形态。未连接 Client 上仅登记不订阅，安全。
func TestNewConsumerAssemblesAlwaysAck(t *testing.T) {
	client := rabbitmq.NewClient(rabbitmq.DefaultConfig()) // 不 Connect：NewConsumer 仅登记
	w, _ := newWorker(newMemRepo(), nil)

	cons := NewConsumer(client, "task.q", w)
	if cons == nil {
		t.Fatal("装配应返回消费者")
	}
	if cons.Name() != "rabbitmq-consumer:task.q" {
		t.Fatalf("默认消费者名形态不符: %s", cons.Name())
	}
	policy := reflect.ValueOf(cons).Elem().FieldByName("policy").Int()
	if policy != int64(rabbitmq.AlwaysAck) {
		t.Fatalf("骨架应固定 AlwaysAck 策略, 实际 %d", policy)
	}
}

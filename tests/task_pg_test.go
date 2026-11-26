//go:build integration

// task 真 PG 集成（change: add-task-package）。
//
// 覆盖终态三句：并发 claim 互斥、成功/失败回写、退避/超时/RetryScan/父子聚合。
// 环境要求：PG_DSN（缺失 Skip），如 postgres://user:pass@127.0.0.1:5432/testdb。
package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/task"
)

func setupTaskPG(t *testing.T) (context.Context, *task.PgRepository, func()) {
	t.Helper()
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("未设置 PG_DSN（如 postgres://user:pass@127.0.0.1:5432/testdb），跳过 task 集成")
	}
	node, err := parsePGDSN(dsn)
	if err != nil {
		t.Fatalf("解析 PG_DSN 失败: %v", err)
	}
	setPGConfig(t, node)
	ctx := context.Background()
	// DDL 建表（幂等）
	if _, err := g.DB().Exec(ctx, task.DDL); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return ctx, task.NewPgRepository(), func() {
		_, _ = g.DB().Exec(ctx, `DROP TABLE IF EXISTS task_attempt, task`)
	}
}

// TestPG_ConcurrentClaimExclusivity 并发 claim 同一 pending 仅一个成功
func TestPG_ConcurrentClaimExclusivity(t *testing.T) {
	ctx, repo, cleanup := setupTaskPG(t)
	defer cleanup()

	tsk := &task.Task{TaskNo: "c1", Type: "t", Status: task.StatusPending, MaxAttempts: 3}
	if err := repo.Save(ctx, tsk); err != nil {
		t.Fatal(err)
	}

	var claimed int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := repo.Claim(ctx, "worker-x")
			if err != nil {
				t.Error(err)
				return
			}
			if got != nil {
				atomic.AddInt32(&claimed, 1)
			}
		}()
	}
	wg.Wait()
	if claimed != 1 {
		t.Fatalf("并发 claim 应恰 1 个成功，实际 %d", claimed)
	}
}

// TestPG_AttemptRoundTrip 成功回写：task 与 attempt 均 succeeded
func TestPG_AttemptRoundTrip(t *testing.T) {
	ctx, repo, cleanup := setupTaskPG(t)
	defer cleanup()

	tsk, _ := submitTask(ctx, repo)
	claimed, err := repo.Claim(ctx, "w-ok")
	if err != nil || claimed == nil {
		t.Fatalf("claim 失败: %v %v", claimed, err)
	}
	attempt, err := repo.CreateAttempt(ctx, claimed, "w-ok")
	if err != nil {
		t.Fatal(err)
	}

	ok, err := repo.MarkAttemptSucceeded(ctx, attempt, []byte(`{"done":true}`))
	if err != nil || !ok {
		t.Fatalf("成功回写应通过: %v %v", ok, err)
	}
	final, _ := repo.Find(ctx, tsk.ID)
	if final.Status != task.StatusSucceeded {
		t.Fatalf("task 应 succeeded: %s", final.Status)
	}
	active, _ := repo.FindActiveAttempt(ctx, tsk.ID)
	if active != nil {
		t.Fatal("不应再有 running attempt")
	}
}

// TestPG_FailAndBackoff 失败回写 + 三档退避（available_at 推进 5/10/20s）
func TestPG_FailAndBackoff(t *testing.T) {
	ctx, repo, cleanup := setupTaskPG(t)
	defer cleanup()

	p := task.NewDefaultRetryPolicy()
	for attempts := 1; attempts <= 3; attempts++ {
		tsk := &task.Task{TaskNo: fmt.Sprintf("b%d", attempts), Type: "t", Status: task.StatusFailed,
			Attempts: attempts, MaxAttempts: 9, AvailableAt: time.Now().Add(-time.Minute)}
		if err := repo.Save(ctx, tsk); err != nil {
			t.Fatal(err)
		}
		// RetryScan 语义：failed 到期 → 退避转 pending
		failed, _ := repo.FindFailedTasks(ctx, 10)
		var found *task.Task
		for _, f := range failed {
			if f.ID == tsk.ID {
				found = f
			}
		}
		if found == nil {
			t.Fatalf("failed 任务应可被扫描（attempts=%d）", attempts)
		}
		next := p.NextAvailableAt(found, time.Now())
		if err := repo.RescheduleToPending(ctx, found, next); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.Find(ctx, tsk.ID)
		want := time.Duration(5<<uint(attempts-1)) * time.Second
		delay := got.AvailableAt.Sub(time.Now())
		if delay < want-time.Second || delay > want+time.Second {
			t.Fatalf("attempts=%d 退避应约 %v，实际 %v", attempts, want, delay)
		}
		if got.Status != task.StatusPending {
			t.Fatalf("RetryScan 后应 pending: %s", got.Status)
		}
	}
}

// TestPG_TimeoutReclaimAndSlowWriterRejection 超时回收 + 慢执行者回写被拒
func TestPG_TimeoutReclaimAndSlowWriterRejection(t *testing.T) {
	ctx, repo, cleanup := setupTaskPG(t)
	defer cleanup()

	// 建 running 任务：started_at 挪到 10s 前，timeout=5s → 已超时
	tsk, _ := submitTask(ctx, repo)
	claimed, _ := repo.Claim(ctx, "w-slow")
	attempt, _ := repo.CreateAttempt(ctx, claimed, "w-slow")
	if _, err := g.DB().Exec(ctx,
		`UPDATE task SET started_at = NOW() - INTERVAL '10 seconds', timeout = 5 WHERE id = ?`, tsk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.DB().Exec(ctx,
		`UPDATE task_attempt SET started_at = NOW() - INTERVAL '10 seconds' WHERE id = ?`, attempt.ID); err != nil {
		t.Fatal(err)
	}

	// 超时任务可被扫到
	timedOut, err := repo.FindTimedOutTasks(ctx, 10)
	if err != nil || len(timedOut) == 0 {
		t.Fatalf("超时任务应可扫描: %v %v", timedOut, err)
	}

	// 回收：markTimeout 后慢执行者的成功回写被拒
	if err := repo.MarkTimeout(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	ok, err := repo.MarkAttemptSucceeded(ctx, attempt, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("超时回收后慢执行者回写应被拒（false）")
	}
	final, _ := repo.Find(ctx, tsk.ID)
	if final.Status == task.StatusSucceeded {
		t.Fatal("task 状态不应被慢执行者覆盖为 succeeded")
	}
}

// TestPG_RetryScanDead 达 max_attempts 转 dead
func TestPG_RetryScanDead(t *testing.T) {
	ctx, repo, cleanup := setupTaskPG(t)
	defer cleanup()

	tsk := &task.Task{TaskNo: "d1", Type: "t", Status: task.StatusFailed,
		Attempts: 3, MaxAttempts: 3, AvailableAt: time.Now().Add(-time.Minute)}
	if err := repo.Save(ctx, tsk); err != nil {
		t.Fatal(err)
	}

	scanner := task.NewRetryScanner(repo)
	_, _, dead, err := scanner.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dead < 1 {
		t.Fatalf("应至少 1 个转 dead: %d", dead)
	}
	final, _ := repo.Find(ctx, tsk.ID)
	if final.Status != task.StatusDead {
		t.Fatalf("应 dead: %s", final.Status)
	}
}

// TestPG_ParentChildAggregate 父子继承 + 聚合派生（2 成功 1 失败 → completed_with_errors 进度 2/3）
func TestPG_ParentChildAggregate(t *testing.T) {
	ctx, repo, cleanup := setupTaskPG(t)
	defer cleanup()

	// 父任务 available_at 推远 1 小时：防止子任务 claim 循环误抓父（父仅作拆分壳）
	far := time.Now().Add(time.Hour)
	parent := &task.Task{TaskNo: "p1", Type: "import", Status: task.StatusPending,
		Priority: 4, MaxAttempts: 2, Timeout: 30, BizExt: []byte(`{"src":"x"}`), AvailableAt: far}
	if err := repo.Save(ctx, parent); err != nil {
		t.Fatal(err)
	}
	children, err := repo.BatchInsertChildren(ctx, parent,
		[][]byte{[]byte(`{"i":1}`), []byte(`{"i":2}`), []byte(`{"i":3}`)})
	if err != nil {
		t.Fatal(err)
	}

	// 继承断言
	for _, c := range children {
		if c.Type != parent.Type || c.Priority != parent.Priority ||
			c.MaxAttempts != parent.MaxAttempts || c.Timeout != parent.Timeout || string(c.BizExt) != string(parent.BizExt) {
			t.Fatalf("继承不符: %+v", c)
		}
	}

	// 2 成功 1 失败
	for i, c := range children {
		ct, _ := repo.Find(ctx, c.ID)
		claimed, _ := repo.Claim(ctx, "w")
		if claimed == nil || claimed.ID != ct.ID {
			// 直接置 running 再建租约（避免 claim 顺序耦合）
			if _, err := g.DB().Exec(ctx,
				`UPDATE task SET status = 'running', attempts = attempts + 1 WHERE id = ?`, ct.ID); err != nil {
				t.Fatal(err)
			}
			ct.Status = task.StatusRunning
			ct.Attempts++
		}
		ct, _ = repo.Find(ctx, c.ID)
		at, _ := repo.CreateAttempt(ctx, ct, "w")
		if i < 2 {
			if ok, err := repo.MarkAttemptSucceeded(ctx, at, nil); !ok || err != nil {
				t.Fatalf("成功回写失败: %v %v", ok, err)
			}
		} else {
			if ok, _ := repo.MarkAttemptFailed(ctx, at, "boom"); !ok {
				t.Fatal("失败回写应通过")
			}
		}
	}

	// 父行状态不被改写；聚合派生
	pRow, _ := repo.Find(ctx, parent.ID)
	if pRow.Status != task.StatusPending {
		t.Fatalf("父行状态应保持 pending: %s", pRow.Status)
	}
	agg, err := repo.AggregateChildStatus(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agg.DerivedStatus != task.StatusCompletedWithErrors {
		t.Fatalf("聚合应为 completed_with_errors: %s", agg.DerivedStatus)
	}
	if agg.Total != 3 || agg.Succeeded != 2 || agg.Failed != 1 {
		t.Fatalf("聚合计数不符: %+v", agg)
	}
	if agg.Progress != 66 {
		t.Fatalf("进度应 66（2/3）: %d", agg.Progress)
	}
}

func submitTask(ctx context.Context, repo *task.PgRepository) (*task.Task, error) {
	tsk := &task.Task{TaskNo: fmt.Sprintf("t%d", time.Now().UnixNano()), Type: "t",
		Status: task.StatusPending, MaxAttempts: 3}
	err := repo.Save(ctx, tsk)
	return tsk, err
}

// jsonEqual JSON 语义比较：PG jsonb 列存储会规范化文本（键值分隔后加空格），
// 回读字节与原始字节不稳定，须 Unmarshal 后比较。
func jsonEqual(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("回读 payload 非法 JSON: %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

// TestPG_SaveInsertUpdateRoundTrip 插入/更新全字段往返：防 Data() 整体覆盖语义
// 回归（v0.6.0 曾因二次 Data 链式追加丢掉 16 字段集，插入仅剩 id 撞 NOT NULL）。
func TestPG_SaveInsertUpdateRoundTrip(t *testing.T) {
	ctx, repo, cleanup := setupTaskPG(t)
	defer cleanup()

	// 插入：全字段落库且 id 由组件生成 uuidv7
	avail := time.Now().Add(time.Minute)
	tsk := &task.Task{
		TaskNo: fmt.Sprintf("rt-%d", time.Now().UnixNano()), Type: "roundtrip",
		Payload: []byte(`{"k":"v"}`), Status: task.StatusPending, Priority: 7,
		MaxAttempts: 5, AvailableAt: avail, Timeout: 30, Progress: 10,
		Result: []byte(`{"done":true}`), LastError: "boom",
		BizExt: []byte(`{"b":1}`),
	}
	if err := repo.Save(ctx, tsk); err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	if tsk.ID == "" {
		t.Fatal("插入后 ID 应由组件生成")
	}
	got, err := repo.Find(ctx, tsk.ID)
	if err != nil || got == nil {
		t.Fatalf("回读失败: %v / %v", got, err)
	}
	if got.TaskNo != tsk.TaskNo || got.Type != tsk.Type || got.Priority != 7 ||
		got.MaxAttempts != 5 || got.Timeout != 30 || got.Progress != 10 ||
		got.LastError != "boom" || !jsonEqual(t, got.Payload, `{"k":"v"}`) ||
		!jsonEqual(t, got.Result, `{"done":true}`) || !jsonEqual(t, got.BizExt, `{"b":1}`) {
		t.Fatalf("插入往返字段失真: got=%+v", got)
	}

	// 更新：改字段后 Save 走更新分支，同样不丢字段
	tsk.Status = task.StatusFailed
	tsk.Progress = 99
	tsk.Attempts = 2
	if err := repo.Save(ctx, tsk); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got, err = repo.Find(ctx, tsk.ID)
	if err != nil || got == nil {
		t.Fatalf("更新后回读失败: %v / %v", got, err)
	}
	if got.Status != task.StatusFailed || got.Progress != 99 || got.Attempts != 2 ||
		got.Type != "roundtrip" || got.Priority != 7 || got.MaxAttempts != 5 {
		t.Fatalf("更新往返字段失真: got=%+v", got)
	}
}

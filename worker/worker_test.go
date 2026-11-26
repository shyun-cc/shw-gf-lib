package worker

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// 测试通用参数：interval 取 50ms 让多个 tick 在秒级内完成；
// 所有等待均用带超时上限的 select 同步，不用裸 sleep，避免 flaky（设计 D7）。
const (
	testInterval = 50 * time.Millisecond
	testTimeout  = 3 * time.Second
	// 锁 TTL 秒级（WithRedisLock 内部按秒取整），取 2s 避免执行间隙锁过期
	testLockTTL = 2 * time.Second
)

// waitEvent 从事件 channel 等待一次信号，超时则 t.Fatal
func waitEvent(t *testing.T, events <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-events:
	case <-time.After(testTimeout):
		t.Fatalf("等待 %s 超时（上限 %s）", what, testTimeout)
	}
}

// waitDone 等待 Run 所在 goroutine 退出（测试结束 cancel 后回收，防 goroutine 泄漏）
func waitDone(t *testing.T, done <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatalf("等待 %s 的 Run 退出超时", name)
	}
}

// TestPeriodicWorker_Run_MultipleTicks 场景 1：短 interval 下 handle 被多次执行
func TestPeriodicWorker_Run_MultipleTicks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan struct{}, 16) // 缓冲足够大，handle 发送不阻塞
	w := NewPeriodicWorker("counter", testInterval, func(ctx context.Context) error {
		events <- struct{}{}
		return nil
	})
	if w.Name() != "counter" {
		t.Fatalf("Name() = %q, 期望 %q", w.Name(), "counter")
	}

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()

	// 顺序等待 3 次执行信号，任意一次超时即失败
	const want = 3
	for i := 1; i <= want; i++ {
		waitEvent(t, events, "第 "+strconv.Itoa(i)+" 次执行")
	}

	// cancel 后 Run 应及时返回
	cancel()
	waitDone(t, done, "counter")
}

// TestPeriodicWorker_Run_HandlePanicNotFatal 场景 2：handle panic 不中断后续 tick
func TestPeriodicWorker_Run_HandlePanicNotFatal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan struct{}, 16)
	calls := 0 // Run 在单 goroutine 内串行调用 handle，普通计数即可
	w := NewPeriodicWorker("panicky", testInterval, func(ctx context.Context) error {
		calls++
		if calls == 1 {
			// 首次执行：向 channel 发送前 panic，验证 runOnce 的 recover 兜底
			panic("boom: 首次执行故意 panic")
		}
		events <- struct{}{}
		return nil
	})

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()

	// 若 panic 中断了循环，这里永远收不到信号，将超时失败
	const want = 2
	for i := 1; i <= want; i++ {
		waitEvent(t, events, "panic 之后的正常执行")
	}
	if calls < want+1 {
		t.Fatalf("handle 共调用 %d 次，期望至少 %d 次（含 1 次 panic）", calls, want+1)
	}

	cancel()
	waitDone(t, done, "panicky")
}

// TestPeriodicWorker_WithRedisLock_SingleInstance 场景 3：单实例持锁正常周期执行
func TestPeriodicWorker_WithRedisLock_SingleInstance(t *testing.T) {
	testredis.Start(t) // 进程内 miniredis，g.Redis() 零感知

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan struct{}, 16)
	w := NewPeriodicWorker("locked-single", testInterval, func(ctx context.Context) error {
		events <- struct{}{}
		return nil
	}, WithRedisLock("test-lock-single", testLockTTL))

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()

	// 每轮 tick 走完整链路：Get 锁 -> handle -> Release，连续 2 次均应正常执行
	const want = 2
	for i := 1; i <= want; i++ {
		waitEvent(t, events, "持锁执行")
	}

	cancel()
	waitDone(t, done, "locked-single")
}

// TestPeriodicWorker_WithRedisLock_TwoInstancesMutex 场景 4：同 key 双实例互斥。
//
// 时序设计：实例 A 首次执行后阻塞在 handle 内持续持锁（TTL 内），
// 此时启动实例 B，给 B 留出多个 tick 周期——B 的每次 Get 都应失败、handle 不被调用；
// 随后放行 A，其积压的 tick 立即触发第 2 次执行（证明 A 周期执行未受影响）。
// 注意：必须在 A 仍在持锁（handle 未返回）时断言 B 计数为 0，
// 因为锁是单次执行持有的，A 释放后 B 按设计就能抢到锁。
func TestPeriodicWorker_WithRedisLock_TwoInstancesMutex(t *testing.T) {
	testredis.Start(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gate := make(chan struct{}) // 控制 A 的 handle 持锁时长
	chanA := make(chan struct{}, 16)
	chanB := make(chan struct{}, 16)

	workerA := NewPeriodicWorker("instance-a", testInterval, func(ctx context.Context) error {
		chanA <- struct{}{} // 发出"执行了一次"信号
		<-gate              // 阻塞在此期间一直持有锁
		return nil
	}, WithRedisLock("test-lock-mutex", testLockTTL))

	workerB := NewPeriodicWorker("instance-b", testInterval, func(ctx context.Context) error {
		chanB <- struct{}{}
		return nil
	}, WithRedisLock("test-lock-mutex", testLockTTL))

	doneA := make(chan struct{})
	go func() {
		_ = workerA.Run(ctx)
		close(doneA)
	}()

	// 先确认 A 已执行并持锁，再启动 B，消除启动竞态
	waitEvent(t, chanA, "实例 A 首次执行")

	doneB := make(chan struct{})
	go func() {
		_ = workerB.Run(ctx)
		close(doneB)
	}()

	// 给 B 留出 5 个 tick 周期（锁始终被 A 占据，B 应全部跳过）
	select {
	case <-time.After(5 * testInterval):
	case <-ctx.Done():
		t.Fatal("ctx 意外取消")
	}

	// 断言 B 的 handle 从未被调用
	if got := len(chanB); got != 0 {
		t.Fatalf("实例 B 在 A 持锁期间执行了 %d 次，期望 0 次（互斥失效）", got)
	}

	// 放行 A：handle 返回并释放锁，积压 tick 立即触发第 2 次执行
	close(gate)
	waitEvent(t, chanA, "实例 A 第二次执行")

	cancel()
	waitDone(t, doneA, "instance-a")
	waitDone(t, doneB, "instance-b")
}

// ---- Manager 生命周期事件用例（worker.Started / worker.Stopped / worker.Panicked）----
// 辅助与假 worker 均带 mgr 前缀，避免与本包并行批次的测试文件撞名。

// mgrStubWorker 轻量假 worker：直接实现 Worker 接口，Run 行为由闭包注入
type mgrStubWorker struct {
	name string
	run  func(ctx context.Context) error
}

func (w *mgrStubWorker) Name() string                  { return w.name }
func (w *mgrStubWorker) Run(ctx context.Context) error { return w.run(ctx) }

// mgrWaitEvent 等待一条事件，超时 Fatal（风格对齐文件顶部 waitEvent）
func mgrWaitEvent(t *testing.T, ch <-chan events.Event, what string) events.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(testTimeout):
		t.Fatalf("等待 %s 超时（上限 %s）", what, testTimeout)
		return events.Event{} // 不可达，满足编译
	}
}

// mgrAssertNoMore 静默窗口内确认无更多事件到达（「恰好一次」断言的收尾）
func mgrAssertNoMore(t *testing.T, ch <-chan events.Event, what string) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("收到多余的 %s 事件：%+v", what, e)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestManager_StartedEvent Started 事件：全部 worker goroutine 拉起后恰好派发一次，
// Names 按注册顺序携带全部 worker 名；cancel 原始 ctx 后 Start 正常返回
func TestManager_StartedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := events.New()
	started := make(chan events.Event, 16)
	d.Subscribe(EventWorkerStarted, 0, func(ctx context.Context, e events.Event) {
		started <- e
	})

	m := NewWorkerManager()
	m.SetDispatcher(d)
	m.Register(NewPeriodicWorker("started-alpha", testInterval, func(ctx context.Context) error { return nil }))
	m.Register(NewPeriodicWorker("started-beta", testInterval, func(ctx context.Context) error { return nil }))

	done := make(chan struct{})
	go func() {
		_ = m.Start(ctx)
		close(done)
	}()

	e := mgrWaitEvent(t, started, "Worker Started")
	p, ok := e.Payload.(WorkerStartedEvent)
	if !ok {
		t.Fatalf("Started 载荷类型不符：want WorkerStartedEvent got %T", e.Payload)
	}
	if want := []string{"started-alpha", "started-beta"}; len(p.Names) != 2 || p.Names[0] != want[0] || p.Names[1] != want[1] {
		t.Fatalf("Started 载荷 Names 不符：want %v got %v", want, p.Names)
	}
	mgrAssertNoMore(t, started, "Started")

	// cancel 原始 ctx（NotifyContext 包装 ctx 的父级）触发退出，Start 正常返回
	cancel()
	waitDone(t, done, "Start")
}

// TestManager_StoppedEvent Stopped 事件：每个 worker 的 Run 返回后各恰好派发一次，
// Err 为 Run 返回值透传（nil 正常退出 / 非 nil 异常退出）
func TestManager_StoppedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := events.New()
	stopped := make(chan events.Event, 16)
	d.Subscribe(EventWorkerStopped, 0, func(ctx context.Context, e events.Event) {
		stopped <- e
	})

	selfDestruct := errors.New("self-destruct")
	m := NewWorkerManager()
	m.SetDispatcher(d)
	m.Register(&mgrStubWorker{name: "stop-ok", run: func(ctx context.Context) error { return nil }})
	m.Register(&mgrStubWorker{name: "stop-bad", run: func(ctx context.Context) error { return selfDestruct }})

	done := make(chan struct{})
	go func() {
		_ = m.Start(ctx)
		close(done)
	}()

	stoppedErr := make(map[string]error) // 到达顺序不定，按 worker 名分桶
	for i := 0; i < 2; i++ {
		e := mgrWaitEvent(t, stopped, "Worker Stopped")
		p, ok := e.Payload.(WorkerStoppedEvent)
		if !ok {
			t.Fatalf("Stopped 载荷类型不符：want WorkerStoppedEvent got %T", e.Payload)
		}
		stoppedErr[p.Name] = p.Err
	}
	if err, ok := stoppedErr["stop-ok"]; !ok || err != nil {
		t.Fatalf("stop-ok 的 Stopped 不符（期望 Err=nil）：Err=%v ok=%t", err, ok)
	}
	if err, ok := stoppedErr["stop-bad"]; !ok || !errors.Is(err, selfDestruct) {
		t.Fatalf("stop-bad 的 Stopped 未透传自毁错误：Err=%v ok=%t", err, ok)
	}
	mgrAssertNoMore(t, stopped, "Stopped")

	// 两个 Run 均立即返回，Start 应自行正常返回（无需信号）
	waitDone(t, done, "Start")
}

// TestManager_PanickedEvent Panicked 事件：Run 内 panic 被 manager recover 后
// 恰好派发一次（携带 worker 名与 recover 值），同伴照常执行，
// 且 panic worker 与正常 worker 的 Stopped 均不缺席
func TestManager_PanickedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := events.New()
	panicked := make(chan events.Event, 16)
	stopped := make(chan events.Event, 16)
	d.Subscribe(EventWorkerPanicked, 0, func(ctx context.Context, e events.Event) { panicked <- e })
	d.Subscribe(EventWorkerStopped, 0, func(ctx context.Context, e events.Event) { stopped <- e })

	calmTicks := make(chan struct{}, 16)
	m := NewWorkerManager()
	m.SetDispatcher(d)
	m.Register(&mgrStubWorker{name: "panic-boom", run: func(ctx context.Context) error { panic("boom") }})
	m.Register(NewPeriodicWorker("panic-calm", testInterval, func(ctx context.Context) error {
		calmTicks <- struct{}{}
		return nil
	}))

	done := make(chan struct{})
	go func() {
		_ = m.Start(ctx)
		close(done)
	}()

	// Panicked 恰好一次，携带 worker 名与 recover 值
	e := mgrWaitEvent(t, panicked, "Worker Panicked")
	p, ok := e.Payload.(WorkerPanickedEvent)
	if !ok {
		t.Fatalf("Panicked 载荷类型不符：want WorkerPanickedEvent got %T", e.Payload)
	}
	if p.Name != "panic-boom" || p.Recovered != "boom" {
		t.Fatalf("Panicked 载荷不符：got %+v", p)
	}
	mgrAssertNoMore(t, panicked, "Panicked")

	// 同伴不受 panic 影响：收到其执行信号
	waitEvent(t, calmTicks, "panic 之后正常 worker 的执行")

	// cancel 触发 calm 退出；两个 worker 的 Stopped 均恰好各一次
	//（panic-boom 的 Stopped 在 recover 后由同一 defer 链派发，Err 为 nil）
	cancel()
	stoppedNames := make(map[string]bool)
	for i := 0; i < 2; i++ {
		se := mgrWaitEvent(t, stopped, "Worker Stopped")
		sp, ok := se.Payload.(WorkerStoppedEvent)
		if !ok {
			t.Fatalf("Stopped 载荷类型不符：want WorkerStoppedEvent got %T", se.Payload)
		}
		stoppedNames[sp.Name] = true
	}
	if !stoppedNames["panic-boom"] || !stoppedNames["panic-calm"] {
		t.Fatalf("Stopped 应覆盖 panic 与正常两个 worker，got %v", stoppedNames)
	}
	mgrAssertNoMore(t, stopped, "Stopped")
	waitDone(t, done, "Start")
}

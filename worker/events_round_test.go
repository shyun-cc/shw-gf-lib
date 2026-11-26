package worker

// 轮次事件（EventRoundStarted/EventRoundCompleted）用例（task 2.3）：
// 全部经注入 events.New() 实例断言，不依赖也不污染 events.Default() 全局状态。
// 事件收集依赖 events 同步派发——channel 内顺序即派发顺序（同轮 Started 严格先于 Completed），
// 所有等待均带超时上限，不用裸 sleep。

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// roundEvt 收集到的一条轮次事件：事件名 + RoundEvent 载荷
type roundEvt struct {
	name    string
	payload RoundEvent
}

// subscribeRound 将 Started/Completed 两个事件名的载荷统一收进 ch（缓冲须足够大，handler 内不阻塞）
func subscribeRound(d *events.Dispatcher, ch chan<- roundEvt) {
	collect := func(ctx context.Context, e events.Event) {
		p, _ := e.Payload.(RoundEvent)
		ch <- roundEvt{name: e.Name, payload: p}
	}
	d.Subscribe(EventRoundStarted, 0, collect)
	d.Subscribe(EventRoundCompleted, 0, collect)
}

// waitRound 带超时等待一条事件并断言事件名
func waitRound(t *testing.T, ch <-chan roundEvt, want string) roundEvt {
	t.Helper()
	select {
	case e := <-ch:
		if e.name != want {
			t.Fatalf("事件顺序不符：want %s got %s", want, e.name)
		}
		return e
	case <-time.After(testTimeout):
		t.Fatalf("等待事件 %s 超时（上限 %s）", want, testTimeout)
		return roundEvt{} // t.Fatalf 会 Goexit，此处仅为满足返回值
	}
}

// drainRounds 非阻塞收完 channel 内剩余事件
func drainRounds(ch <-chan roundEvt) []roundEvt {
	var out []roundEvt
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

// assertPaired 断言事件序列为 Started/Completed 严格交替成对（无悬挂 Started）
func assertPaired(t *testing.T, seq []roundEvt) {
	t.Helper()
	if len(seq)%2 != 0 {
		t.Fatalf("事件不成对（存在悬挂 Started）：%v", seq)
	}
	for i := 0; i < len(seq); i += 2 {
		if seq[i].name != EventRoundStarted || seq[i+1].name != EventRoundCompleted {
			t.Fatalf("位置 %d/%d 期望 Started/Completed 交替，实际 %s/%s",
				i, i+1, seq[i].name, seq[i+1].name)
		}
	}
}

// TestPeriodicWorker_RoundEvents_NormalPair 正常轮恰好一对 Started/Completed：
// Completed 携带 Duration > 0 与 Err == nil，Started 载荷为零值 Duration/Err。
// 收到首对即 cancel；cancel 与下一 tick 的 select 竞态至多多跑完整一轮，
// 故收尾断言剩余事件仍严格配对（无悬挂 Started）。
func TestPeriodicWorker_RoundEvents_NormalPair(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 64)
	subscribeRound(d, ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := NewPeriodicWorker("round-ok", testInterval, func(ctx context.Context) error {
		time.Sleep(time.Millisecond) // 确保 Duration 有明确余量
		return nil
	})
	w.Dispatcher = d

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()

	s := waitRound(t, ch, EventRoundStarted)
	if s.payload.Name != "round-ok" || s.payload.Duration != 0 || s.payload.Err != nil {
		t.Fatalf("RoundStarted 载荷不符（Duration/Err 应为零值）：%+v", s.payload)
	}
	c := waitRound(t, ch, EventRoundCompleted)
	if c.payload.Name != "round-ok" || c.payload.Err != nil {
		t.Fatalf("RoundCompleted 载荷不符：%+v", c.payload)
	}
	if c.payload.Duration <= 0 {
		t.Fatalf("RoundCompleted.Duration 应 > 0，实际 %v", c.payload.Duration)
	}

	cancel()
	waitDone(t, done, "round-ok")
	assertPaired(t, drainRounds(ch))
}

// TestPeriodicWorker_RoundEvents_ErrPassthrough handle 返回 error 时 Completed.Err 原样透传
func TestPeriodicWorker_RoundEvents_ErrPassthrough(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 64)
	subscribeRound(d, ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errWant := errors.New("round-fail-42")
	w := NewPeriodicWorker("round-err", testInterval, func(ctx context.Context) error {
		return errWant
	})
	w.Dispatcher = d

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()

	waitRound(t, ch, EventRoundStarted)
	c := waitRound(t, ch, EventRoundCompleted)
	if c.payload.Err != errWant {
		t.Fatalf("Completed.Err 应透传 handle 返回值：want %v got %v", errWant, c.payload.Err)
	}

	cancel()
	waitDone(t, done, "round-err")
	assertPaired(t, drainRounds(ch))
}

// TestPeriodicWorker_RoundEvents_DurationExcludesInterval Duration 与 interval 无关：
// 真实短 interval（50ms）+ handle 内可控耗时 5ms，断言 Duration >= 5ms 且 < interval
// （若误含轮间等待将达 interval 量级）。
func TestPeriodicWorker_RoundEvents_DurationExcludesInterval(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 64)
	subscribeRound(d, ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := NewPeriodicWorker("round-dur", testInterval, func(ctx context.Context) error {
		time.Sleep(5 * time.Millisecond) // 可控耗时：time.Sleep 保证至少 5ms
		return nil
	})
	w.Dispatcher = d

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()

	waitRound(t, ch, EventRoundStarted)
	c := waitRound(t, ch, EventRoundCompleted)
	if c.payload.Duration < 5*time.Millisecond {
		t.Fatalf("Duration 应 >= handle 耗时 5ms，实际 %v", c.payload.Duration)
	}
	if c.payload.Duration >= testInterval {
		t.Fatalf("Duration 应 < interval %s（不含轮间等待），实际 %v", testInterval, c.payload.Duration)
	}

	cancel()
	waitDone(t, done, "round-dur")
}

// TestProcWorker_RoundEvents_NormalPair ProcWorker 正常轮恰好一对：
// 注入版（Sleep 置空、Rand 固定）+ MaxLoops=1 恰一轮后 Run 自毁退出——
// 无 cancel 竞态，可严格断言事件总数恰为 2 且 Started 在前。
func TestProcWorker_RoundEvents_NormalPair(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 64)
	subscribeRound(d, ch)

	w := NewProcWorker("proc-round-ok", 10*time.Millisecond, func(ctx context.Context) error {
		time.Sleep(time.Millisecond) // 确保 Duration 有明确余量
		return nil
	})
	w.Sleep = func(time.Duration) {}      // 不真睡
	w.Rand = func(n int) int { return 0 } // 固定随机
	w.MaxLoops = 1                        // 恰一轮后以 CodeProcessIterationMax 自毁退出
	w.Dispatcher = d

	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background()) }()
	select {
	case <-done: // 自毁退出即全部派发（同步）已完成
	case <-time.After(testTimeout):
		t.Fatal("等待 ProcWorker Run 自毁退出超时")
	}

	all := drainRounds(ch)
	if len(all) != 2 {
		t.Fatalf("恰一轮应恰好一对事件，实际 %d 条：%v", len(all), all)
	}
	if all[0].name != EventRoundStarted {
		t.Fatalf("首个事件应为 Started，实际 %s", all[0].name)
	}
	if all[1].name != EventRoundCompleted {
		t.Fatalf("第二事件应为 Completed，实际 %s", all[1].name)
	}
	if all[0].payload.Name != "proc-round-ok" || all[1].payload.Name != "proc-round-ok" {
		t.Fatalf("载荷 Name 不符：%+v / %+v", all[0].payload, all[1].payload)
	}
	if all[1].payload.Duration <= 0 {
		t.Fatalf("Duration 应 > 0，实际 %v", all[1].payload.Duration)
	}
	if all[1].payload.Err != nil {
		t.Fatalf("正常轮 Completed.Err 应为 nil，实际 %v", all[1].payload.Err)
	}
}

// TestProcWorker_RoundEvents_DurationExcludesInterval Duration 与 interval/抖动无关（注入时间源）：
// interval 10ms、Rand 取最大抖动、Sleep 快进注入时钟（轮间等待真实耗时为 0），
// handle 真实耗时 3ms——断言每轮 Duration 为 handle 纯耗时（>=3ms 且 <10ms），
// 若误含周期补齐/抖动等待将 >= interval。
func TestProcWorker_RoundEvents_DurationExcludesInterval(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 64)
	subscribeRound(d, ch)

	var fakeNow int64 // 注入时钟（纳秒），Sleep 快进驱动
	w := NewProcWorker("proc-round-dur", 10*time.Millisecond, func(ctx context.Context) error {
		time.Sleep(3 * time.Millisecond) // Duration 由库内真实时钟测量，handle 须真实耗时
		return nil
	})
	w.Clock = func() time.Time { return time.Unix(0, atomic.LoadInt64(&fakeNow)) }
	w.Sleep = func(dd time.Duration) { atomic.AddInt64(&fakeNow, int64(dd)) } // 快进不真睡
	w.Rand = func(n int) int { return n }                                     // 最大抖动：误入 Duration 即超标
	w.MaxLoops = 3
	w.Dispatcher = d

	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background()) }()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("等待 ProcWorker Run 自毁退出超时")
	}

	all := drainRounds(ch)
	if len(all) != 6 {
		t.Fatalf("3 轮应恰好 3 对事件，实际 %d 条：%v", len(all), all)
	}
	assertPaired(t, all)
	for i := 0; i < len(all); i += 2 {
		p := all[i+1].payload
		if p.Err != nil {
			t.Fatalf("第 %d 轮 Completed.Err 应为 nil，实际 %v", i/2+1, p.Err)
		}
		if p.Duration < 3*time.Millisecond {
			t.Fatalf("第 %d 轮 Duration 应 >= handle 耗时 3ms，实际 %v", i/2+1, p.Duration)
		}
		if p.Duration >= 10*time.Millisecond {
			t.Fatalf("第 %d 轮 Duration 应 < interval 10ms（不含补齐/抖动等待），实际 %v", i/2+1, p.Duration)
		}
	}
}

// TestRoundEvents_LockSkipZeroEvents 锁被占实例零事件（仿 worker_test.go 场景 4 形态）：
// A 持锁阻塞期间，B 的多个 tick 全部锁获取失败——B 的注入 dispatcher 零派发。
// A 注入空 dispatcher（无订阅者）避免其轮次事件落入全局 Default()。
func TestRoundEvents_LockSkipZeroEvents(t *testing.T) {
	testredis.Start(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gate := make(chan struct{}) // 控制 A 的 handle 持锁时长
	chanA := make(chan struct{}, 16)
	chanB := make(chan struct{}, 16) // B 的 handle 被调信号（互斥失效时才有值）

	evtB := make(chan roundEvt, 64) // B 的轮次事件：锁跳过轮应为空
	dispB := events.New()
	subscribeRound(dispB, evtB)

	workerA := NewPeriodicWorker("lock-round-a", testInterval, func(ctx context.Context) error {
		chanA <- struct{}{}
		<-gate // 阻塞在此期间一直持有锁
		return nil
	}, WithRedisLock("round-lock-mutex", testLockTTL))
	workerA.Dispatcher = events.New() // 空实例：不订阅、不落全局

	workerB := NewPeriodicWorker("lock-round-b", testInterval, func(ctx context.Context) error {
		chanB <- struct{}{}
		return nil
	}, WithRedisLock("round-lock-mutex", testLockTTL))
	workerB.Dispatcher = dispB

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

	if got := len(evtB); got != 0 {
		t.Fatalf("锁跳过轮派发了 %d 条事件，期望 0（B 的 tick 应零事件）", got)
	}
	if got := len(chanB); got != 0 {
		t.Fatalf("实例 B 在 A 持锁期间执行了 %d 次，期望 0 次（互斥失效）", got)
	}

	// 放行 A 并回收，A 第二次执行属于其自身 dispatcher，与 B 无关
	close(gate)
	waitEvent(t, chanA, "实例 A 第二次执行")

	cancel()
	waitDone(t, doneA, "lock-round-a")
	waitDone(t, doneB, "lock-round-b")

	// 放行后若 B 抢到锁属正常（A 已释放），但 A 持锁期间窗口内必须零事件——上面已断言
}

// TestRoundEvents_SubscriberPanicIsolation 订阅方 handler panic 不伤 worker：
// Started/Completed 各挂一个高优先级 panic handler（先于收集 handler 执行），
// events 侧 handler 级 recover 兜底后，当轮与后续轮次照常执行（两对完整事件收到）。
func TestRoundEvents_SubscriberPanicIsolation(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 64)
	subscribeRound(d, ch)

	boom := func(ctx context.Context, e events.Event) { panic("subscriber boom: " + e.Name) }
	d.Subscribe(EventRoundStarted, 10, boom) // 大值先执行：panic handler 先跑
	d.Subscribe(EventRoundCompleted, 10, boom)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := NewPeriodicWorker("round-sub", testInterval, func(ctx context.Context) error {
		return nil
	})
	w.Dispatcher = d

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()

	// 两轮完整 Started/Completed 配对：panic handler 未中断派发，worker 未中断轮次
	for round := 1; round <= 2; round++ {
		waitRound(t, ch, EventRoundStarted)
		c := waitRound(t, ch, EventRoundCompleted)
		if c.payload.Err != nil {
			t.Fatalf("第 %d 轮 Completed.Err 应为 nil，实际 %v", round, c.payload.Err)
		}
	}

	cancel()
	waitDone(t, done, "round-sub")
	assertPaired(t, drainRounds(ch))
}

// TestPeriodicWorker_RoundEvents_PanicRoundNoCompleted handle panic 的轮不发 Completed：
// 首轮 handle panic（runOnce 既有 recover 兜住继续跑），事件序列应为
// S(轮1), S(轮2), C(轮2), S(轮3), C(轮3)——轮 1 有 Started 无 Completed，后续轮恢复配对。
func TestPeriodicWorker_RoundEvents_PanicRoundNoCompleted(t *testing.T) {
	d := events.New()
	ch := make(chan roundEvt, 64)
	subscribeRound(d, ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0 // Run 单 goroutine 串行调用 handle，普通计数即可
	w := NewPeriodicWorker("round-panic", testInterval, func(ctx context.Context) error {
		calls++
		if calls == 1 {
			panic("round panic: 首轮故意")
		}
		return nil
	})
	w.Dispatcher = d

	done := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(done)
	}()

	// 收到 2 个 Completed（轮 2、轮 3）即构成完整判定序列
	var seq []roundEvt
	for len(seq) < 5 {
		select {
		case e := <-ch:
			seq = append(seq, e)
		case <-time.After(testTimeout):
			t.Fatalf("事件序列收集不足（已收 %d 条）：%v", len(seq), seq)
		}
	}
	want := []string{
		EventRoundStarted,   // 轮 1 Started（panic 前已派发）
		EventRoundStarted,   // 轮 2 Started——轮 1 无 Completed 直接跟来，即 panic 轮未发 Completed
		EventRoundCompleted, // 轮 2 Completed
		EventRoundStarted,   // 轮 3 Started
		EventRoundCompleted, // 轮 3 Completed
	}
	for i, n := range want {
		if seq[i].name != n {
			t.Fatalf("事件序列位置 %d 得到 %s 期望 %s（panic 轮不应派发 Completed）：%v",
				i, seq[i].name, n, seq)
		}
	}
	for _, i := range []int{2, 4} {
		if seq[i].payload.Err != nil {
			t.Fatalf("恢复轮 Completed.Err 应为 nil，实际 %v", seq[i].payload.Err)
		}
		if seq[i].payload.Duration <= 0 {
			t.Fatalf("恢复轮 Duration 应 > 0，实际 %v", seq[i].payload.Duration)
		}
	}

	cancel()
	waitDone(t, done, "round-panic")
	assertPaired(t, drainRounds(ch))
}

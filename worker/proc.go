package worker

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/redislock"
)

// ProcWorker 三层进程保护的周期任务（对应 PHP AbstractLoopProcess/AbstractPeriodicProcess），
// 内嵌 BaseWorker 承担轮次保护壳（每轮独立 traceId、panic recover、RoundStarted/
// RoundCompleted 轮次事件派发），本层保留三层进程保护：handle panic 经底座 recover
// 落日志后由 panicRound 钩子强制释放锁并 re-panic 上抛（不轮转，该轮有 Started
// 无 Completed），由 WorkerManager 的生命周期事件接管。
//  1. 自毁上限：MaxLoops（>0 时）循环达限以 CodeProcessIterationMax 退出；
//     LimitMemoryMB（>0 时）MemFunc 读数达限以 CodeProcessMemoryLimit 退出
//  2. 锁异常分层释放：经底座钩子接入——beforeRound 获取锁（失败跳过该轮）、
//     afterRound 正常完成 owner 安全释放、panicRound panic 强制释放（不等 TTL）
//     后 re-panic 上抛（传播出 Run，归 worker.Panicked/worker.Stopped 生命周期事件管）
//  3. 周期抖动错峰：轮起点间隔 ≥ interval（抖动只延长）；StartupDelay 三态（-1 随机 1-5s、0 无、>0 定值）
//
// 注册即运行：无环境变量开关，注册到 WorkerManager 后随生命周期执行。
// 时间与随机全部可注入（Clock/Rand/Sleep），单测不真睡。
type ProcWorker struct {
	*BaseWorker
	Interval time.Duration // 周期（相邻两轮起点间隔下界）
	Handle   func(ctx context.Context) error

	MaxLoops      int        // >0 启用循环上限自毁
	LimitMemoryMB int        // >0 启用内存上限自毁
	MemFunc       func() int // 内存读数注入（默认 runtime.MemStats 摘要，测试注入假读数）
	StartupDelay  int        // 秒；-1 随机 [1,5]，0 不延迟，>0 定值
	LockKey       string     // 非空启用分布式锁（秒级 TTL 用 LockTTL）
	LockTTL       int

	Clock func() time.Time // 时间源注入
	Rand  func(n int) int  // 随机源注入（抖动与随机启动延迟）
	Sleep func(time.Duration)

	// Dispatcher 事件分发器注入：轮次事件（RoundStarted/RoundCompleted）经其派发，nil 降级 events.Default()
	Dispatcher *events.Dispatcher

	lock *redislock.Lock
}

// 编译期断言：ProcWorker 实现 Worker 接口（Name 经内嵌 BaseWorker 提升）
var _ Worker = (*ProcWorker)(nil)

// ProcOption 配置选项
type ProcOption func(*ProcWorker)

// WithProcLock 启用分布式锁
func WithProcLock(key string, ttlSeconds int) ProcOption {
	return func(w *ProcWorker) { w.LockKey = key; w.LockTTL = ttlSeconds }
}

// NewProcWorker 构造入口：注册到 WorkerManager 后随生命周期运行。
func NewProcWorker(name string, interval time.Duration, handle func(ctx context.Context) error, opts ...ProcOption) *ProcWorker {
	w := &ProcWorker{
		BaseWorker:   NewBaseWorker(name, handle),
		Interval:     interval,
		Handle:       handle,
		StartupDelay: 0,
		Clock:        time.Now,
		Rand:         rand.Intn,
		Sleep:        time.Sleep,
		MemFunc:      defaultMemMB,
	}
	// 锁策略接进底座钩子：方法值持有 w，执行时才读 w.lock，
	// 故锁对象在 Run 内按 LockKey 创建也不影响接线
	w.beforeRound = w.beforeLockRound
	w.afterRound = w.releaseLock
	w.panicRound = w.forceReleaseLock
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Run 主循环：注册即运行（无开关判定）。
// 自毁（循环/内存上限）返回携带段位码的错误；ctx 取消正常退出。
func (w *ProcWorker) Run(ctx context.Context) error {
	// Dispatcher/Handle 是本类型导出字段，构造后可被外部赋值；启动时同步进内嵌底座
	// 的对应字段，保证「构造后赋值再 Run」的既有用法语义不变
	w.dispatcher = w.Dispatcher
	w.handle = w.Handle

	// 启动延迟三态
	if delay := w.startupDelay(); delay > 0 {
		w.Sleep(delay)
	}

	if w.LockKey != "" {
		w.lock = redislock.Create(w.LockKey, w.LockTTL)
	}

	loops := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if w.MaxLoops > 0 && loops >= w.MaxLoops {
			return exception.New(exception.CodeProcessIterationMax,
				fmt.Sprintf("ProcWorker %s 达循环上限 %d，自毁退出", w.Name(), w.MaxLoops))
		}
		if w.LimitMemoryMB > 0 && w.MemFunc() >= w.LimitMemoryMB {
			return exception.New(exception.CodeProcessMemoryLimit,
				fmt.Sprintf("ProcWorker %s 内存 %dMB 达上限 %dMB，自毁退出", w.Name(), w.MemFunc(), w.LimitMemoryMB))
		}

		start := w.Clock()
		w.RunOnce(ctx)
		loops++

		if w.LimitMemoryMB > 0 && w.MemFunc() >= w.LimitMemoryMB {
			return exception.New(exception.CodeProcessMemoryLimit,
				fmt.Sprintf("ProcWorker %s 内存 %dMB 达上限 %dMB，自毁退出", w.Name(), w.MemFunc(), w.LimitMemoryMB))
		}

		// 周期补齐 + 抖动（只延长不缩短）：interval - elapsed + jitter(0..interval/10)
		elapsed := w.Clock().Sub(start)
		wait := w.Interval - elapsed
		if wait < 0 {
			wait = 0
		}
		jitterMax := int(w.Interval / 10)
		if jitterMax < 1 {
			jitterMax = 1
		}
		wait += time.Duration(w.Rand(jitterMax))
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		w.Sleep(wait)
	}
}

// beforeLockRound 轮前钩子：分布式锁存在且获取失败时跳过该轮
// （无事件派发、handle 不执行——跳过语义由底座 RunOnce 承担）。
func (w *ProcWorker) beforeLockRound(ctx context.Context) bool {
	return w.lock == nil || w.lock.Get(ctx)
}

// releaseLock 正常完成钩子：handle 正常完成后 owner 安全释放
func (w *ProcWorker) releaseLock(ctx context.Context) {
	if w.lock != nil {
		w.lock.Release(ctx)
	}
}

// forceReleaseLock panic 收尾钩子：handle panic 后强制释放（不等 TTL），随后
// re-panic 上抛——panic 传播出 RunOnce/Run，由 WorkerManager 捕获派发
// worker.Panicked/worker.Stopped 生命周期事件、该 worker 停止（归生命周期事件管）
func (w *ProcWorker) forceReleaseLock(ctx context.Context, r any) {
	if w.lock != nil {
		w.lock.ForceRelease(ctx) // 异常分层释放：panic 强制释放不等 TTL
	}
	panic(r)
}

// startupDelay 启动延迟三态
func (w *ProcWorker) startupDelay() time.Duration {
	switch {
	case w.StartupDelay < 0:
		return time.Duration(1+w.Rand(5)) * time.Second
	case w.StartupDelay == 0:
		return 0
	default:
		return time.Duration(w.StartupDelay) * time.Second
	}
}

// defaultMemMB 默认内存读数（MB）：runtime.MemStats.Alloc
func defaultMemMB() int {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int(ms.Alloc / 1024 / 1024)
}

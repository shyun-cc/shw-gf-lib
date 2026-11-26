package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
	"github.com/shyun-cc/shw-gf-lib/redislock"
)

// newTestProc 快速构造注入版 ProcWorker（不真睡、固定随机）
func newTestProc(name string, handle func(ctx context.Context) error) *ProcWorker {
	w := NewProcWorker(name, 10*time.Millisecond, handle)
	w.Sleep = func(time.Duration) {}
	w.Rand = func(n int) int { return 0 }
	return w
}

// TestProc_RunWhenRegistered 注册即运行：不设任何环境变量，Run 被调用即执行
func TestProc_RunWhenRegistered(t *testing.T) {
	// Name() 返回构造传入的单名
	if got := NewProcWorker("x", 10*time.Millisecond, nil).Name(); got != "x" {
		t.Fatalf("Name() = %q, 期望 %q", got, "x")
	}

	var ran int32
	w := newTestProc("w1", func(ctx context.Context) error {
		atomic.AddInt32(&ran, 1)
		return nil
	})
	w.MaxLoops = 2 // 防无限
	go func() { _ = w.Run(context.Background()) }()
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&ran) == 0 {
		select {
		case <-deadline:
			t.Fatal("注册后 Run 应执行")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// TestProc_MaxLoopsSelfDestruct 循环上限自毁：CodeProcessIterationMax
func TestProc_MaxLoopsSelfDestruct(t *testing.T) {
	w := newTestProc("w3", func(ctx context.Context) error { return nil })
	w.MaxLoops = 3

	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background()) }()
	select {
	case err := <-done:
		appErr, ok := err.(*exception.AppError)
		if !ok || appErr.Code != exception.CodeProcessIterationMax {
			t.Fatalf("应报循环上限错误，实际 %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未在限期内自毁")
	}
}

// TestProc_MemoryLimitSelfDestruct 内存上限自毁：CodeProcessMemoryLimit
func TestProc_MemoryLimitSelfDestruct(t *testing.T) {
	mem := int32(10)
	w := newTestProc("w4", func(ctx context.Context) error {
		atomic.StoreInt32(&mem, 999) // 首轮执行后读数爆表
		return nil
	})
	w.MemFunc = func() int { return int(atomic.LoadInt32(&mem)) }
	w.LimitMemoryMB = 100

	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background()) }()
	select {
	case err := <-done:
		appErr, ok := err.(*exception.AppError)
		if !ok || appErr.Code != exception.CodeProcessMemoryLimit {
			t.Fatalf("应报内存上限错误，实际 %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未在限期内自毁")
	}
}

// TestProc_LockLayeredRelease 锁异常分层释放：正常 release / panic forceRelease
func TestProc_LockLayeredRelease(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()

	// 正常路径：handle 完成后 key 立即消失
	var rounds int32
	normal := newTestProc("w5", func(ctx context.Context) error {
		atomic.AddInt32(&rounds, 1)
		return nil
	})
	normal.LockKey = "proc-normal"
	normal.LockTTL = 30
	go func() { _ = normal.Run(ctx) }()
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&rounds) < 1 {
		select {
		case <-deadline:
			t.Fatal("正常路径未执行")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// release 是异步的：轮询等待 key 消失（上限 2s，避免固定 sleep 的竞态）
	waitUntil := time.Now().Add(2 * time.Second)
	for mr.Exists("redis-lock:proc-normal") && time.Now().Before(waitUntil) {
		time.Sleep(5 * time.Millisecond)
	}
	if mr.Exists("redis-lock:proc-normal") {
		t.Fatal("正常完成后锁应经 owner 释放消失")
	}

	// panic 路径：forceRelease，key 立即消失
	panicky := newTestProc("panicky", func(ctx context.Context) error {
		panic("boom")
	})
	panicky.LockKey = "proc-panic"
	panicky.LockTTL = 30
	panicky.MaxLoops = 1 // 第一轮 panic 后（re-panic 传播前先 forceRelease）
	panicked := make(chan struct{})
	go func() {
		defer close(panicked)
		defer func() { _ = recover() }() // 外层捕获 re-panic
		_ = panicky.Run(ctx)
	}()
	select {
	case <-panicked:
	case <-time.After(2 * time.Second):
		t.Fatal("panic 未传播")
	}
	if mr.Exists("redis-lock:proc-panic") {
		t.Fatal("panic 后锁应被 forceRelease（不等 TTL）")
	}
}

// TestProc_IntervalJitterOnlyExtends 周期间隔下界：抖动只延长不缩短
func TestProc_IntervalJitterOnlyExtends(t *testing.T) {
	var starts []time.Time
	fakeNow := int64(0)
	w := NewProcWorker("w6", 10*time.Millisecond, func(ctx context.Context) error {
		starts = append(starts, time.Unix(0, atomic.AddInt64(&fakeNow, 1)))
		return nil
	})
	w.Clock = func() time.Time { return time.Unix(0, atomic.LoadInt64(&fakeNow)) }
	w.Rand = func(n int) int { return n } // 最大抖动
	w.Sleep = func(d time.Duration) {
		atomic.AddInt64(&fakeNow, int64(d)) // 快进时钟
	}
	w.MaxLoops = 4

	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background()) }()
	select {
	case err := <-done:
		// MaxLoops=4：恰在第 5 次循环入口自毁（第 5 轮未执行），属预期行为
		if err != nil {
			if appErr, ok := err.(*exception.AppError); !ok || appErr.Code != exception.CodeProcessIterationMax {
				t.Fatalf("非预期错误: %v", err)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未完成")
	}
	// 相邻起点间隔 ≥ Interval（抖动只延长）——用记录的 fake 时刻序检查
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 10*time.Millisecond-1 {
			t.Fatalf("相邻轮起点间隔 %v 小于周期 10ms", starts[i].Sub(starts[i-1]))
		}
	}
}

// TestProc_StartupDelayTernary 启动延迟三态
func TestProc_StartupDelayTernary(t *testing.T) {
	// -1 随机 [1,5]s；>0 定值；0 无延迟
	w := newTestProc("d0", nil)
	if d := w.startupDelay(); d != 0 {
		t.Fatalf("StartupDelay=0 应无延迟，实际 %v", d)
	}
	w.StartupDelay = 7
	if d := w.startupDelay(); d != 7*time.Second {
		t.Fatalf("定值延迟不符: %v", d)
	}
	w.StartupDelay = -1
	w.Rand = func(n int) int { return n - 1 } // rand(5)=4 → 1+4=5s 上界
	if d := w.startupDelay(); d != 5*time.Second {
		t.Fatalf("随机上界应 5s: %v", d)
	}
	w.Rand = func(n int) int { return 0 }
	if d := w.startupDelay(); d != 1*time.Second {
		t.Fatalf("随机下界应 1s: %v", d)
	}
}

// 编译期引用（防止 import 裁剪误判）
var _ = redislock.Create

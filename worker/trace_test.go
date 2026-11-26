// Package worker 测试：周期每轮独立 traceId（对应 specs/trace-context 非 HTTP 场景）。
//
// 取证：捕获 glog 输出，两轮执行各产生一条带 {traceId} 的日志行，二者存在且互不相同。
package worker

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/exception"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"
)

var workerTraceIDRe = regexp.MustCompile(`\{([0-9a-f]{32})\}`)

func TestPeriodicWorker_EachRoundIndependentTraceId(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	g.Log().SetLevel(glog.LEVEL_ALL)
	g.Log().SetWriter(&buf)
	t.Cleanup(func() { g.Log().SetWriter(os.Stdout) })

	events := make(chan struct{}, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := NewPeriodicWorker("traced", 30*time.Millisecond, func(ctx context.Context) error {
		g.Log().Infof(ctx, "tick")
		events <- struct{}{}
		return nil
	})

	done := make(chan struct{})
	go func() { _ = w.Run(ctx); close(done) }()

	// 等两轮执行完成
	for i := 0; i < 2; i++ {
		select {
		case <-events:
		case <-time.After(3 * time.Second):
			t.Fatal("等待 tick 超时")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("等待 Run 退出超时")
	}

	mu.Lock()
	defer mu.Unlock()
	ids := workerTraceIDRe.FindAllStringSubmatch(buf.String(), -1)
	if len(ids) < 2 {
		t.Fatalf("两轮执行日志应各携带 traceId, 原始日志:\n%s", buf.String())
	}
	if ids[0][1] == ids[1][1] {
		t.Fatalf("两轮 traceId 应互不相同: %s", ids[0][1])
	}
}

func TestProcWorker_EachRoundIndependentTraceId(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	g.Log().SetLevel(glog.LEVEL_ALL)
	g.Log().SetWriter(&buf)
	t.Cleanup(func() { g.Log().SetWriter(os.Stdout) })

	events := make(chan struct{}, 4)

	// 注入版 ProcWorker：不真睡、固定随机、限两轮防无限
	w := NewProcWorker("proc-traced", 10*time.Millisecond, func(ctx context.Context) error {
		g.Log().Infof(ctx, "tick")
		events <- struct{}{}
		return nil
	})
	w.Sleep = func(time.Duration) {}
	w.Rand = func(n int) int { return 0 }
	w.MaxLoops = 2

	done := make(chan error, 1)
	go func() { done <- w.Run(context.Background()) }()

	// 等两轮执行完成
	for i := 0; i < 2; i++ {
		select {
		case <-events:
		case <-time.After(3 * time.Second):
			t.Fatal("等待 tick 超时")
		}
	}
	select {
	case err := <-done:
		// MaxLoops=2：第 3 次循环入口以 CodeProcessIterationMax 自毁退出，属预期
		if appErr, ok := err.(*exception.AppError); !ok || appErr.Code != exception.CodeProcessIterationMax {
			t.Fatalf("非预期错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待 Run 自毁退出超时")
	}

	mu.Lock()
	defer mu.Unlock()
	ids := workerTraceIDRe.FindAllStringSubmatch(buf.String(), -1)
	if len(ids) < 2 {
		t.Fatalf("两轮执行日志应各携带 traceId, 原始日志:\n%s", buf.String())
	}
	if ids[0][1] == ids[1][1] {
		t.Fatalf("两轮 traceId 应互不相同: %s", ids[0][1])
	}
}

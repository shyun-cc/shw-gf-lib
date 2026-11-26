package worker

import (
	"context"
	"os/signal"
	"sync"
	"syscall"

	"github.com/shyun-cc/shw-gf-lib/events"

	"github.com/gogf/gf/v2/frame/g"
)

// WorkerManager 管理多个 Worker 的生命周期
type WorkerManager struct {
	workers    []Worker
	wg         sync.WaitGroup
	dispatcher *events.Dispatcher
}

func NewWorkerManager() *WorkerManager {
	return &WorkerManager{}
}

func (m *WorkerManager) Register(w Worker) {
	m.workers = append(m.workers, w)
}

// SetDispatcher 注入独立事件分发器（测试或事件域隔离用；不设走 events.Default()）
func (m *WorkerManager) SetDispatcher(d *events.Dispatcher) {
	m.dispatcher = d
}

func (m *WorkerManager) Start(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Started 载荷的名单按注册顺序收集
	names := make([]string, 0, len(m.workers))
	for _, w := range m.workers {
		names = append(names, w.Name())
		m.wg.Add(1)
		go m.runWorker(ctx, w)
	}

	g.Log().Infof(ctx, "Worker 管理器已启动，共 %d 个 Worker，等待信号退出...", len(m.workers))
	// 全部 worker goroutine 已拉起、尚未等待退出，此时派发 Started 恰好一次
	dispatchEvent(m.dispatcher, ctx, EventWorkerStarted, WorkerStartedEvent{Names: names})
	m.wg.Wait()
	g.Log().Info(ctx, "所有 Worker 已停止")
	return nil
}

func (m *WorkerManager) runWorker(ctx context.Context, w Worker) {
	defer m.wg.Done()

	var err error
	// 收尾（含「已停止」日志与 Stopped 派发）放 defer：Run panic 被 recover 后
	// 函数体尾部不可达，只有 defer 收尾能保证 Panicked 与 Stopped 都恰好派发一次
	defer func() {
		if r := recover(); r != nil {
			g.Log().Errorf(ctx, "Worker %s 发生 panic: %v", w.Name(), r)
			dispatchEvent(m.dispatcher, ctx, EventWorkerPanicked, WorkerPanickedEvent{Name: w.Name(), Recovered: r})
		}
		g.Log().Infof(ctx, "Worker %s 已停止", w.Name())
		dispatchEvent(m.dispatcher, ctx, EventWorkerStopped, WorkerStoppedEvent{Name: w.Name(), Err: err})
	}()

	g.Log().Infof(ctx, "Worker %s 启动", w.Name())
	if err = w.Run(ctx); err != nil {
		g.Log().Errorf(ctx, "Worker %s 异常退出: %v", w.Name(), err)
	}
}

// Package events 事件分发器（PSR-14 语义 Go 化）。
//
// 解耦契约：组件作为发布方只 import 本包派发事件，不 import 任何监听方；
// 业务 handler 由项目 boot 层订阅装配。同一事件可注册任意多 handler，
// 按优先级有序（大值先执行，同优先级按注册顺序）同步执行；
// 单个 handler panic 不中断同事件后续 handler（recover + 日志）。
package events

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

// Event 事件对象：名字区分事件，载荷 any 由发布方与订阅方约定
type Event struct {
	Name         string
	Payload      any
	DispatchedAt time.Time
}

// Handler 事件处理器
type Handler func(ctx context.Context, e Event)

type subscription struct {
	priority int // 大值先执行
	seq      int // 注册顺序（同优先级稳定排序）
	handler  Handler
}

// Dispatcher 事件分发器（并发安全：订阅 RWMutex，派发遍历注册切片快照）
type Dispatcher struct {
	mu   sync.RWMutex
	subs map[string][]subscription
	seq  int
}

// New 创建独立分发器实例（测试或隔离域用；业务全局用 Default）
func New() *Dispatcher {
	return &Dispatcher{subs: make(map[string][]subscription)}
}

var (
	defaultOnce sync.Once
	defaultDisp *Dispatcher
)

// Default 进程级默认分发器（对齐 PHP 容器单例语义）
func Default() *Dispatcher {
	defaultOnce.Do(func() {
		defaultDisp = New()
	})
	return defaultDisp
}

// Subscribe 注册 handler 到默认分发器（priority 大值先执行）
func Subscribe(name string, priority int, handler Handler) {
	Default().Subscribe(name, priority, handler)
}

// Dispatch 经默认分发器派发事件
func Dispatch(ctx context.Context, name string, payload any) {
	Default().Dispatch(ctx, name, payload)
}

// Subscribe 注册 handler（priority 大值先执行，同优先级按注册顺序）
func (d *Dispatcher) Subscribe(name string, priority int, handler Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	d.subs[name] = append(d.subs[name], subscription{priority: priority, seq: d.seq, handler: handler})
	sort.SliceStable(d.subs[name], func(i, j int) bool {
		a, b := d.subs[name][i], d.subs[name][j]
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		return a.seq < b.seq
	})
}

// Dispatch 同步派发：遍历注册切片快照，逐个执行 handler；
// 每个 handler 独立 recover，panic 记日志后继续后续 handler
func (d *Dispatcher) Dispatch(ctx context.Context, name string, payload any) {
	d.mu.RLock()
	snapshot := make([]subscription, len(d.subs[name]))
	copy(snapshot, d.subs[name])
	d.mu.RUnlock()

	if len(snapshot) == 0 {
		return
	}

	e := Event{Name: name, Payload: payload, DispatchedAt: time.Now()}
	for _, s := range snapshot {
		d.run(ctx, e, s.handler)
	}
}

func (d *Dispatcher) run(ctx context.Context, e Event, h Handler) {
	defer func() {
		if r := recover(); r != nil {
			g.Log().Errorf(ctx, "[EVENTS] handler panic 事件=%s: %v", e.Name, r)
		}
	}()
	h(ctx, e)
}

package task

import "sync"

// Mode 任务类型的消费模式
type Mode int

const (
	// ModePull 拉取模式（默认）：pull worker 轮询 Claim 拉取执行，不经队列发消息
	ModePull Mode = iota
	// ModeQueue 队列模式：提交与重试均经 QueueProducer 发 task_id 消息，即时消费
	ModeQueue
)

// TypeRegistry type→mode 注册表：把 type 声明为 queue 模式的唯一入口。
// 注册后 Dispatcher.Submit 对该 type 一律发消息（无需逐次传 ViaQueue），
// RetryScanner 重试转回 pending 后重投——type 注册状态是重试重投的依据
// （ViaQueue 只是提交期一次性标志，不落表、不作为重试依据）。
// 并发安全：服务启动期可并发注册，运行期高频查询。
type TypeRegistry struct {
	mu    sync.RWMutex
	modes map[string]Mode
}

// NewTypeRegistry 建空注册表（所有 type 默认 pull 模式）
func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{modes: make(map[string]Mode)}
}

// Register 注册 type 为 queue 模式（重复注册幂等；零值 TypeRegistry 亦可直接调用）
func (r *TypeRegistry) Register(typ string) {
	r.mu.Lock()
	if r.modes == nil {
		r.modes = make(map[string]Mode)
	}
	r.modes[typ] = ModeQueue
	r.mu.Unlock()
}

// IsQueue type 是否注册为 queue 模式；未注册一律 false（视为 pull 模式）。
// 调用方持 nil *TypeRegistry 时应自行判空，等价空注册表
func (r *TypeRegistry) IsQueue(typ string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.modes[typ] == ModeQueue
}

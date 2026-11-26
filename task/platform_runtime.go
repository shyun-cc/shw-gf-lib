package task

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

// PlatformHandler 是项目在代码中注册的处理器。Runtime 只按已编译注册表映射类型到函数，
// 不反射执行任意方法、SQL、Shell 或页面上传代码。
type PlatformHandler struct {
	Version string // 稳定处理器版本，须与类型目录/运行快照的 HandlerVersion 一致
	// Validate 可选的参数 schema 校验；执行前失败按 permanent_failure 结束。
	// 项目的提交接口也可通过 Runtime.ValidatePayload 复用。
	Validate func(payload []byte) error
	Run      ManagedHandler
	// Splitter 可选：父任务按稳定业务键分批拆分子任务（与 Run 二选一，Splitter 优先）。
	// 每批“子任务+Outbox+游标”同事务；SplitBatchSize（默认 100，≤1000）为每批上限，
	// SplitBudget（默认 10）为单次尝试最多批数，超出则保留游标续派，父任务不长期占用执行名额。
	Splitter       Splitter
	SplitBatchSize int
	SplitBudget    int
	// Deprecated: Reconcile 仅保留源兼容，不再被 Runtime 调用。
	Reconcile func(context.Context, *Run) (string, error)
}

// RuntimeConfig 是统一 Worker 进程的装配参数。进程参数只保留环境、连接与资源硬上限，
// 启停/周期/并发等运行配置一律来自数据库（Store.TypeControl 必须开启）。
type RuntimeConfig struct {
	Store     *Store
	Transport Transport // nil 时以数据库 pull 领取；非 nil 时经 RabbitMQ 消费，需 Store.Outbox
	Handlers  map[string]PlatformHandler
	// Allowed 是部署许可的类型白名单；nil 表示允许全部已注册处理器。
	// 实例能力 = Handlers ∩ Allowed。
	Allowed map[string]bool
	// Types 启动时以 RegisterTypes 补齐缺失定义（新定义默认关闭）；可为空。
	Types []TypeSpec
	// Classes 消费的执行类别；空时取已注册处理器在类型目录中的类别。
	Classes      []string
	Concurrency  int // 本实例执行池硬上限（部署资源上限），必须 >=1
	Role         string
	BuildVersion string
	Host         string
	// Refresh 配置轮询间隔，默认 2s，最大 5s。通知可调用 Runtime.Refresh 加速。
	Refresh time.Duration
	// Poll 调度/投递/恢复的短轮询间隔，默认 1s，最大 10s；业务周期只由持久化 next_run_at 决定。
	Poll time.Duration
	// ShutdownGrace 停机时等待在途任务自然完成的时限，默认 30s；
	// CancelGrace 协作取消后等待 Handler 返回的时限，默认 10s。
	ShutdownGrace time.Duration
	CancelGrace   time.Duration
	// RepairAfter 已发送通知超过该时长仍未被领取即补投，默认 2min。
	RepairAfter time.Duration
	// DisableScheduler / DisableRelay / DisableRecovery 用于按角色拆分；默认全部开启，
	// 多实例同时运行安全（namespace 事务串行 + 唯一键）。
	DisableScheduler bool
	DisableRelay     bool
	DisableRecovery  bool
	// TraceID 为每次领取生成 attempt trace 标识（写入 attempt.trace_id，并注入 Handler/Splitter 的 ctx）。
	// 为空时每次 attempt 生成新的 OpenTelemetry traceId。返回 32 位十六进制 traceId 时同时注入
	// SpanContext（glog/gtrace/tracex.FromCtx 可读）；其他格式只能经 AttemptTraceID(ctx) 读取。
	TraceID func(context.Context) string
	// AttemptContext 可选：在注入 attempt trace 之后进一步装配执行 ctx（如项目日志字段），不得取消或替换 trace。
	AttemptContext func(context.Context, *Lease) context.Context
	// Redact 在结果落库前脱敏（错误摘要、Summary、Result 中的敏感信息）。
	Redact func(Completion) Completion
}

// RuntimeStatus 是实例本地运行状态（供健康检查/指标导出）。
type RuntimeStatus struct {
	InstanceID      string
	AppliedRevision int64
	ConfigStale     bool // 最近一次配置刷新失败，仍使用最后已验证配置
	LastError       string
	Running         int
	Capacity        int
}

// Runtime 统一 Worker：配置刷新、调度、Outbox 投递、消费/拉取、恢复与有界执行池。
type Runtime struct {
	cfg      RuntimeConfig
	store    *Store
	caps     map[string]string
	slots    chan struct{}
	refresh  chan struct{}
	inflight sync.WaitGroup
	running  atomic.Int32

	mu       sync.Mutex
	status   RuntimeStatus
	types    map[string]*TaskType
	instance string
	workCtx  context.Context
	stopWork context.CancelFunc
}

// NewRuntime 校验装配参数。
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	if cfg.Store == nil || !cfg.Store.cfg.TypeControl {
		return nil, fmt.Errorf("task: runtime requires a TypeControl store")
	}
	if cfg.Transport != nil && !cfg.Store.cfg.Outbox {
		return nil, fmt.Errorf("task: runtime transport requires an Outbox store")
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > 10000 {
		return nil, fmt.Errorf("task: runtime concurrency must be 1..10000")
	}
	if cfg.Refresh == 0 {
		cfg.Refresh = 2 * time.Second
	}
	if cfg.Poll == 0 {
		cfg.Poll = time.Second
	}
	if cfg.Refresh < 0 || cfg.Refresh > 5*time.Second || cfg.Poll < 0 || cfg.Poll > 10*time.Second {
		return nil, fmt.Errorf("task: runtime refresh must be <=5s and poll <=10s")
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = 30 * time.Second
	}
	if cfg.CancelGrace == 0 {
		cfg.CancelGrace = 10 * time.Second
	}
	if cfg.RepairAfter == 0 {
		cfg.RepairAfter = 2 * time.Minute
	}
	caps := map[string]string{}
	for typ, h := range cfg.Handlers {
		if (h.Run == nil && h.Splitter == nil) || h.Version == "" || h.SplitBatchSize < 0 || h.SplitBatchSize > 1000 || h.SplitBudget < 0 {
			return nil, fmt.Errorf("task: handler %s requires Version and Run or Splitter", typ)
		}
		if cfg.Allowed == nil || cfg.Allowed[typ] {
			caps[typ] = h.Version
		}
	}
	r := &Runtime{cfg: cfg, store: cfg.Store, caps: caps, slots: make(chan struct{}, cfg.Concurrency), refresh: make(chan struct{}, 1)}
	r.status.Capacity = cfg.Concurrency
	return r, nil
}

// Refresh 请求立即刷新配置（例如收到配置变更通知）；通知丢失由轮询补齐。
func (r *Runtime) Refresh() {
	select {
	case r.refresh <- struct{}{}:
	default:
	}
}

// Status 返回本地运行状态快照。
func (r *Runtime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.status
	s.Running = int(r.running.Load())
	return s
}

// ValidatePayload 用已注册处理器的 schema 校验参数（项目提交接口复用）。
func (r *Runtime) ValidatePayload(typ string, payload []byte) error {
	h, ok := r.cfg.Handlers[typ]
	if !ok {
		return ErrTypeUnavailable
	}
	if h.Validate != nil {
		return h.Validate(payload)
	}
	return nil
}

func (r *Runtime) setError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.status.ConfigStale = false
		r.status.LastError = ""
		return
	}
	r.status.ConfigStale = true
	r.status.LastError = err.Error()
}

// loadConfig 读取修订号与类型目录。启动阶段失败直接拒绝运行（不回退为“全部开启”）。
func (r *Runtime) loadConfig(ctx context.Context) (int64, error) {
	rev, err := r.store.ConfigRevision(ctx)
	if err != nil {
		return 0, err
	}
	types, err := r.store.Types(ctx)
	if err != nil {
		return 0, err
	}
	m := make(map[string]*TaskType, len(types))
	for _, t := range types {
		m[t.Type] = t
	}
	r.mu.Lock()
	r.types = m
	r.status.AppliedRevision = rev
	r.mu.Unlock()
	return rev, nil
}

func (r *Runtime) classes() []string {
	if len(r.cfg.Classes) > 0 {
		return r.cfg.Classes
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for typ := range r.caps {
		c := DefaultClass
		if t, ok := r.types[typ]; ok {
			c = t.Class
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// Run 启动统一 Worker，阻塞到 ctx 取消后按有界顺序停机：
// 停止调度/投递/新领取与取消订阅 → 等待在途（ShutdownGrace）→ 协作取消（CancelGrace）
// → 结果写回（未返回者由租约过期恢复）→ 登记实例停止。不会先 ACK 再退出丢失任务：
// 消息只在领取事务提交后 ACK，等待名额的消息在停机时 Nack(requeue)。
func (r *Runtime) Run(ctx context.Context) error {
	if len(r.cfg.Types) > 0 {
		if _, err := r.store.RegisterTypes(ctx, r.cfg.Types, nil); err != nil {
			return err
		}
	}
	rev, err := r.loadConfig(ctx)
	if err != nil {
		return fmt.Errorf("task: runtime refuses to start without config: %w", err)
	}
	inst, err := r.store.RegisterInstance(ctx, Instance{Role: r.cfg.Role, BuildVersion: r.cfg.BuildVersion, Host: r.cfg.Host, Capabilities: r.caps, Capacity: r.cfg.Concurrency})
	if err != nil {
		return err
	}
	if err = r.store.HeartbeatApplied(ctx, inst.ID, rev); err != nil {
		return err
	}
	r.mu.Lock()
	r.instance = inst.ID
	r.status.InstanceID = inst.ID
	r.workCtx, r.stopWork = context.WithCancel(context.WithoutCancel(ctx))
	r.mu.Unlock()
	defer r.stopWork()

	loopCtx, stopLoops := context.WithCancel(ctx)
	defer stopLoops()
	var loops sync.WaitGroup
	fatal := make(chan error, 1)
	start := func(fn func(context.Context) error) {
		loops.Add(1)
		go func() {
			defer loops.Done()
			if err := fn(loopCtx); err != nil && loopCtx.Err() == nil {
				select {
				case fatal <- err:
				default:
				}
				stopLoops()
			}
		}()
	}
	start(func(ctx context.Context) error { return r.refreshLoop(ctx, inst.ID, rev) })
	if !r.cfg.DisableScheduler {
		start(r.every(func(ctx context.Context) error { _, err := r.store.Tick(ctx, 100, nil); return err }))
	}
	if !r.cfg.DisableRecovery {
		start(r.every(r.recoverOnce))
	}
	if r.cfg.Transport != nil {
		if !r.cfg.DisableRelay {
			relay := &Relay{Store: r.store, Publisher: r.cfg.Transport, Owner: inst.ID}
			start(r.every(func(ctx context.Context) error {
				for {
					n, err := relay.RunOnce(ctx)
					if err != nil || n < 100 {
						return err
					}
				}
			}))
		}
		for _, class := range r.classes() {
			class := class
			start(func(ctx context.Context) error {
				return r.cfg.Transport.Consume(ctx, class, r.cfg.Concurrency, func(ctx context.Context, d Delivery) { r.deliver(ctx, inst.ID, d) })
			})
		}
	} else {
		start(func(ctx context.Context) error { return r.pullLoop(ctx, inst.ID) })
	}

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-fatal:
	}
	stopLoops()
	loops.Wait()
	r.drain()
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := r.store.StopInstance(stopCtx, inst.ID); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

// drain 等待在途执行：先给 ShutdownGrace 自然完成，再协作取消并最多等待 CancelGrace。
func (r *Runtime) drain() {
	done := make(chan struct{})
	go func() { r.inflight.Wait(); close(done) }()
	select {
	case <-done:
		return
	case <-time.After(r.cfg.ShutdownGrace):
	}
	r.stopWork()
	select {
	case <-done:
	case <-time.After(r.cfg.CancelGrace):
		g.Log().Warningf(context.Background(), "task runtime: %d handlers ignored cancellation; their leases will expire and be recovered", r.running.Load())
	}
}

func (r *Runtime) every(fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		t := time.NewTicker(r.cfg.Poll)
		defer t.Stop()
		for {
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				g.Log().Warningf(ctx, "task runtime loop error: %v", err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
		}
	}
}

func (r *Runtime) recoverOnce(ctx context.Context) error {
	if _, _, err := r.store.Recover(ctx, 100); err != nil {
		return err
	}
	if r.store.cfg.Outbox {
		_, err := r.store.RepairDispatch(ctx, 100, r.cfg.RepairAfter)
		return err
	}
	return nil
}

// refreshLoop 轮询配置修订号并上报已应用版本（兼作实例心跳）。刷新失败保留最后已验证配置并标记过期；
// 实例被判定停止（ErrConflict）属于致命错误，Runtime 退出由部署重启。
func (r *Runtime) refreshLoop(ctx context.Context, instance string, applied int64) error {
	t := time.NewTicker(r.cfg.Refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-r.refresh:
		}
		rev, err := r.store.ConfigRevision(ctx)
		if err == nil && rev != applied {
			rev, err = r.loadConfig(ctx)
		}
		r.setError(err)
		if err != nil {
			continue
		}
		applied = rev
		if err = r.store.HeartbeatApplied(ctx, instance, applied); errors.Is(err, ErrConflict) {
			return fmt.Errorf("task: runtime instance %s no longer active: %w", instance, err)
		}
		r.setError(err)
	}
}

// acquire 占用一个本地执行名额；ctx 取消返回 false。
func (r *Runtime) acquire(ctx context.Context) bool {
	select {
	case r.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *Runtime) release() { <-r.slots }

func (r *Runtime) traceID(ctx context.Context) string {
	if r.cfg.TraceID != nil {
		return r.cfg.TraceID(ctx)
	}
	return newTraceID()
}

// deliver 处理一条执行通知：先占本地名额（不足时有界持有，不热循环 requeue），
// 再由 Store.Accept 原子领取或持久化延期；只有责任落库后才 ACK。数据库故障时本地退避持有消息，
// 停机则 Nack(requeue) 归还。
func (r *Runtime) deliver(ctx context.Context, instance string, d Delivery) {
	if !r.acquire(ctx) {
		_ = d.Nack(true)
		return
	}
	backoff := 200 * time.Millisecond
	for {
		lease, decision, err := r.store.Accept(ctx, instance, d.Body(), r.traceID(ctx))
		if err == nil {
			switch decision {
			case DeliveryClaimed:
				if ackErr := d.Ack(); ackErr != nil {
					// 领取已落库：重投的副本将判为 stale，执行照常进行。
					g.Log().Warningf(ctx, "task runtime: ack after claim failed: %v", ackErr)
				}
				r.start(lease)
				return
			case DeliveryPoison:
				g.Log().Errorf(ctx, "task runtime: poison dispatch message isolated to dead letter: %q", truncate(d.Body(), 256))
				_ = d.Nack(false)
			default:
				_ = d.Ack()
			}
			r.release()
			return
		}
		g.Log().Warningf(ctx, "task runtime: accept failed, holding message without ack: %v", err)
		select {
		case <-ctx.Done():
			_ = d.Nack(true)
			r.release()
			return
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}

// pullLoop 无传输层时以数据库 pull 领取，名额满时等待，空闲时按 Poll 休眠。
func (r *Runtime) pullLoop(ctx context.Context, instance string) error {
	for {
		if !r.acquire(ctx) {
			return nil
		}
		lease, err := r.store.Claim(ctx, instance, "", r.traceID(ctx))
		if err != nil || lease == nil {
			r.release()
			if err != nil && ctx.Err() == nil {
				g.Log().Warningf(ctx, "task runtime: claim failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(r.cfg.Poll):
			}
			continue
		}
		r.start(lease)
	}
}

// start 在已占名额上异步执行一个租约，执行结束释放名额。
func (r *Runtime) start(lease *Lease) {
	r.inflight.Add(1)
	r.running.Add(1)
	go func() {
		defer r.inflight.Done()
		defer r.running.Add(-1)
		defer r.release()
		r.execute(lease)
	}()
}

// execute 运行 Handler：独立续租心跳，感知协作取消，deadline 到期交由恢复处置，
// 结果经脱敏钩子后写回。Handler 不响应取消时名额保持占用直到其返回（不强杀）。
func (r *Runtime) execute(lease *Lease) {
	r.mu.Lock()
	workCtx := r.workCtx
	r.mu.Unlock()
	// 每次 attempt 独立的 trace 上下文：业务 Handler/Splitter、续租、写回与日志共用同一 trace，
	// 与 AttemptView.TraceID 一致；不复用实例级 context 作为全轮 trace。
	workCtx = withAttempt(workCtx, lease)
	if r.cfg.AttemptContext != nil {
		workCtx = r.cfg.AttemptContext(workCtx, lease)
	}
	finish := func(c Completion) {
		if r.cfg.Redact != nil {
			c = r.cfg.Redact(c)
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(workCtx), 5*time.Second)
		defer cancel()
		if err := r.store.Complete(ctx, lease, c, nil); err != nil {
			// 写回失败（租约已失或数据库不可用）：由租约过期恢复负责，不在本地重放。
			g.Log().Warningf(ctx, "task runtime: complete %s failed: %v", lease.Run.ID, err)
		}
	}
	h, ok := r.cfg.Handlers[lease.Run.Type]
	if !ok || h.Version != lease.Run.Definition.HandlerVersion {
		finish(Completion{Outcome: OutcomeUnknown, ErrorCode: "handler_unavailable", ErrorSummary: "runtime handler capability mismatch"})
		return
	}
	if h.Validate != nil {
		if err := h.Validate(lease.Run.Payload); err != nil {
			finish(Completion{Outcome: OutcomePermanentFailure, ErrorCode: "invalid_payload", ErrorSummary: truncate([]byte(err.Error()), 2048)})
			return
		}
	}
	runCtx, cancel := context.WithCancel(workCtx)
	defer cancel()
	if lease.Deadline != nil {
		var stop context.CancelFunc
		runCtx, stop = context.WithDeadline(runCtx, *lease.Deadline)
		defer stop()
	}
	type result struct {
		c     Completion
		err   error
		split bool // 拆分尝试已由 FinishSplit 写回
	}
	done := make(chan result, 1)
	exec := &Execution{Lease: lease, Store: r.store}
	go func() {
		var res result
		defer func() {
			if recover() != nil {
				res = result{c: Completion{Outcome: OutcomeUnknown, ErrorCode: "handler_panic", ErrorSummary: "handler panicked"}, err: fmt.Errorf("task: handler panic")}
			}
			done <- res
		}()
		if h.Splitter != nil {
			res.split, res.err = r.split(runCtx, exec, h)
			if res.split {
				return
			}
			if res.err != nil && runCtx.Err() == nil {
				res.c = Completion{Outcome: OutcomeRetryableFailure, ErrorCode: "split_failed"}
			}
			return
		}
		res.c, res.err = h.Run(runCtx, exec)
	}()
	interval := lease.Run.Definition.Lease / 3
	if interval > 10*time.Second {
		interval = 10 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	lost := false
	for {
		select {
		case res := <-done:
			if lost {
				return
			}
			if res.split {
				return
			}
			if runCtx.Err() == context.DeadlineExceeded {
				r.expire(workCtx)
				return
			}
			finish(normalize(res.c, res.err, runCtx.Err() != nil))
			return
		case <-tick.C:
			if lost {
				continue
			}
			beat, stop := context.WithTimeout(context.WithoutCancel(workCtx), interval)
			err := r.store.Renew(beat, lease)
			stop()
			if errors.Is(err, ErrCancelRequested) {
				cancel()
				continue
			}
			if err != nil {
				// 租约已失：取消 Handler，等待其返回后放弃写回（旧写入会被拒绝）。
				lost = true
				cancel()
			}
		}
	}
}

// expire 在业务 deadline 到期后立即触发恢复，由恢复策略决定重试或异常终结。
func (r *Runtime) expire(parent context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	if _, _, err := r.store.Recover(ctx, 100); err != nil {
		g.Log().Warningf(ctx, "task runtime: recover after deadline failed: %v", err)
	}
}

// normalize 把 Handler 返回值规整为合法 Completion：带 error 的成功类结果视为可重试失败；
// 被取消（停机或协作取消）且 Handler 未给出结论时视为 interrupted。
func normalize(c Completion, err error, cancelled bool) Completion {
	if err != nil {
		if c.Outcome == "" || c.Outcome == OutcomeSuccess || c.Outcome == OutcomePartial || c.Outcome == OutcomeSkipped {
			c.Outcome = OutcomeRetryableFailure
			if cancelled {
				c.Outcome = OutcomeInterrupted
			}
		}
		if c.ErrorCode == "" {
			c.ErrorCode = "handler_error"
		}
		if c.ErrorSummary == "" {
			c.ErrorSummary = truncate([]byte(err.Error()), 2048)
		}
	}
	if c.Outcome == "" {
		c.Outcome = OutcomeSuccess
		if cancelled {
			c.Outcome = OutcomeInterrupted
		}
	}
	return c
}

// split 执行父任务的一次拆分尝试：从持久化游标继续，最多 SplitBudget 批，
// 每批经 AppendSplit 与游标同事务落库，最后 FinishSplit 进入等待子任务或续派。
// 返回 true 表示已写回；返回 error 时游标保留，按父任务恢复策略重试。
func (r *Runtime) split(ctx context.Context, exec *Execution, h PlatformHandler) (bool, error) {
	size, budget := h.SplitBatchSize, h.SplitBudget
	if size == 0 {
		size = 100
	}
	if budget == 0 {
		budget = 10
	}
	cursor, err := r.store.SplitCursor(ctx, exec.Lease)
	if err != nil {
		return false, err
	}
	for i := 0; i < budget; i++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		batch, err := h.Splitter(ctx, exec.Lease.Run, cursor, size)
		if err != nil {
			return false, err
		}
		if len(batch.Children) > size {
			return false, fmt.Errorf("task: splitter returned %d children above limit %d", len(batch.Children), size)
		}
		if _, err = r.store.AppendSplit(context.WithoutCancel(ctx), exec.Lease, batch, nil); err != nil {
			return false, err
		}
		cursor = batch.Cursor
		if batch.Done {
			break
		}
	}
	if err := r.store.FinishSplit(context.WithoutCancel(ctx), exec.Lease); err != nil {
		return false, err
	}
	return true, nil
}

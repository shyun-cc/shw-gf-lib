package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
)

var (
	ErrLeaseLost = errors.New("task: lease_lost")
	ErrConflict  = errors.New("task: version_or_state_conflict")
	ErrNotFound  = errors.New("task: not_found")
)

const (
	// StatusAbnormal 是异常终态，仅供复盘，不等待确认或阻塞后续运行。
	StatusAbnormal = "abnormal"
	// Deprecated: 仅用于识别旧存储值；查询返回 StatusAbnormal。
	StatusNeedsReview = "needs_review"
	// RecoveryStop 不重试不确定副作用，异常结束当前运行。
	RecoveryStop  = "stop_on_failure"
	RecoveryRetry = "idempotent_retry"
	// Deprecated: 兼容旧快照，行为等同 RecoveryStop。
	RecoveryReview = "needs_review"
	// Deprecated: 兼容旧快照，行为等同 RecoveryStop，不再调用核对器。
	RecoveryReconcile = "reconcile_first"
	MisfireCoalesce   = "coalesce"
	MisfireSkip       = "skip"
	MisfireCatchUp    = "catch_up"
	TimeInstant       = "timestamptz"
	TimeWall          = "timestamp"
)

// StoreConfig 为项目注入数据库、时钟与时间存储模式。Namespace 必须非空。
type StoreConfig struct {
	DB        gdb.DB
	Namespace string
	Now       func() time.Time
	TimeMode  string
	Location  *time.Location
	// TypeControl 开启持久化类型目录控制：提交与领取都要求 task_type 存在，
	// 并受提交/消费开关、类型全局并发与限速约束；运行策略取自类型目录。
	// 默认 false 不要求类型目录；异常终态语义不受此开关影响。
	TypeControl bool
	// Outbox 开启事务内执行通知：创建运行、到期重试转 pending 时同事务写 task_outbox，
	// 由 Relay 经 RabbitMQ 等传输层投递。默认 false（v0.12 纯 DB pull）。
	Outbox bool
}

// RetryOptions 的 MaxAttempts 包含首次；Jitter 为 [0,1] 的随机抖动比例。
type RetryOptions struct {
	MaxAttempts int
	Base        time.Duration
	Factor      float64
	Max         time.Duration
	Jitter      float64
}

func (r RetryOptions) validate() error {
	if r.MaxAttempts < 1 || r.Base < 0 || r.Max < r.Base || r.Factor < 1 || math.IsNaN(r.Factor) || math.IsInf(r.Factor, 0) || r.Jitter < 0 || r.Jitter > 1 || math.IsNaN(r.Jitter) {
		return fmt.Errorf("task: invalid retry options")
	}
	return nil
}
func (r RetryOptions) delay(attempt int, random float64) time.Duration {
	n := math.Max(0, float64(attempt-1))
	v := math.Min(float64(r.Max), float64(r.Base)*math.Pow(r.Factor, n))
	if math.IsNaN(v) {
		v = 0
	}
	return time.Duration(math.Min(float64(r.Max), v*(1-r.Jitter+r.Jitter*random)))
}

// Definition 是每轮运行的不可变策略快照。Recovery 必须显式声明。
type Definition struct {
	Type           string
	HandlerVersion string
	Payload        []byte
	Priority       int
	Timeout        time.Duration
	Lease          time.Duration
	Retry          RetryOptions
	Recovery       string
	ConcurrencyKey string
	Class          string `json:",omitempty"` // 执行类别（RabbitMQ 路由键）；TypeControl 下取自类型目录
	// OrderingKey 非空时按 (OrderScope, OrderingKey) 严格有序：同键按持久化序号执行，
	// 前项尚未终结时阻塞后项，不同键可并发。OrderScope 为空时取 Type。
	OrderingKey string `json:",omitempty"`
	OrderScope  string `json:",omitempty"`
}

func (d Definition) validate() error {
	if len(d.Payload) > 65536 || len(d.Payload) > 0 && !json.Valid(d.Payload) {
		return fmt.Errorf("task: invalid payload")
	}
	if d.Type == "" || len(d.Type) > 64 || d.HandlerVersion == "" || d.Timeout < 0 || d.Lease < time.Second || len(d.OrderingKey) > 256 || len(d.OrderScope) > 64 {
		return fmt.Errorf("task: invalid definition")
	}
	if d.Recovery != RecoveryStop && d.Recovery != RecoveryRetry && d.Recovery != RecoveryReview && d.Recovery != RecoveryReconcile {
		return fmt.Errorf("task: explicit recovery policy required")
	}
	return d.Retry.validate()
}

// Schedule 的 Version 用于管理 CAS，DefinitionVersion 用于拒绝旧镜像覆盖登记。
type Schedule struct {
	MisfireFrom       *time.Time
	MisfireUntil      *time.Time // 最近合并/跳过所评估的计划区间 [from,until)
	ID                string
	Name              string
	Enabled           bool
	Version           int64
	DefinitionVersion int64
	Definition        Definition
	Kind              string
	Interval          time.Duration
	Cron              string
	Timezone          string
	Anchor            time.Time
	NextRunAt         time.Time
	LastScheduledAt   *time.Time
	Misfire           string
	MaxCatchUp        int
	MissedCount       int64
}

// Run 复用 Task，增加持久化调度与执行控制字段。
type Run struct {
	Task
	Namespace          string
	ScheduleID         string
	ScheduleVersion    int64
	ScheduledAt        *time.Time
	Trigger            string
	RequestKey         string
	ReplayOf           string
	Definition         Definition
	ActiveAttemptID    string
	Version            int64
	Outcome            string
	ErrorCode          string
	Summary            []byte
	DispatchGeneration int64 // 当前有效执行通知代次（Outbox 模式）
	CancelRequested    bool  // 已请求协作取消（运行中）
	OrderSeq           int64 // 严格有序序号（OrderingKey 非空时，与创建同事务分配）
}

// Lease 是写进度、续租和终态的凭据，禁止写入日志或暴露管理响应。
type Lease struct {
	Run       *Run
	AttemptID string
	Token     string
	ExpiresAt time.Time
	Deadline  *time.Time
	// TraceID 是本次 attempt 落库的 trace 标识（即 AttemptView.TraceID），可能为空。
	TraceID string
}

// Completion 描述业务结果；ErrorSummary 及 Summary 由项目脱敏，大小受库限制。
type Completion struct {
	Outcome      string // success/partial/skipped/retryable_failure(=failed)/permanent_failure/unknown/interrupted
	ErrorCode    string
	ErrorSummary string
	Summary      []byte
	Result       []byte
	BusinessRef  string
	Stats        map[string]int64 // 有界结构化计数，最多 64 项，键 ≤64 字节
}

// Instance 每次进程启动应使用新 ID；Capabilities 是部署许可后的 type→version 白名单。
type Instance struct {
	ID           string
	Role         string
	BuildVersion string
	Host         string
	Capabilities map[string]string
	Capacity     int
	StartedAt    time.Time
	HeartbeatAt  time.Time
	StoppedAt    *time.Time
	// AppliedRevision 是实例最近上报的已应用配置修订号（只读，由 HeartbeatApplied 写入）。
	AppliedRevision int64 `json:"-"`
}

// Audit 在同一数据库事务内调用，失败会回滚操作；不得执行外部副作用。
type Audit func(context.Context, gdb.TX) error

// Page 限制管理查询范围。Offset 分页用于管理展示，不作任务检查点。
type Page struct {
	Limit  int
	Offset int
}

func (p Page) bounds() (int, int) {
	n := p.Limit
	if n <= 0 {
		n = 50
	}
	if n > 200 {
		n = 200
	}
	o := p.Offset
	if o < 0 {
		o = 0
	}
	return n, o
}

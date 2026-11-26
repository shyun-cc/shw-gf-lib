package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/google/uuid"
)

var (
	// ErrTypeUnavailable 类型目录缺定义（TypeControl 下拒绝提交与领取）。
	ErrTypeUnavailable = errors.New("task: type_unavailable")
	// ErrSubmitDisabled 类型业务提交开关关闭。
	ErrSubmitDisabled = errors.New("task: submit_disabled")
)

// Completion.Outcome 取值。failed 为 v0.12 兼容写法，等同 retryable_failure。
const (
	OutcomeSuccess          = "success"
	OutcomePartial          = "partial"
	OutcomeSkipped          = "skipped"
	OutcomeRetryableFailure = "retryable_failure"
	OutcomeFailed           = "failed"
	OutcomePermanentFailure = "permanent_failure"
	OutcomeUnknown          = "unknown"
	OutcomeInterrupted      = "interrupted"
)

// DefaultClass 为未声明执行类别的类型所用类别。
const DefaultClass = "default"

// TypePolicy 是类型目录中的执行策略，派发新运行时快照进 Definition。
// 已生成运行保留原快照，修改只影响未来运行。
type TypePolicy struct {
	Priority int
	Timeout  time.Duration
	Lease    time.Duration
	Retry    RetryOptions
	Recovery string
}

func (p TypePolicy) validate() error {
	d := Definition{Type: "x", HandlerVersion: "x", Priority: p.Priority, Timeout: p.Timeout, Lease: p.Lease, Retry: p.Retry, Recovery: p.Recovery}
	return d.validate()
}

// TypeSpec 是代码侧已编译注册的类型声明；RegisterTypes 只用它补齐缺失定义。
type TypeSpec struct {
	Type              string
	HandlerVersion    string
	Class             string
	GlobalConcurrency int // 默认 1
	Policy            TypePolicy
}

// TypeConfig 是管理端可修改的类型运行配置。
// SubmitEnabled/ConsumeEnabled/GlobalConcurrency/Rate* 属实时控制层，领取时即时生效；
// HandlerVersion/Policy 只影响之后创建的运行。
type TypeConfig struct {
	HandlerVersion    string
	Class             string
	SubmitEnabled     bool
	ConsumeEnabled    bool
	GlobalConcurrency int           // 跨全部实例的同时运行上限，1..10000
	RateLimit         int           // 窗口内最多开始的尝试数，0 不限
	RateWindow        time.Duration // RateLimit>0 时必须 >=1s
	Policy            TypePolicy
}

func (c TypeConfig) validate() error {
	if c.HandlerVersion == "" || len(c.HandlerVersion) > 64 || c.Class == "" || len(c.Class) > 64 {
		return fmt.Errorf("task: invalid type handler version or class")
	}
	if c.GlobalConcurrency < 1 || c.GlobalConcurrency > 10000 || c.RateLimit < 0 || c.RateLimit > 1000000 {
		return fmt.Errorf("task: invalid type concurrency or rate limit")
	}
	if c.RateLimit > 0 && (c.RateWindow < time.Second || c.RateWindow > 24*time.Hour) {
		return fmt.Errorf("task: rate window must be 1s..24h")
	}
	return c.Policy.validate()
}

// TaskType 是持久化类型目录行。Version 用于管理 CAS。
type TaskType struct {
	TypeConfig
	Type         string
	Version      int64
	RegisteredAt time.Time
	UpdatedAt    time.Time
}

func (s *Store) taskType(row gdb.Record) (*TaskType, error) {
	if row.IsEmpty() {
		return nil, ErrNotFound
	}
	v := &TaskType{Type: row["type"].String(), Version: row["version"].Int64(), RegisteredAt: s.readTime(row["registered_at"]), UpdatedAt: s.readTime(row["updated_at"])}
	v.HandlerVersion = row["handler_version"].String()
	v.Class = row["class"].String()
	v.SubmitEnabled = row["submit_enabled"].Bool()
	v.ConsumeEnabled = row["consume_enabled"].Bool()
	v.GlobalConcurrency = row["global_concurrency"].Int()
	v.RateLimit = row["rate_limit"].Int()
	v.RateWindow = time.Duration(row["rate_window_ms"].Int64()) * time.Millisecond
	if err := json.Unmarshal([]byte(row["policy"].String()), &v.Policy); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Store) bumpRevision(tx gdb.TX) error {
	_, err := tx.Exec(`INSERT INTO task_config_revision(namespace,revision) VALUES(?,1) ON CONFLICT(namespace) DO UPDATE SET revision=task_config_revision.revision+1`, s.cfg.Namespace)
	return err
}

// ConfigRevision 返回 namespace 配置修订号；任何类型/计划配置变更都会递增。
func (s *Store) ConfigRevision(ctx context.Context) (int64, error) {
	row, err := s.cfg.DB.GetOne(ctx, `SELECT revision FROM task_config_revision WHERE namespace=?`, s.cfg.Namespace)
	if err != nil {
		return 0, err
	}
	return row["revision"].Int64(), nil
}

// RegisterTypes 只插入缺失类型；新类型提交与消费默认关闭，需管理端显式开通。
// 已有类型的配置、开关和版本不被覆盖。新插入时调用一次 Audit。
func (s *Store) RegisterTypes(ctx context.Context, specs []TypeSpec, fn Audit) (out []*TaskType, err error) {
	for _, spec := range specs {
		if spec.Type == "" || len(spec.Type) > 64 {
			return nil, fmt.Errorf("task: invalid type")
		}
		if err = spec.config().validate(); err != nil {
			return nil, err
		}
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		inserted := false
		for _, spec := range specs {
			cfg := spec.config()
			policy, e := encoded(cfg.Policy)
			if e != nil {
				return e
			}
			now := s.stamp(s.now())
			res, e := tx.Exec(`INSERT INTO task_type(namespace,type,handler_version,class,submit_enabled,consume_enabled,global_concurrency,rate_limit,rate_window_ms,policy,version,registered_at,updated_at) VALUES(?,?,?,?,FALSE,FALSE,?,0,0,CAST(? AS JSONB),1,?,?) ON CONFLICT(namespace,type) DO NOTHING`, s.cfg.Namespace, spec.Type, cfg.HandlerVersion, cfg.Class, cfg.GlobalConcurrency, policy, now, now)
			if e != nil {
				return e
			}
			if n, e := res.RowsAffected(); e != nil {
				return e
			} else if n == 1 {
				inserted = true
			}
			row, e := tx.GetOne(`SELECT * FROM task_type WHERE namespace=? AND type=?`, s.cfg.Namespace, spec.Type)
			if e != nil {
				return e
			}
			v, e := s.taskType(row)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		if !inserted {
			return nil
		}
		if e := s.bumpRevision(tx); e != nil {
			return e
		}
		return audit(ctx, tx, fn)
	})
	if err != nil {
		out = nil
	}
	return
}

func (t TypeSpec) config() TypeConfig {
	c := TypeConfig{HandlerVersion: t.HandlerVersion, Class: t.Class, GlobalConcurrency: t.GlobalConcurrency, Policy: t.Policy}
	if c.Class == "" {
		c.Class = DefaultClass
	}
	if c.GlobalConcurrency == 0 {
		c.GlobalConcurrency = 1
	}
	return c
}

// ConfigureType 以 expected version CAS 替换类型配置；requestKey 幂等，重复命中不再审计。
func (s *Store) ConfigureType(ctx context.Context, typ string, expected int64, cfg TypeConfig, key string, fn Audit) (out *TaskType, err error) {
	if err = cfg.validate(); err != nil {
		return nil, err
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, typeTarget(typ), "type_configure", []any{expected, cfg})
		if e != nil {
			return e
		}
		if !done {
			policy, e := encoded(cfg.Policy)
			if e != nil {
				return e
			}
			res, e := tx.Exec(`UPDATE task_type SET handler_version=?,class=?,submit_enabled=?,consume_enabled=?,global_concurrency=?,rate_limit=?,rate_window_ms=?,policy=CAST(? AS JSONB),version=version+1,updated_at=? WHERE namespace=? AND type=? AND version=?`, cfg.HandlerVersion, cfg.Class, cfg.SubmitEnabled, cfg.ConsumeEnabled, cfg.GlobalConcurrency, cfg.RateLimit, cfg.RateWindow.Milliseconds(), policy, s.stamp(s.now()), s.cfg.Namespace, typ, expected)
			if e != nil {
				return e
			}
			if n, e := res.RowsAffected(); e != nil {
				return e
			} else if n != 1 {
				return ErrConflict
			}
			if e = s.bumpRevision(tx); e != nil {
				return e
			}
			if e = audit(ctx, tx, fn); e != nil {
				return e
			}
		}
		row, e := tx.GetOne(`SELECT * FROM task_type WHERE namespace=? AND type=?`, s.cfg.Namespace, typ)
		if e != nil {
			return e
		}
		out, e = s.taskType(row)
		return e
	})
	if err != nil {
		out = nil
	}
	return
}

// SetTypeSwitches 单独切换业务提交与消费开关（CAS + 幂等 + 同事务审计）。
// 关闭消费不丢弃任务，积压保留至重新开启。
func (s *Store) SetTypeSwitches(ctx context.Context, typ string, expected int64, submit, consume bool, key string, fn Audit) (*TaskType, error) {
	var out *TaskType
	err := s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, typeTarget(typ), "type_switch", []any{expected, submit, consume})
		if e != nil {
			return e
		}
		if !done {
			res, e := tx.Exec(`UPDATE task_type SET submit_enabled=?,consume_enabled=?,version=version+1,updated_at=? WHERE namespace=? AND type=? AND version=?`, submit, consume, s.stamp(s.now()), s.cfg.Namespace, typ, expected)
			if e != nil {
				return e
			}
			if n, e := res.RowsAffected(); e != nil {
				return e
			} else if n != 1 {
				return ErrConflict
			}
			if e = s.bumpRevision(tx); e != nil {
				return e
			}
			if e = audit(ctx, tx, fn); e != nil {
				return e
			}
		}
		row, e := tx.GetOne(`SELECT * FROM task_type WHERE namespace=? AND type=?`, s.cfg.Namespace, typ)
		if e != nil {
			return e
		}
		out, e = s.taskType(row)
		return e
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// typeTarget 把类型名映射为命令账本 target_id（UUID 列）的稳定值。
func typeTarget(typ string) string { return uuidFromName("type:" + typ) }

// GetType 查询单个类型配置。
func (s *Store) GetType(ctx context.Context, typ string) (*TaskType, error) {
	row, err := s.cfg.DB.GetOne(ctx, `SELECT * FROM task_type WHERE namespace=? AND type=?`, s.cfg.Namespace, typ)
	if err != nil {
		return nil, err
	}
	return s.taskType(row)
}

// Types 列出 namespace 下全部类型配置（类型数量有限，不分页）。
func (s *Store) Types(ctx context.Context) ([]*TaskType, error) {
	rows, err := s.cfg.DB.GetAll(ctx, `SELECT * FROM task_type WHERE namespace=? ORDER BY type`, s.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	out := make([]*TaskType, 0, len(rows))
	for _, row := range rows {
		v, err := s.taskType(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// prepare 在 TypeControl 下用当前类型策略补全运行快照；submit 为 true 时检查业务提交开关。
func (s *Store) prepare(tx gdb.TX, def Definition, submit bool) (Definition, error) {
	if !s.cfg.TypeControl {
		return def, def.validate()
	}
	row, err := tx.GetOne(`SELECT * FROM task_type WHERE namespace=? AND type=?`, s.cfg.Namespace, def.Type)
	if err != nil {
		return def, err
	}
	if row.IsEmpty() {
		return def, ErrTypeUnavailable
	}
	t, err := s.taskType(row)
	if err != nil {
		return def, err
	}
	if submit && !t.SubmitEnabled {
		return def, ErrSubmitDisabled
	}
	def.HandlerVersion = t.HandlerVersion
	if def.Priority == 0 {
		def.Priority = t.Policy.Priority
	}
	def.Timeout = t.Policy.Timeout
	def.Lease = t.Policy.Lease
	def.Retry = t.Policy.Retry
	def.Recovery = t.Policy.Recovery
	def.Class = t.Class
	return def, def.validate()
}

// sameRequest 判断同一请求键的重复提交是否为同一请求。
// TypeControl 下策略字段来自类型目录，只比较调用方控制的字段。
func (s *Store) sameRequest(stored, in Definition) bool {
	if s.cfg.TypeControl {
		return stored.Type == in.Type && string(stored.Payload) == string(in.Payload) && stored.ConcurrencyKey == in.ConcurrencyKey && stored.OrderingKey == in.OrderingKey && stored.OrderScope == in.OrderScope
	}
	a, _ := encoded(stored)
	b, _ := encoded(in)
	return a == b
}

// settle 依据结果与恢复策略计算运行的下一状态与可领取时点（纯函数，便于单测）。
func settle(def Definition, attempts, maxAttempts int, outcome string, now time.Time, random float64) (string, time.Time, bool) {
	switch outcome {
	case OutcomeSuccess, OutcomePartial, OutcomeSkipped:
		return StatusSucceeded, time.Time{}, false
	case OutcomePermanentFailure:
		return StatusAbnormal, time.Time{}, false
	case OutcomeUnknown:
		return StatusAbnormal, time.Time{}, false
	case OutcomeFailed, OutcomeRetryableFailure, OutcomeInterrupted:
		if def.Recovery != RecoveryRetry {
			return StatusAbnormal, time.Time{}, false
		}
		if attempts >= maxAttempts {
			return StatusAbnormal, time.Time{}, false
		}
		return StatusFailed, now.Add(def.Retry.delay(attempts, random)), true
	}
	return StatusAbnormal, time.Time{}, false
}

func randomUnit() float64 { return rand.Float64() }

func uuidFromName(name string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gflib/task/"+name)).String()
}

package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/google/uuid"
)

// Store 以同一 namespace 的短事务锁统一调度、领取、管理与回收，不在事务内执行 Handler。
type Store struct{ cfg StoreConfig }

func NewStore(cfg StoreConfig) (*Store, error) {
	if cfg.DB == nil || cfg.Namespace == "" {
		return nil, fmt.Errorf("task: DB and namespace required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TimeMode == "" {
		cfg.TimeMode = TimeInstant
	}
	if cfg.TimeMode != TimeInstant && cfg.TimeMode != TimeWall {
		return nil, fmt.Errorf("task: invalid time mode")
	}
	if cfg.TimeMode == TimeWall && cfg.Location == nil {
		return nil, fmt.Errorf("task: wall time requires explicit Location")
	}
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	// Timestamp cannot distinguish repeated civil times; keep support deliberately limited.
	if cfg.TimeMode == TimeWall && cfg.Location.String() != "Asia/Shanghai" && cfg.Location.String() != "UTC" {
		return nil, fmt.Errorf("task: wall time supports UTC or Asia/Shanghai")
	}
	return &Store{cfg: cfg}, nil
}
func (s *Store) now() time.Time { return s.cfg.Now().Truncate(time.Microsecond) }
func (s *Store) stamp(t time.Time) string {
	if s.cfg.TimeMode == TimeWall {
		return t.In(s.cfg.Location).Format("2006-01-02 15:04:05.999999")
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func (s *Store) readTime(v *gvar.Var) time.Time {
	if v == nil || v.IsNil() {
		return time.Time{}
	}
	if s.cfg.TimeMode == TimeInstant {
		return v.Time()
	}
	if t, ok := v.Val().(time.Time); ok {
		y, m, d := t.Date()
		h, n, sec := t.Clock()
		return time.Date(y, m, d, h, n, sec, t.Nanosecond(), s.cfg.Location)
	}
	raw := v.String()
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
		if t, e := time.ParseInLocation(layout, raw, s.cfg.Location); e == nil {
			return t
		}
	}
	return time.Time{}
}
func (s *Store) columnType() string {
	if s.cfg.TimeMode == TimeWall {
		return "TIMESTAMP"
	}
	return "TIMESTAMPTZ"
}
func newID() (string, error)        { id, err := uuid.NewV7(); return id.String(), err }
func encoded(v any) (string, error) { b, e := json.Marshal(v); return string(b), e }
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (s *Store) transaction(ctx context.Context, fn func(context.Context, gdb.TX) error) error {
	return s.cfg.DB.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

// lock 校验时间模式并取得 namespace 事务级 advisory lock（随事务提交/回滚释放）。
func (s *Store) lock(ctx context.Context, tx gdb.TX) error {
	if err := s.checkSchema(ctx, tx); err != nil {
		return err
	}
	_, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, "gflib/task/"+s.cfg.Namespace)
	return err
}
func audit(ctx context.Context, tx gdb.TX, fn Audit) error {
	if fn != nil {
		return fn(ctx, tx)
	}
	return nil
}
func (s *Store) schedule(row gdb.Record) (*Schedule, error) {
	if row.IsEmpty() {
		return nil, ErrNotFound
	}
	var v Schedule
	if err := json.Unmarshal([]byte(row["spec"].String()), &v); err != nil {
		return nil, err
	}
	v.ID = row["id"].String()
	v.Name = row["name"].String()
	v.Enabled = row["enabled"].Bool()
	v.Version = row["version"].Int64()
	v.DefinitionVersion = row["definition_version"].Int64()
	v.NextRunAt = s.readTime(row["next_run_at"])
	v.MissedCount = row["missed_count"].Int64()
	if t := s.readTime(row["misfire_from"]); !t.IsZero() {
		v.MisfireFrom = &t
	}
	if t := s.readTime(row["misfire_until"]); !t.IsZero() {
		v.MisfireUntil = &t
	}
	if t := s.readTime(row["last_scheduled_at"]); !t.IsZero() {
		v.LastScheduledAt = &t
	}
	return &v, nil
}
func (s *Store) run(row gdb.Record) (*Run, error) {
	if row.IsEmpty() {
		return nil, ErrNotFound
	}
	v := &Run{Task: *hydrateTask(row), Namespace: row["namespace"].String(), ScheduleID: row["schedule_id"].String(), ScheduleVersion: row["schedule_version"].Int64(), Trigger: row["trigger_type"].String(), RequestKey: row["request_key"].String(), ReplayOf: row["replay_of"].String(), ActiveAttemptID: row["active_attempt_id"].String(), Version: row["version"].Int64(), Outcome: row["outcome"].String(), ErrorCode: row["error_code"].String(), Summary: []byte(row["summary"].String()), DispatchGeneration: row["dispatch_generation"].Int64(), CancelRequested: row["cancel_requested"].Bool(), OrderSeq: row["order_seq"].Int64()}
	if err := json.Unmarshal([]byte(row["definition"].String()), &v.Definition); err != nil {
		return nil, err
	}
	v.Status = publicStatus(v.Status)
	v.AvailableAt = s.readTime(row["available_at"])
	for k, p := range map[string]**time.Time{"scheduled_at": &v.ScheduledAt, "started_at": &v.StartedAt, "finished_at": &v.FinishedAt, "created_at": &v.CreatedAt, "updated_at": &v.UpdatedAt} {
		if t := s.readTime(row[k]); !t.IsZero() {
			*p = &t
		}
	}
	return v, nil
}

// RegisterSchedule 只插入缺失计划；重启不覆盖暂停、配置或游标。旧定义版本被拒绝。
func (s *Store) RegisterSchedule(ctx context.Context, in Schedule) (out *Schedule, err error) {
	if err = in.validate(); err != nil {
		return
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		row, e := tx.GetOne(`SELECT * FROM task_schedule WHERE namespace=? AND name=? FOR UPDATE`, s.cfg.Namespace, in.Name)
		if e != nil {
			return e
		}
		if !row.IsEmpty() {
			out, e = s.schedule(row)
			if e == nil && in.DefinitionVersion < out.DefinitionVersion {
				return ErrConflict
			}
			return e
		}
		in.ID, e = newID()
		if e != nil {
			return e
		}
		in.Version = 1
		if in.NextRunAt.IsZero() {
			in.NextRunAt = in.Anchor
			if in.Kind == "cron" {
				in.NextRunAt, e = in.Next(in.Anchor.Add(-time.Nanosecond))
				if e != nil {
					return e
				}
			}
		}
		spec, e := encoded(in)
		if e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO task_schedule(id,namespace,name,enabled,version,definition_version,spec,next_run_at) VALUES(?,?,?,?,?,?,CAST(? AS JSONB),?)`, in.ID, s.cfg.Namespace, in.Name, in.Enabled, in.Version, in.DefinitionVersion, spec, s.stamp(in.NextRunAt))
		out = &in
		return e
	})
	return
}

func (s *Store) insertRun(tx gdb.TX, def Definition, scheduleID string, version int64, point *time.Time, trigger, key, replay string) (*Run, error) {
	if err := def.validate(); err != nil {
		return nil, err
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	data, err := encoded(def)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var scheduled any
	if point != nil {
		scheduled = s.stamp(*point)
	}
	payload := jsonQueryString(def.Payload)
	if payload == nil {
		payload = "{}"
	}
	row, err := tx.GetOne(`INSERT INTO task(id,type,payload,status,priority,max_attempts,timeout,available_at,created_at,updated_at,namespace,handler_version,schedule_id,schedule_version,scheduled_at,trigger_type,request_key,replay_of,definition) VALUES(?,?,CAST(? AS JSONB),'pending',?,?,?,?,?,?,?,?,?,?,?,?,?,?,CAST(? AS JSONB)) RETURNING *`, id, def.Type, payload, def.Priority, def.Retry.MaxAttempts, int(def.Timeout/time.Second), s.stamp(now), s.stamp(now), s.stamp(now), s.cfg.Namespace, def.HandlerVersion, nullable(scheduleID), version, scheduled, trigger, nullable(key), nullable(replay), data)
	if err != nil {
		return nil, err
	}
	out, err := s.run(row)
	if err != nil {
		return nil, err
	}
	if def.OrderingKey != "" {
		if out.OrderSeq, err = s.assignOrder(tx, out.ID, def, replay); err != nil {
			return nil, err
		}
	}
	if out.DispatchGeneration, err = s.enqueue(tx, out.ID, def.Class, now); err != nil {
		return nil, err
	}
	return out, nil
}

// Tick 原子派发到期计划并推进游标。每计划最多一个活跃运行；catch_up 每轮最多派发一个，保留积压。
func (s *Store) Tick(ctx context.Context, budget int, fn Audit) (created []*Run, err error) {
	if budget < 1 || budget > 100 {
		return nil, fmt.Errorf("task: tick budget must be 1..100")
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		now := s.now()
		rows, e := tx.GetAll(`SELECT * FROM task_schedule WHERE namespace=? AND enabled AND next_run_at<=? AND NOT EXISTS(SELECT 1 FROM task t WHERE t.schedule_id=task_schedule.id AND t.status IN ('pending','running','failed','needs_review','waiting_children')) ORDER BY next_run_at,id LIMIT ? FOR UPDATE SKIP LOCKED`, s.cfg.Namespace, s.stamp(now), budget)
		if e != nil {
			return e
		}
		for _, row := range rows {
			plan, e := s.schedule(row)
			if e != nil {
				return e
			}
			active, e := tx.GetOne(`SELECT id FROM task WHERE schedule_id=? AND status IN ('pending','running','failed','needs_review','waiting_children') LIMIT 1`, plan.ID)
			if e != nil {
				return e
			}
			if !active.IsEmpty() {
				continue
			}
			def, e := s.prepare(tx, plan.Definition, false)
			if errors.Is(e, ErrTypeUnavailable) {
				// 缺类型定义：拒绝派发且不推进游标，待管理端补齐后继续。
				continue
			}
			if e != nil {
				return e
			}
			points, next, missed, e := plan.Due(now, 1)
			if e != nil {
				return e
			}
			var last any
			for _, p := range points {
				previous, lookupErr := tx.GetOne(`SELECT id FROM task WHERE schedule_id=? AND scheduled_at=? AND trigger_type='scheduled'`, plan.ID, s.stamp(p))
				if lookupErr != nil {
					return lookupErr
				}
				if !previous.IsEmpty() {
					last = s.stamp(p)
					continue
				}
				r, e := s.insertRun(tx, def, plan.ID, plan.Version, &p, "scheduled", "", "")
				if e != nil {
					return e
				}
				created = append(created, r)
				last = s.stamp(p)
			}
			var from, until any
			if missed > 0 {
				from = s.stamp(plan.NextRunAt)
				until = s.stamp(next)
			}
			_, e = tx.Exec(`UPDATE task_schedule SET misfire_from=COALESCE(?,misfire_from),misfire_until=COALESCE(?,misfire_until),next_run_at=?,last_scheduled_at=COALESCE(?,last_scheduled_at),missed_count=missed_count+? WHERE id=?`, from, until, s.stamp(next), last, missed, plan.ID)
			if e != nil {
				return e
			}
		}
		return audit(ctx, tx, fn)
	})
	if err != nil {
		created = nil
	}
	return
}

// Submit 创建独立手动运行。requestKey 必填；相同键只能用于相同 Definition。
func (s *Store) Submit(ctx context.Context, def Definition, requestKey string, fn Audit) (out *Run, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		out, err = s.submit(ctx, tx, def, requestKey, fn)
		return err
	})
	if err != nil {
		out = nil
	}
	return
}

func (s *Store) submit(ctx context.Context, tx gdb.TX, def Definition, requestKey string, fn Audit) (*Run, error) {
	if requestKey == "" || len(requestKey) > 256 {
		return nil, fmt.Errorf("task: request key required")
	}
	row, e := tx.GetOne(`SELECT * FROM task WHERE namespace=? AND request_key=?`, s.cfg.Namespace, requestKey)
	if e != nil {
		return nil, e
	}
	if !row.IsEmpty() {
		out, e := s.run(row)
		if e != nil {
			return nil, e
		}
		if !s.sameRequest(out.Definition, def) || out.Trigger != "manual" || out.ScheduleID != "" {
			return nil, ErrConflict
		}
		return out, nil
	}
	def, e = s.prepare(tx, def, true)
	if e != nil {
		return nil, e
	}
	out, e := s.insertRun(tx, def, "", 0, nil, "manual", requestKey, "")
	if e != nil {
		return nil, e
	}
	return out, audit(ctx, tx, fn)
}

// checkSchema prevents a configured instant/wall mismatch from silently shifting timestamps.
func (s *Store) checkSchema(ctx context.Context, tx gdb.TX) error {
	row, err := tx.GetOne(`SELECT format_type(a.atttypid,a.atttypmod) AS type FROM pg_attribute a WHERE a.attrelid='task'::regclass AND a.attname='available_at'`)
	if err != nil {
		return err
	}
	expected := "timestamp with time zone"
	if s.cfg.TimeMode == TimeWall {
		expected = "timestamp without time zone"
	}
	if row["type"].String() != expected {
		return fmt.Errorf("task: storage time mode mismatch")
	}
	return nil
}

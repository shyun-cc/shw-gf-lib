package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
)

func (s *Store) command(tx gdb.TX, key, target, action string, input any) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("task: request key required")
	}
	b, e := json.Marshal(input)
	if e != nil {
		return false, e
	}
	hash := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(hash[:])
	row, e := tx.GetOne(`SELECT * FROM task_command WHERE namespace=? AND request_key=?`, s.cfg.Namespace, key)
	if e != nil {
		return false, e
	}
	if !row.IsEmpty() {
		if row["target_id"].String() != target || row["action"].String() != action || row["fingerprint"].String() != fingerprint {
			return false, ErrConflict
		}
		return true, nil
	}
	_, e = tx.Exec(`INSERT INTO task_command(namespace,request_key,target_id,action,fingerprint,created_at) VALUES(?,?,?,?,?,?)`, s.cfg.Namespace, key, target, action, fingerprint, s.stamp(s.now()))
	return false, e
}

// SetEnabled 暂停只阻止后续派发及领取；运行中的尝试保持有效。重复 requestKey 不重复审计。
func (s *Store) SetEnabled(ctx context.Context, id string, expected int64, enabled bool, key string, fn Audit) error {
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, id, "enabled", []any{expected, enabled})
		if e != nil || done {
			return e
		}
		res, e := tx.Exec(`UPDATE task_schedule SET enabled=?,version=version+1 WHERE id=? AND namespace=? AND version=?`, enabled, id, s.cfg.Namespace, expected)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		if e = s.bumpRevision(tx); e != nil {
			return e
		}
		return audit(ctx, tx, fn)
	})
}

// Configure 显式替换未来计划定义，按新 Anchor 计算 next；不修改已生成运行快照。
func (s *Store) Configure(ctx context.Context, id string, expected int64, in Schedule, key string, fn Audit) error {
	if err := in.validate(); err != nil {
		return err
	}
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, id, "configure", []any{expected, in})
		if e != nil || done {
			return e
		}
		row, e := tx.GetOne(`SELECT * FROM task_schedule WHERE id=? AND namespace=? FOR UPDATE`, id, s.cfg.Namespace)
		if e != nil {
			return e
		}
		if row.IsEmpty() || row["version"].Int64() != expected || row["definition_version"].Int64() > in.DefinitionVersion || row["name"].String() != in.Name {
			return ErrConflict
		}
		next := in.Anchor
		if in.Kind == "cron" {
			next, e = in.Next(in.Anchor.Add(-time.Nanosecond))
			if e != nil {
				return e
			}
		}
		in.ID = id
		in.Version = expected + 1
		spec, e := encoded(in)
		if e != nil {
			return e
		}
		_, e = tx.Exec(`UPDATE task_schedule SET spec=CAST(? AS JSONB),definition_version=?,version=version+1,enabled=?,next_run_at=? WHERE id=?`, spec, in.DefinitionVersion, in.Enabled, s.stamp(next), id)
		if e != nil {
			return e
		}
		if e = s.bumpRevision(tx); e != nil {
			return e
		}
		return audit(ctx, tx, fn)
	})
}

// Trigger 立即运行不修改周期游标；已存在活跃轮次返回 ErrConflict。
func (s *Store) Trigger(ctx context.Context, id, key string, fn Audit) (out *Run, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, id, "trigger", id)
		if e != nil {
			return e
		}
		if done {
			row, e := tx.GetOne(`SELECT * FROM task WHERE namespace=? AND request_key=?`, s.cfg.Namespace, key)
			if e != nil {
				return e
			}
			out, e = s.run(row)
			return e
		}
		row, e := tx.GetOne(`SELECT * FROM task_schedule WHERE id=? AND namespace=? FOR UPDATE`, id, s.cfg.Namespace)
		if e != nil {
			return e
		}
		plan, e := s.schedule(row)
		if e != nil {
			return e
		}
		if !plan.Enabled {
			return ErrConflict
		}
		active, e := tx.GetOne(`SELECT id FROM task WHERE schedule_id=? AND status IN ('pending','running','failed','needs_review','waiting_children') LIMIT 1`, id)
		if e != nil {
			return e
		}
		if !active.IsEmpty() {
			return ErrConflict
		}
		def, e := s.prepare(tx, plan.Definition, false)
		if e != nil {
			return e
		}
		out, e = s.insertRun(tx, def, plan.ID, plan.Version, nil, "manual", key, "")
		if e != nil {
			return e
		}
		return audit(ctx, tx, fn)
	})
	if err != nil {
		out = nil
	}
	return
}

// Retry 仅允许声明幂等重试的 abnormal/旧 dead 终态运行；创建新运行并保留 replay_of，不修改历史尝试。
func (s *Store) Retry(ctx context.Context, id string, expected int64, key string, fn Audit) (*Run, error) {
	return s.resolve(ctx, id, expected, key, "retry", fn)
}

// Resolve 保留源兼容，不再提供人工确认/成功/取消/重放流程。
// Deprecated: 始终返回 ErrConflict；异常记录不可被人工改写为成功。
func (s *Store) Resolve(ctx context.Context, id string, expected int64, key, decision string, fn Audit) (*Run, error) {
	return nil, ErrConflict
}
func (s *Store) resolve(ctx context.Context, id string, expected int64, key, decision string, fn Audit) (out *Run, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, id, "resolve", []any{expected, decision})
		if e != nil {
			return e
		}
		if done {
			cmd, e := tx.GetOne(`SELECT result_id FROM task_command WHERE namespace=? AND request_key=?`, s.cfg.Namespace, key)
			if e != nil {
				return e
			}
			rid := cmd["result_id"].String()
			if rid == "" {
				rid = id
			}
			row, e := tx.GetOne(`SELECT * FROM task WHERE id=? AND namespace=?`, rid, s.cfg.Namespace)
			if e != nil {
				return e
			}
			out, e = s.run(row)
			return e
		}
		row, e := tx.GetOne(`SELECT * FROM task WHERE id=? AND namespace=? FOR UPDATE`, id, s.cfg.Namespace)
		if e != nil {
			return e
		}
		r, e := s.run(row)
		if e != nil {
			return e
		}
		if r.Version != expected {
			return ErrConflict
		}
		if row["status"].String() == StatusNeedsReview || decision != "retry" || r.Definition.Recovery != RecoveryRetry || (r.Status != StatusDead && r.Status != StatusAbnormal) {
			return ErrConflict
		}
		if r.ScheduleID != "" {
			active, e := tx.GetOne(`SELECT id FROM task WHERE schedule_id=? AND status IN ('pending','running','failed','needs_review','waiting_children') LIMIT 1`, r.ScheduleID)
			if e != nil {
				return e
			}
			if !active.IsEmpty() {
				return ErrConflict
			}
		}
		active, e := tx.GetOne(`SELECT id FROM task WHERE namespace=? AND replay_of=? AND status IN ('pending','running','failed','needs_review','waiting_children') LIMIT 1`, s.cfg.Namespace, id)
		if e != nil {
			return e
		}
		if !active.IsEmpty() {
			return ErrConflict
		}
		// 管理 CAS 消耗本版本，防止不同请求键用同一版本重复创建重试。
		if _, e = tx.Exec(`UPDATE task SET version=version+1,updated_at=? WHERE id=?`, s.stamp(s.now()), id); e != nil {
			return e
		}
		out, e = s.insertRun(tx, r.Definition, r.ScheduleID, r.ScheduleVersion, nil, "replay", key, id)
		if e != nil {
			return e
		}
		// 子任务重试继承逻辑键，汇总仅计最新运行，原异常记录完整保留。
		if r.ParentID != nil && *r.ParentID != "" {
			if _, e = tx.Exec(`UPDATE task SET parent_id=?,child_key=(SELECT child_key FROM task WHERE id=?) WHERE id=?`, *r.ParentID, id, out.ID); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(`UPDATE task_command SET result_id=? WHERE namespace=? AND request_key=?`, out.ID, s.cfg.Namespace, key); e != nil {
			return e
		}

		return audit(ctx, tx, fn)
	})
	if err != nil {
		out = nil
	}
	return
}

func (s *Store) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	row, err := s.cfg.DB.GetOne(ctx, `SELECT * FROM task_schedule WHERE id=? AND namespace=?`, id, s.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	return s.schedule(row)
}
func (s *Store) GetRun(ctx context.Context, id string) (*Run, error) {
	row, err := s.cfg.DB.GetOne(ctx, `SELECT * FROM task WHERE id=? AND namespace=?`, id, s.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	return s.run(row)
}
func (s *Store) Schedules(ctx context.Context, p Page) ([]*Schedule, error) {
	n, o := p.bounds()
	rows, e := s.cfg.DB.GetAll(ctx, `SELECT * FROM task_schedule WHERE namespace=? ORDER BY name,id LIMIT ? OFFSET ?`, s.cfg.Namespace, n, o)
	if e != nil {
		return nil, e
	}
	out := make([]*Schedule, 0, len(rows))
	for _, row := range rows {
		v, e := s.schedule(row)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) Runs(ctx context.Context, scheduleID, status string, p Page) ([]*Run, error) {
	n, o := p.bounds()
	q := `SELECT * FROM task WHERE namespace=?`
	args := []any{s.cfg.Namespace}
	if scheduleID != "" {
		q += ` AND schedule_id=?`
		args = append(args, scheduleID)
	}
	if status != "" {
		if publicStatus(status) == StatusAbnormal {
			q += ` AND status IN ('abnormal','needs_review')`
		} else {
			q += ` AND status=?`
			args = append(args, status)
		}
	}
	q += ` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`
	args = append(args, n, o)
	rows, e := s.cfg.DB.GetAll(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	out := make([]*Run, 0, len(rows))
	for _, row := range rows {
		v, e := s.run(row)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

// AttemptView 不包含 lease token。Result 为 Completion 的 JSON，Checkpoint 为项目定义的有界 JSON。
type AttemptView struct {
	ID          string
	TaskID      string
	AttemptNo   int
	Status      string
	InstanceID  string
	TraceID     string
	StartedAt   time.Time
	FinishedAt  *time.Time
	HeartbeatAt time.Time
	ExpiresAt   time.Time
	Deadline    *time.Time
	Checkpoint  []byte
	Result      []byte
	Error       string
}

func (s *Store) Attempts(ctx context.Context, taskID string, p Page) ([]AttemptView, error) {
	n, o := p.bounds()
	rows, e := s.cfg.DB.GetAll(ctx, `SELECT a.* FROM task_attempt a JOIN task t ON t.id=a.task_id WHERE t.namespace=? AND t.id=? ORDER BY a.attempt_no DESC LIMIT ? OFFSET ?`, s.cfg.Namespace, taskID, n, o)
	if e != nil {
		return nil, e
	}
	out := make([]AttemptView, 0, len(rows))
	for _, r := range rows {
		v := AttemptView{ID: r["id"].String(), TaskID: r["task_id"].String(), AttemptNo: r["attempt_no"].Int(), Status: r["status"].String(), InstanceID: r["worker_id"].String(), TraceID: r["trace_id"].String(), StartedAt: s.readTime(r["started_at"]), HeartbeatAt: s.readTime(r["heartbeat_at"]), ExpiresAt: s.readTime(r["lease_expires_at"]), Checkpoint: []byte(r["checkpoint"].String()), Result: []byte(r["result"].String()), Error: r["error"].String()}
		if t := s.readTime(r["finished_at"]); !t.IsZero() {
			v.FinishedAt = &t
		}
		if t := s.readTime(r["deadline_at"]); !t.IsZero() {
			v.Deadline = &t
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Store) Instances(ctx context.Context, p Page) ([]Instance, error) {
	n, o := p.bounds()
	rows, e := s.cfg.DB.GetAll(ctx, `SELECT * FROM task_instance WHERE namespace=? ORDER BY started_at DESC,id LIMIT ? OFFSET ?`, s.cfg.Namespace, n, o)
	if e != nil {
		return nil, e
	}
	out := make([]Instance, 0, len(rows))
	for _, row := range rows {
		var v Instance
		if e = json.Unmarshal([]byte(row["info"].String()), &v); e != nil {
			return nil, e
		}
		v.StartedAt = s.readTime(row["started_at"])
		v.HeartbeatAt = s.readTime(row["heartbeat_at"])
		v.AppliedRevision = row["applied_revision"].Int64()
		if t := s.readTime(row["stopped_at"]); !t.IsZero() {
			v.StoppedAt = &t
		}
		out = append(out, v)
	}
	return out, nil
}

// Counts 提供状态计数供项目导出监控指标，不注册全局 metrics。
func (s *Store) Counts(ctx context.Context) (map[string]int64, error) {
	rows, e := s.cfg.DB.GetAll(ctx, `SELECT status,COUNT(*) AS n FROM task WHERE namespace=? GROUP BY status`, s.cfg.Namespace)
	if e != nil {
		return nil, e
	}
	out := map[string]int64{}
	for _, row := range rows {
		out[publicStatus(row["status"].String())] += row["n"].Int64()
	}
	return out, nil
}

// Cleanup 只删无请求幂等键、无关联引用的旧终态；保留活动/异常、命令及手动请求记录。
func (s *Store) Cleanup(ctx context.Context, before time.Time, limit int) (removed int64, err error) {
	if limit < 1 || limit > 1000 {
		return 0, fmt.Errorf("task: invalid cleanup limit")
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		res, e := tx.Exec(`DELETE FROM task WHERE id IN (SELECT t.id FROM task t WHERE t.namespace=? AND t.status IN ('succeeded','dead','cancelled') AND t.finished_at<? AND t.request_key IS NULL AND NOT EXISTS(SELECT 1 FROM task c WHERE c.parent_id=t.id OR c.replay_of=t.id) AND NOT EXISTS(SELECT 1 FROM task_concurrency c WHERE c.active_task_id=t.id) AND NOT EXISTS(SELECT 1 FROM task_command c WHERE c.target_id=t.id OR c.result_id=t.id) ORDER BY t.finished_at LIMIT ?)`, s.cfg.Namespace, s.stamp(before), limit)
		if e != nil {
			return e
		}
		removed, e = res.RowsAffected()
		return e
	})
	return
}

// GetInstance 查询单个进程，Online 判断由调用方用 HeartbeatAt 与展示阈值完成。
func (s *Store) GetInstance(ctx context.Context, id string) (*Instance, error) {
	row, e := s.cfg.DB.GetOne(ctx, `SELECT * FROM task_instance WHERE namespace=? AND id=?`, s.cfg.Namespace, id)
	if e != nil {
		return nil, e
	}
	if row.IsEmpty() {
		return nil, ErrNotFound
	}
	var v Instance
	if e = json.Unmarshal([]byte(row["info"].String()), &v); e != nil {
		return nil, e
	}
	v.StartedAt = s.readTime(row["started_at"])
	v.HeartbeatAt = s.readTime(row["heartbeat_at"])
	v.AppliedRevision = row["applied_revision"].Int64()
	if at := s.readTime(row["stopped_at"]); !at.IsZero() {
		v.StoppedAt = &at
	}
	return &v, nil
}

// GetAttempt 查询单次尝试详情，不返回租约 token。
func (s *Store) GetAttempt(ctx context.Context, id string) (*AttemptView, error) {
	row, e := s.cfg.DB.GetOne(ctx, `SELECT a.* FROM task_attempt a JOIN task t ON t.id=a.task_id WHERE t.namespace=? AND a.id=?`, s.cfg.Namespace, id)
	if e != nil {
		return nil, e
	}
	if row.IsEmpty() {
		return nil, ErrNotFound
	}
	v := &AttemptView{ID: row["id"].String(), TaskID: row["task_id"].String(), AttemptNo: row["attempt_no"].Int(), Status: row["status"].String(), InstanceID: row["worker_id"].String(), TraceID: row["trace_id"].String(), StartedAt: s.readTime(row["started_at"]), HeartbeatAt: s.readTime(row["heartbeat_at"]), ExpiresAt: s.readTime(row["lease_expires_at"]), Checkpoint: []byte(row["checkpoint"].String()), Result: []byte(row["result"].String()), Error: row["error"].String()}
	if at := s.readTime(row["finished_at"]); !at.IsZero() {
		v.FinishedAt = &at
	}
	if at := s.readTime(row["deadline_at"]); !at.IsZero() {
		v.Deadline = &at
	}
	return v, nil
}

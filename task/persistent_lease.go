package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
)

// RegisterInstance 登记进程身份。ID 为空时生成新 UUID，已有 ID 不允许复用。
func (s *Store) RegisterInstance(ctx context.Context, in Instance) (out *Instance, err error) {
	if in.Capacity < 1 {
		return nil, fmt.Errorf("task: positive instance capacity required")
	}
	if in.ID == "" {
		in.ID, err = newID()
		if err != nil {
			return
		}
	}
	in.StartedAt = s.now()
	in.HeartbeatAt = in.StartedAt
	in.StoppedAt = nil
	data, err := encoded(in)
	if err != nil {
		return nil, err
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		_, e := tx.Exec(`INSERT INTO task_instance(id,namespace,info,started_at,heartbeat_at) VALUES(?,?,CAST(? AS JSONB),?,?)`, in.ID, s.cfg.Namespace, data, s.stamp(in.StartedAt), s.stamp(in.HeartbeatAt))
		return e
	})
	if err == nil {
		out = &in
	}
	return
}
func (s *Store) HeartbeatInstance(ctx context.Context, id string) error {
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		res, err := tx.Exec(`UPDATE task_instance SET heartbeat_at=? WHERE id=? AND namespace=? AND stopped_at IS NULL`, s.stamp(s.now()), id, s.cfg.Namespace)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}

// HeartbeatApplied 刷新心跳并上报实例已应用的配置修订号。
func (s *Store) HeartbeatApplied(ctx context.Context, id string, revision int64) error {
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		res, err := tx.Exec(`UPDATE task_instance SET heartbeat_at=?,applied_revision=? WHERE id=? AND namespace=? AND stopped_at IS NULL`, s.stamp(s.now()), revision, id, s.cfg.Namespace)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (s *Store) StopInstance(ctx context.Context, id string) error {
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		_, err := tx.Exec(`UPDATE task_instance SET stopped_at=? WHERE id=? AND namespace=? AND stopped_at IS NULL`, s.stamp(s.now()), id, s.cfg.Namespace)
		return err
	})
}

// Claim pull 与 ByID 共用原子路径。id 为空为 pull；无许可能力/无可领取任务返回 nil。
func (s *Store) Claim(ctx context.Context, instanceID, id, traceID string) (out *Lease, err error) {
	if len(traceID) > 256 {
		return nil, fmt.Errorf("task: trace ID too long")
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		out, err = s.claimTx(tx, instanceID, id, traceID)
		return err
	})
	if err != nil {
		out = nil
	}
	return
}

// claimTx 在已持有 namespace 锁的事务内执行领取；无可领取返回 (nil,nil)。
func (s *Store) claimTx(tx gdb.TX, instanceID, id, traceID string) (*Lease, error) {
	var out *Lease
	err := func() error {
		now := s.now()
		inst, e := tx.GetOne(`SELECT * FROM task_instance WHERE id=? AND namespace=? AND stopped_at IS NULL`, instanceID, s.cfg.Namespace)
		if e != nil {
			return e
		}
		if inst.IsEmpty() {
			return ErrConflict
		}
		if s.readTime(inst["heartbeat_at"]).Add(30 * time.Second).Before(now) {
			return ErrConflict
		}
		var instance Instance
		if e = json.Unmarshal([]byte(inst["info"].String()), &instance); e != nil {
			return e
		}
		if len(instance.Capabilities) == 0 {
			return nil
		}
		count, e := tx.GetOne(`SELECT COUNT(*) AS n FROM task_attempt a JOIN task t ON t.active_attempt_id=a.id WHERE t.namespace=? AND a.worker_id=? AND a.status='running'`, s.cfg.Namespace, instanceID)
		if e != nil {
			return e
		}
		if count["n"].Int() >= instance.Capacity {
			return nil
		}
		caps, e := encoded(instance.Capabilities)
		if e != nil {
			return e
		}
		q := `SELECT t.* FROM task t LEFT JOIN task_schedule p ON p.id=t.schedule_id WHERE t.namespace=? AND t.status='pending' AND t.available_at<=? AND (CAST(? AS JSONB) ->> t.type)=t.handler_version AND (t.schedule_id IS NULL OR p.enabled) AND NOT EXISTS (SELECT 1 FROM task_concurrency c WHERE c.namespace=t.namespace AND c.key=(t.definition->>'ConcurrencyKey') AND c.active_task_id<>t.id) AND` + orderGate
		args := []any{s.cfg.Namespace, s.stamp(now), caps}
		if s.cfg.TypeControl {
			q += ` AND EXISTS (SELECT 1 FROM task_type ty WHERE ty.namespace=t.namespace AND ty.type=t.type AND ty.consume_enabled` +
				` AND (SELECT COUNT(*) FROM task r WHERE r.namespace=t.namespace AND r.type=t.type AND r.status='running')<ty.global_concurrency` +
				` AND (ty.rate_limit=0 OR (SELECT COUNT(*) FROM task_attempt ra JOIN task rt ON rt.id=ra.task_id WHERE rt.namespace=t.namespace AND rt.type=t.type AND ra.started_at>CAST(? AS ` + s.columnType() + `)-ty.rate_window_ms*INTERVAL '1 millisecond')<ty.rate_limit))`
			args = append(args, s.stamp(now))
		}
		if id != "" {
			q += ` AND t.id=?`
			args = append(args, id)
		}
		q += ` ORDER BY t.priority DESC,t.available_at,t.id LIMIT 1 FOR UPDATE OF t SKIP LOCKED`
		row, e := tx.GetOne(q, args...)
		if e != nil {
			return e
		}
		if row.IsEmpty() {
			return nil
		}
		run, e := s.run(row)
		if e != nil {
			return e
		}
		if key := run.Definition.ConcurrencyKey; key != "" {
			_, e = tx.Exec(`INSERT INTO task_concurrency(namespace,key,active_task_id) VALUES(?,?,?) ON CONFLICT(namespace,key) DO NOTHING`, s.cfg.Namespace, key, run.ID)
			if e != nil {
				return e
			}
			owner, e := tx.GetOne(`SELECT active_task_id FROM task_concurrency WHERE namespace=? AND key=?`, s.cfg.Namespace, key)
			if e != nil {
				return e
			}
			if owner["active_task_id"].String() != run.ID {
				return nil
			}
		}
		attempt, e := newID()
		if e != nil {
			return e
		}
		token, e := newID()
		if e != nil {
			return e
		}
		expiry := now.Add(run.Definition.Lease)
		var deadline *time.Time
		var deadlineValue any
		if run.Definition.Timeout > 0 {
			v := now.Add(run.Definition.Timeout)
			deadline = &v
			deadlineValue = s.stamp(v)
		}
		_, e = tx.Exec(`INSERT INTO task_attempt(id,task_id,attempt_no,status,started_at,worker_id,lease_token,lease_expires_at,heartbeat_at,deadline_at,trace_id) VALUES(?,?,?,'running',?,?,?,?,?,?,?)`, attempt, run.ID, run.Attempts+1, s.stamp(now), instanceID, token, s.stamp(expiry), s.stamp(now), deadlineValue, traceID)
		if e != nil {
			return e
		}
		row, e = tx.GetOne(`UPDATE task SET status='running',attempts=attempts+1,active_attempt_id=?,started_at=?,finished_at=NULL,updated_at=?,version=version+1 WHERE id=? RETURNING *`, attempt, s.stamp(now), s.stamp(now), run.ID)
		if e != nil {
			return e
		}
		run, e = s.run(row)
		if e != nil {
			return e
		}

		out = &Lease{Run: run, AttemptID: attempt, Token: token, ExpiresAt: expiry, Deadline: deadline, TraceID: traceID}
		return nil
	}()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) owned(tx gdb.TX, l *Lease) (*Run, gdb.Record, error) {
	if l == nil || l.Run == nil {
		return nil, nil, ErrLeaseLost
	}
	row, e := tx.GetOne(`SELECT * FROM task WHERE id=? AND namespace=? FOR UPDATE`, l.Run.ID, s.cfg.Namespace)
	if e != nil {
		return nil, nil, e
	}
	if row.IsEmpty() || row["status"].String() != StatusRunning || row["active_attempt_id"].String() != l.AttemptID {
		return nil, nil, ErrLeaseLost
	}
	a, e := tx.GetOne(`SELECT * FROM task_attempt WHERE id=? AND task_id=? AND lease_token=? AND status='running' FOR UPDATE`, l.AttemptID, l.Run.ID, l.Token)
	if e != nil {
		return nil, nil, e
	}
	if a.IsEmpty() || !s.readTime(a["lease_expires_at"]).After(s.now()) {
		return nil, nil, ErrLeaseLost
	}
	r, e := s.run(row)
	return r, a, e
}

// Renew 仅续租，不延长业务 deadline。失效租约不可复活。
// 运行已被 CancelRun 请求取消时，续租仍然提交并返回 ErrCancelRequested，
// 执行器应协作取消 Handler 并写回结果（非成功结果落为 cancelled）。
func (s *Store) Renew(ctx context.Context, l *Lease) error {
	cancelled := false
	err := s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, a, e := s.owned(tx, l)
		if e != nil {
			return e
		}
		if d := s.readTime(a["deadline_at"]); !d.IsZero() && !d.After(s.now()) {
			return ErrLeaseLost
		}
		cancelled = r.CancelRequested
		_, e = tx.Exec(`UPDATE task_attempt SET heartbeat_at=?,lease_expires_at=? WHERE id=?`, s.stamp(s.now()), s.stamp(s.now().Add(r.Definition.Lease)), l.AttemptID)
		return e
	})
	if err == nil && cancelled {
		return ErrCancelRequested
	}
	return err
}

// Progress 与业务检查点写入可通过 Audit 同事务完成。checkpoint 最大 16KiB。
func (s *Store) Progress(ctx context.Context, l *Lease, progress int, checkpoint []byte, fn Audit) error {
	if progress < 0 || progress > 100 || len(checkpoint) > 16384 || len(checkpoint) > 0 && !json.Valid(checkpoint) {
		return fmt.Errorf("task: invalid progress/checkpoint")
	}
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		_, a, e := s.owned(tx, l)
		if e != nil {
			return e
		}
		if d := s.readTime(a["deadline_at"]); !d.IsZero() && !d.After(s.now()) {
			return ErrLeaseLost
		}
		if _, e = tx.Exec(`UPDATE task_attempt SET checkpoint=CAST(? AS JSONB) WHERE id=?`, jsonQueryString(checkpoint), l.AttemptID); e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE task SET progress=?,updated_at=? WHERE id=?`, progress, s.stamp(s.now()), l.Run.ID); e != nil {
			return e
		}
		return audit(ctx, tx, fn)
	})
}
func validCompletion(c Completion) error {
	switch c.Outcome {
	case OutcomeSuccess, OutcomePartial, OutcomeSkipped, OutcomeFailed, OutcomeRetryableFailure, OutcomePermanentFailure, OutcomeUnknown, OutcomeInterrupted:
	default:
		return fmt.Errorf("task: invalid outcome")
	}
	if len(c.ErrorCode) > 128 || len(c.ErrorSummary) > 2048 || len(c.BusinessRef) > 1024 || len(c.Summary) > 16384 || len(c.Result) > 65536 || len(c.Stats) > 64 {
		return fmt.Errorf("task: completion too large")
	}
	for k := range c.Stats {
		if k == "" || len(k) > 64 {
			return fmt.Errorf("task: invalid completion stats key")
		}
	}
	if len(c.Summary) > 0 && !json.Valid(c.Summary) || len(c.Result) > 0 && !json.Valid(c.Result) {
		return fmt.Errorf("task: invalid completion JSON")
	}
	return nil
}
func (s *Store) finish(tx gdb.TX, r *Run, attemptID string, c Completion, attemptStatus string) error {
	now := s.now()
	available := r.AvailableAt
	cont, e := s.continuations(tx, r)
	if e != nil {
		return e
	}
	status, retryAt, retry := settle(r.Definition, r.Attempts-cont, r.MaxAttempts, c.Outcome, now, randomUnit())
	if retry {
		available = retryAt
	}
	if r.CancelRequested && status != StatusSucceeded {
		status = StatusCancelled
		if c.ErrorCode == "" {
			c.ErrorCode = "cancelled"
		}
	}
	if status == StatusAbnormal && c.ErrorCode == "" {
		c.ErrorCode = r.Definition.Recovery
	}
	detail, e := encoded(c)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE task_attempt SET status=?,finished_at=?,error=?,result=CAST(? AS JSONB) WHERE id=?`, attemptStatus, s.stamp(now), c.ErrorSummary, detail, attemptID); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE task SET status=?,outcome=?,error_code=?,summary=CAST(? AS JSONB),result=CAST(? AS JSONB),last_error=?,available_at=?,finished_at=?,active_attempt_id=NULL,updated_at=?,version=version+1 WHERE id=?`, status, c.Outcome, c.ErrorCode, detail, jsonQueryString(c.Result), c.ErrorSummary, s.stamp(available), s.stamp(now), s.stamp(now), r.ID); e != nil {
		return e
	}
	if status == StatusSucceeded || status == StatusDead || status == StatusCancelled || status == StatusAbnormal {
		if _, e = tx.Exec(`DELETE FROM task_concurrency WHERE namespace=? AND active_task_id=?`, s.cfg.Namespace, r.ID); e != nil {
			return e
		}
	}
	return s.afterSettle(tx, r, status)
}

// Complete 租约、deadline、活跃尝试均有效时才写入终态。
func (s *Store) Complete(ctx context.Context, l *Lease, c Completion, fn Audit) error {
	if err := validCompletion(c); err != nil {
		return err
	}
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, a, e := s.owned(tx, l)
		if e != nil {
			return e
		}
		if d := s.readTime(a["deadline_at"]); !d.IsZero() && !d.After(s.now()) {
			return ErrLeaseLost
		}
		state := AttemptSucceeded
		if c.Outcome != OutcomeSuccess && c.Outcome != OutcomePartial && c.Outcome != OutcomeSkipped {
			state = AttemptFailed
		}
		if c.Outcome == OutcomeInterrupted {
			state = "interrupted"
		}
		if e = s.finish(tx, r, l.AttemptID, c, state); e != nil {
			return e
		}
		return audit(ctx, tx, fn)
	})
}

// Recover 原子转换历史 needs_review 并释放占用、回收过期租约/deadline、将到期 failed 转 pending。
// 每类至多处理 limit 条；recovered 包含历史转换与过期回收，不重复计算退避。
func (s *Store) Recover(ctx context.Context, limit int) (recovered, retried int, err error) {
	if limit < 1 || limit > 1000 {
		return 0, 0, fmt.Errorf("task: invalid recovery limit")
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		now := s.now()
		// 数据兼容由引擎事务处理；不改历史 outcome/error/attempt，不需结构迁移。
		legacy, e := tx.GetAll(`SELECT * FROM task WHERE namespace=? AND status='needs_review' ORDER BY id LIMIT ? FOR UPDATE`, s.cfg.Namespace, limit)
		if e != nil {
			return e
		}
		for _, row := range legacy {
			r, e := s.run(row)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(`UPDATE task SET status='abnormal',active_attempt_id=NULL,finished_at=COALESCE(finished_at,?),updated_at=?,version=version+1 WHERE id=?`, s.stamp(now), s.stamp(now), r.ID); e != nil {
				return e
			}
			if _, e = tx.Exec(`DELETE FROM task_concurrency WHERE namespace=? AND active_task_id=?`, s.cfg.Namespace, r.ID); e != nil {
				return e
			}
			if e = s.afterSettle(tx, r, StatusAbnormal); e != nil {
				return e
			}
			recovered++
		}
		rows, e := tx.GetAll(`SELECT t.* FROM task t JOIN task_attempt a ON a.id=t.active_attempt_id WHERE t.namespace=? AND t.status='running' AND a.status='running' AND (a.lease_expires_at<=? OR a.deadline_at<=?) ORDER BY a.lease_expires_at LIMIT ? FOR UPDATE OF t,a`, s.cfg.Namespace, s.stamp(now), s.stamp(now), limit)
		if e != nil {
			return e
		}
		for _, row := range rows {
			r, e := s.run(row)
			if e != nil {
				return e
			}
			outcome := "unknown"
			if r.Definition.Recovery == RecoveryRetry {
				outcome = "failed"
			}
			if e = s.finish(tx, r, r.ActiveAttemptID, Completion{Outcome: outcome, ErrorCode: "lease_expired", ErrorSummary: "execution lease or deadline expired"}, AttemptTimeout); e != nil {
				return e
			}
			recovered++
		}
		rows, e = tx.GetAll(`SELECT t.* FROM task t LEFT JOIN task_schedule p ON p.id=t.schedule_id WHERE t.namespace=? AND t.status='failed' AND t.available_at<=? AND (t.schedule_id IS NULL OR p.enabled) ORDER BY t.available_at LIMIT ? FOR UPDATE OF t`, s.cfg.Namespace, s.stamp(now), limit)
		if e != nil {
			return e
		}
		for _, row := range rows {
			r, e := s.run(row)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(`UPDATE task SET status='pending',version=version+1,updated_at=? WHERE id=?`, s.stamp(now), r.ID); e != nil {
				return e
			}
			// 到期重试在同事务内创建下一代通知；旧代次消息到达时按 stale 处理，不触发提前执行。
			if _, e = s.enqueue(tx, r.ID, r.Definition.Class, now); e != nil {
				return e
			}
			retried++
		}
		return nil
	})
	if err != nil {
		recovered = 0
		retried = 0
	}
	return
}

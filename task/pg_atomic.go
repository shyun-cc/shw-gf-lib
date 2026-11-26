package task

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
)

// ClaimExecution 为旧 Worker 的 pull/ByID 提供同一事务领取+建 attempt 路径。
func (r *PgRepository) ClaimExecution(ctx context.Context, id, worker string) (t *Task, a *TaskAttempt, err error) {
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		q := `SELECT * FROM task WHERE namespace='' AND status='pending' AND available_at<=NOW()`
		args := []any{}
		if id != "" {
			q += ` AND id=?`
			args = append(args, id)
		}
		q += ` ORDER BY priority DESC,available_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`
		row, e := tx.GetOne(q, args...)
		if e != nil {
			return e
		}
		if row.IsEmpty() {
			return nil
		}
		t = hydrateTask(row)
		aid, e := newID()
		if e != nil {
			return e
		}
		now := time.Now()
		t.Attempts++
		t.Status = StatusRunning
		if _, e = tx.Exec(`UPDATE task SET status='running',attempts=?,started_at=?,updated_at=? WHERE id=?`, t.Attempts, now, now, t.ID); e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO task_attempt(id,task_id,attempt_no,status,started_at,worker_id) VALUES(?,?,?,'running',?,?)`, aid, t.ID, t.Attempts, now, worker); e != nil {
			return e
		}
		a = &TaskAttempt{ID: aid, TaskID: t.ID, AttemptNo: t.Attempts, Status: AttemptRunning, StartedAt: now, WorkerID: worker}
		return nil
	})
	if err != nil {
		t = nil
		a = nil
	}
	return
}

// ScanRecovery 旧 PG 运行原子超时回收与重试；新 namespace 任务只由 Store.Recover 管理。
func (r *PgRepository) ScanRecovery(ctx context.Context, retry *ExponentialBackoffRetryPolicy, publish func(context.Context, *Task)) (stuck, retried, dead int, err error) {
	if retry == nil {
		retry = NewDefaultRetryPolicy()
	}
	var wake []*Task
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		rows, e := tx.GetAll(`SELECT t.* FROM task t WHERE t.namespace='' AND t.status='running' AND ((t.timeout>0 AND EXISTS(SELECT 1 FROM task_attempt a WHERE a.task_id=t.id AND a.attempt_no=t.attempts AND a.status='running' AND a.started_at+(t.timeout||' seconds')::interval<=NOW())) OR NOT EXISTS(SELECT 1 FROM task_attempt a WHERE a.task_id=t.id AND a.attempt_no=t.attempts AND a.status='running')) ORDER BY t.id LIMIT 100 FOR UPDATE OF t SKIP LOCKED`)
		if e != nil {
			return e
		}
		for _, row := range rows {
			t := hydrateTask(row)
			a, e := tx.GetOne(`SELECT * FROM task_attempt WHERE task_id=? AND attempt_no=? AND status='running' FOR UPDATE`, t.ID, t.Attempts)
			if e != nil {
				return e
			}
			if a.IsEmpty() {
				if _, e = tx.Exec(`UPDATE task SET status='needs_review',last_error='orphan legacy execution',updated_at=NOW() WHERE id=?`, t.ID); e != nil {
					return e
				}
				stuck++
				continue
			}
			if _, e = tx.Exec(`UPDATE task_attempt SET status='timeout',finished_at=NOW() WHERE id=?`, a["id"].String()); e != nil {
				return e
			}
			status := StatusDead
			next := time.Now()
			if retry.ShouldRetry(t) {
				status = StatusPending
				next = retry.NextAvailableAt(t, next)
				retried++
			} else {
				dead++
			}
			if _, e = tx.Exec(`UPDATE task SET status=?,available_at=?,last_error='execution timeout',updated_at=NOW() WHERE id=?`, status, next, t.ID); e != nil {
				return e
			}
			if status == StatusPending {
				// publish is deferred until available_at becomes due
			}
			stuck++
		}
		rows, e = tx.GetAll(`SELECT * FROM task WHERE namespace='' AND status='failed' AND available_at<=NOW() ORDER BY id LIMIT 100 FOR UPDATE SKIP LOCKED`)
		if e != nil {
			return e
		}
		for _, row := range rows {
			t := hydrateTask(row)
			status := StatusDead
			next := time.Now()
			if retry.ShouldRetry(t) {
				status = StatusPending
				next = retry.NextAvailableAt(t, next)
				retried++
			} else {
				dead++
			}
			if _, e = tx.Exec(`UPDATE task SET status=?,available_at=?,updated_at=NOW() WHERE id=?`, status, next, t.ID); e != nil {
				return e
			}
			if status == StatusPending {
				// publish is deferred until available_at becomes due
			}
		}
		due, e := tx.GetAll(`SELECT * FROM task WHERE namespace='' AND status='pending' AND available_at<=NOW() ORDER BY available_at LIMIT 100`)
		if e != nil {
			return e
		}
		for _, row := range due {
			wake = append(wake, hydrateTask(row))
		}
		return nil
	})
	if err != nil {
		return 0, 0, 0, err
	}
	if publish != nil {
		for _, t := range wake {
			publish(ctx, t)
		}
	}
	return
}

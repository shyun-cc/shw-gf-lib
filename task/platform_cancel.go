package task

import (
	"context"
	"errors"

	"github.com/gogf/gf/v2/database/gdb"
)

// ErrCancelRequested 运行已被请求取消；续租仍有效，执行器应协作取消 Handler。
var ErrCancelRequested = errors.New("task: cancel_requested")

// CancelRun 取消运行（CAS + 幂等 + 同事务审计）：
// 未开始/待重试立即置为 cancelled 并释放互斥组；运行中只请求协作取消，
// 执行器下次续租感知后取消 Handler，非成功结果落为 cancelled。已成功的外部副作用不承诺回滚。
func (s *Store) CancelRun(ctx context.Context, id string, expected int64, key string, fn Audit) (out *Run, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, id, "cancel", expected)
		if e != nil {
			return e
		}
		if !done {
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
			if e = s.cancelTx(tx, r); e != nil {
				return e
			}
			if e = audit(ctx, tx, fn); e != nil {
				return e
			}
		}
		row, e := tx.GetOne(`SELECT * FROM task WHERE id=? AND namespace=?`, id, s.cfg.Namespace)
		if e != nil {
			return e
		}
		out, e = s.run(row)
		return e
	})
	if err != nil {
		out = nil
	}
	return
}

func (s *Store) cancelTx(tx gdb.TX, r *Run) error {
	now := s.stamp(s.now())
	switch r.Status {
	case StatusPending, StatusFailed, StatusWaitingChildren:
		if _, e := tx.Exec(`UPDATE task SET status='cancelled',error_code=CASE WHEN error_code='' THEN 'cancelled' ELSE error_code END,finished_at=?,updated_at=?,version=version+1 WHERE id=?`, now, now, r.ID); e != nil {
			return e
		}
		if _, e := tx.Exec(`DELETE FROM task_concurrency WHERE namespace=? AND active_task_id=?`, s.cfg.Namespace, r.ID); e != nil {
			return e
		}
		return s.afterSettle(tx, r, StatusCancelled)
	case StatusRunning:
		_, e := tx.Exec(`UPDATE task SET cancel_requested=TRUE,updated_at=?,version=version+1 WHERE id=?`, now, r.ID)
		return e
	}
	return ErrConflict
}

// afterSettle 在运行进入新状态的同一事务内执行平台级后续处理：子任务终态触发父任务汇总检查。
func (s *Store) afterSettle(tx gdb.TX, r *Run, status string) error {
	if r.ParentID == nil || *r.ParentID == "" {
		return nil
	}
	switch status {
	case StatusSucceeded, StatusDead, StatusCancelled, StatusAbnormal:
		return s.finalizeParent(tx, *r.ParentID)
	}
	return nil
}

package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gogf/gf/v2/database/gdb"
)

// StatusWaitingChildren 父任务已拆分完毕、等待子任务处置；不占执行名额。
const StatusWaitingChildren = "waiting_children"

// 拆分状态。
const (
	SplitSplitting = "splitting"
	SplitWaiting   = "waiting_children"
	SplitCompleted = "completed"
)

// ChildSpec 是一个逻辑子任务。Key 是父任务内稳定的业务键（订单号、设备号、快照范围等），
// 重启/重试拆分时以 unique(parent_id,child_key) 保证不重复生成；禁止用易漂移的 offset 充当。
type ChildSpec struct {
	Key      string
	Type     string // 子任务类型；空表示与父任务同类型
	Payload  []byte
	Priority int
	// OrderingKey 可选：子任务之间按业务键严格有序（scope 为子任务类型）。
	OrderingKey string
}

// SplitBatch 是 Splitter 一次返回的批次：子任务、下一游标（稳定检查点 JSON）与是否已拆完。
type SplitBatch struct {
	Children []ChildSpec
	Cursor   []byte
	Done     bool
}

// Splitter 从 cursor（首次为 nil）继续拆分，最多返回 limit 个子任务。
// 数据范围是否冻结、后续新增对象是否加入由业务在 cursor 语义中决定。
type Splitter func(ctx context.Context, parent *Run, cursor []byte, limit int) (SplitBatch, error)

// SplitView 是父任务拆分与汇总进度。Total 仅在拆分完成后给出；未完成时展示 Discovered（已发现 N 项）。
type SplitView struct {
	State          string
	SplitCompleted bool
	Discovered     int64
	Total          *int64
	Batches        int64
	Continuations  int
	SummaryVersion int64
	Pending        int64 // 逻辑子任务（取每个 child_key 最新运行）各状态计数
	Running        int64
	Succeeded      int64
	Partial        int64
	Failed         int64 // dead
	Cancelled      int64
	Abnormal       int64
	NeedsReview    int64 // Deprecated: 始终为零，使用 Abnormal
}

func validBatch(b SplitBatch) error {
	if len(b.Children) > 1000 || len(b.Cursor) > 16384 || len(b.Cursor) > 0 && !json.Valid(b.Cursor) {
		return fmt.Errorf("task: invalid split batch")
	}
	seen := map[string]bool{}
	for _, c := range b.Children {
		if c.Key == "" || len(c.Key) > 256 || len(c.OrderingKey) > 256 || seen[c.Key] || len(c.Payload) > 65536 || len(c.Payload) > 0 && !json.Valid(c.Payload) {
			return fmt.Errorf("task: invalid split child")
		}
		seen[c.Key] = true
	}
	return nil
}

// splitRow 读取（必要时创建）父任务拆分状态，加行锁。
func (s *Store) splitRow(tx gdb.TX, r *Run) (gdb.Record, error) {
	row, err := tx.GetOne(`SELECT * FROM task_split WHERE task_id=? FOR UPDATE`, r.ID)
	if err != nil || !row.IsEmpty() {
		return row, err
	}
	if _, err = tx.Exec(`INSERT INTO task_split(task_id,namespace,state,updated_at) VALUES(?,?,'splitting',?)`, r.ID, s.cfg.Namespace, s.stamp(s.now())); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE task SET is_split=TRUE WHERE id=?`, r.ID); err != nil {
		return nil, err
	}
	return tx.GetOne(`SELECT * FROM task_split WHERE task_id=? FOR UPDATE`, r.ID)
}

// SplitCursor 返回父任务当前拆分游标（持有有效租约时）。
func (s *Store) SplitCursor(ctx context.Context, l *Lease) (cursor []byte, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, _, e := s.owned(tx, l)
		if e != nil {
			return e
		}
		row, e := s.splitRow(tx, r)
		if e != nil {
			return e
		}
		if v := row["cursor"]; v != nil && !v.IsNil() {
			cursor = []byte(v.String())
		}
		return nil
	})
	return
}

// AppendSplit 在一个事务内写入一批子任务（含各自 Outbox 通知）并推进拆分游标；
// 已存在的 child_key 跳过。batch.Done 时标记 split_completed。返回新插入的子任务数。
func (s *Store) AppendSplit(ctx context.Context, l *Lease, batch SplitBatch, fn Audit) (inserted int, err error) {
	if err = validBatch(batch); err != nil {
		return 0, err
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, _, e := s.owned(tx, l)
		if e != nil {
			return e
		}
		if _, e = s.splitRow(tx, r); e != nil {
			return e
		}
		for _, c := range batch.Children {
			exists, e := tx.GetOne(`SELECT 1 AS x FROM task WHERE parent_id=? AND child_key=? LIMIT 1`, r.ID, c.Key)
			if e != nil {
				return e
			}
			if !exists.IsEmpty() {
				continue
			}
			typ := c.Type
			if typ == "" {
				typ = r.Type
			}
			def, e := s.prepare(tx, Definition{Type: typ, Payload: c.Payload, Priority: c.Priority, OrderingKey: c.OrderingKey}, false)
			if e != nil {
				return e
			}
			child, e := s.insertRun(tx, def, "", 0, nil, "child", "", "")
			if e != nil {
				return e
			}
			if _, e = tx.Exec(`UPDATE task SET parent_id=?,child_key=? WHERE id=?`, r.ID, c.Key, child.ID); e != nil {
				return e
			}
			inserted++
		}
		if _, e = tx.Exec(`UPDATE task_split SET cursor=CAST(? AS JSONB),generated=generated+?,batches=batches+1,split_completed=split_completed OR ?,updated_at=? WHERE task_id=?`, jsonQueryString(batch.Cursor), inserted, batch.Done, s.stamp(s.now()), r.ID); e != nil {
			return e
		}
		return audit(ctx, tx, fn)
	})
	if err != nil {
		inserted = 0
	}
	return
}

// FinishSplit 结束父任务本次拆分尝试：已拆完则父任务进入 waiting_children 并释放执行名额
// （全部子任务已处置时立即汇总）；未拆完则保留游标，父任务转 pending 并立即续派（不计入失败次数）。
func (s *Store) FinishSplit(ctx context.Context, l *Lease) error {
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		r, _, e := s.owned(tx, l)
		if e != nil {
			return e
		}
		split, e := s.splitRow(tx, r)
		if e != nil {
			return e
		}
		now := s.stamp(s.now())
		done := split["split_completed"].Bool()
		detail, _ := encoded(Completion{Outcome: OutcomeSuccess, ErrorCode: map[bool]string{true: "split_completed", false: "split_continue"}[done]})
		if _, e = tx.Exec(`UPDATE task_attempt SET status='succeeded',finished_at=?,result=CAST(? AS JSONB) WHERE id=?`, now, detail, l.AttemptID); e != nil {
			return e
		}
		if !done {
			if _, e = tx.Exec(`UPDATE task_split SET continuations=continuations+1,updated_at=? WHERE task_id=?`, now, r.ID); e != nil {
				return e
			}
			if _, e = tx.Exec(`UPDATE task SET status='pending',available_at=?,active_attempt_id=NULL,updated_at=?,version=version+1 WHERE id=?`, now, now, r.ID); e != nil {
				return e
			}
			_, e = s.enqueue(tx, r.ID, r.Definition.Class, s.now())
			return e
		}
		if _, e = tx.Exec(`UPDATE task_split SET state='waiting_children',updated_at=? WHERE task_id=?`, now, r.ID); e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE task SET status='waiting_children',active_attempt_id=NULL,updated_at=?,version=version+1 WHERE id=?`, now, r.ID); e != nil {
			return e
		}
		return s.finalizeParent(tx, r.ID)
	})
}

// continuations 返回拆分续派次数（续派不计入失败重试次数）。
func (s *Store) continuations(tx gdb.TX, r *Run) (int, error) {
	if !r.IsSplit {
		return 0, nil
	}
	row, err := tx.GetOne(`SELECT continuations FROM task_split WHERE task_id=?`, r.ID)
	if err != nil {
		return 0, err
	}
	return row["continuations"].Int(), nil
}

// latestChildren 以每个 child_key 的最新运行代表逻辑子任务（重试新运行替代原运行，汇总只计一次）。
const latestChildren = `SELECT DISTINCT ON (child_key) status,outcome FROM task WHERE parent_id=? AND child_key IS NOT NULL ORDER BY child_key,created_at DESC,id DESC`

// finalizeParent 在子任务结算的同一事务内检查父任务：拆分已完成且全部逻辑子任务已处置时汇总最终结果。
// 已汇总的父任务在子任务重放结算后重新汇总（summary_version 递增）。
func (s *Store) finalizeParent(tx gdb.TX, parentID string) error {
	split, err := tx.GetOne(`SELECT * FROM task_split WHERE task_id=? FOR UPDATE`, parentID)
	if err != nil || split.IsEmpty() || !split["split_completed"].Bool() {
		return err
	}
	prow, err := tx.GetOne(`SELECT * FROM task WHERE id=? FOR UPDATE`, parentID)
	if err != nil || prow.IsEmpty() {
		return err
	}
	parent, err := s.run(prow)
	if err != nil {
		return err
	}
	if parent.Status != StatusWaitingChildren && !(split["state"].String() == SplitCompleted && (parent.Status == StatusSucceeded || parent.Status == StatusDead || parent.Status == StatusAbnormal)) {
		return nil
	}
	rows, err := tx.GetAll(latestChildren, parentID)
	if err != nil {
		return err
	}
	stats := map[string]int64{"total": int64(len(rows))}
	for _, row := range rows {
		switch st, oc := row["status"].String(), row["outcome"].String(); {
		case st == StatusSucceeded && oc == OutcomePartial:
			stats["partial"]++
		case st == StatusSucceeded:
			stats["succeeded"]++
		case st == StatusAbnormal:
			stats["abnormal"]++
		case st == StatusDead:
			stats["failed"]++
		case st == StatusCancelled:
			stats["cancelled"]++
		default:
			return nil // 仍有活跃的逻辑子任务
		}
	}
	status, outcome := StatusSucceeded, OutcomeSuccess
	if bad := stats["failed"] + stats["cancelled"] + stats["abnormal"]; bad > 0 && bad == stats["total"] {
		status, outcome = StatusAbnormal, OutcomePermanentFailure
	} else if bad+stats["partial"] > 0 {
		outcome = OutcomePartial
	}
	detail, err := encoded(Completion{Outcome: outcome, Stats: stats})
	if err != nil {
		return err
	}
	now := s.stamp(s.now())
	if _, err = tx.Exec(`UPDATE task SET status=?,outcome=?,summary=CAST(? AS JSONB),progress=100,finished_at=?,updated_at=?,version=version+1 WHERE id=?`, status, outcome, detail, now, now, parentID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE task_split SET state='completed',summary_version=summary_version+1,updated_at=? WHERE task_id=?`, now, parentID); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM task_concurrency WHERE namespace=? AND active_task_id=?`, s.cfg.Namespace, parentID); err != nil {
		return err
	}
	return s.afterSettle(tx, parent, status)
}

// SplitProgress 返回父任务拆分进度与逻辑子任务计数。
func (s *Store) SplitProgress(ctx context.Context, parentID string) (*SplitView, error) {
	row, err := s.cfg.DB.GetOne(ctx, `SELECT sp.* FROM task_split sp JOIN task t ON t.id=sp.task_id WHERE sp.task_id=? AND t.namespace=?`, parentID, s.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	if row.IsEmpty() {
		return nil, ErrNotFound
	}
	v := &SplitView{State: row["state"].String(), SplitCompleted: row["split_completed"].Bool(), Discovered: row["generated"].Int64(), Batches: row["batches"].Int64(), Continuations: row["continuations"].Int(), SummaryVersion: row["summary_version"].Int64()}
	if v.SplitCompleted {
		n := v.Discovered
		v.Total = &n
	}
	rows, err := s.cfg.DB.GetAll(ctx, latestChildren, parentID)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		switch st := r["status"].String(); st {
		case StatusSucceeded:
			if r["outcome"].String() == OutcomePartial {
				v.Partial++
			} else {
				v.Succeeded++
			}
		case StatusDead:
			v.Failed++
		case StatusCancelled:
			v.Cancelled++
		case StatusAbnormal, StatusNeedsReview:
			v.Abnormal++
		case StatusRunning:
			v.Running++
		default:
			v.Pending++
		}
	}
	return v, nil
}

// Children 分页列出父任务的子任务运行（含重放）。
func (s *Store) Children(ctx context.Context, parentID string, p Page) ([]*Run, error) {
	n, o := p.bounds()
	rows, err := s.cfg.DB.GetAll(ctx, `SELECT * FROM task WHERE namespace=? AND parent_id=? ORDER BY child_key,created_at,id LIMIT ? OFFSET ?`, s.cfg.Namespace, parentID, n, o)
	if err != nil {
		return nil, err
	}
	out := make([]*Run, 0, len(rows))
	for _, row := range rows {
		v, err := s.run(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// CancelTree 取消父任务；includeChildren 为 true 时同时取消未开始/待重试的子任务，
// 运行中的子任务请求协作取消。已成功的外部副作用不承诺回滚。
func (s *Store) CancelTree(ctx context.Context, id string, expected int64, includeChildren bool, key string, fn Audit) (out *Run, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, id, "cancel_tree", []any{expected, includeChildren})
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
			if includeChildren {
				rows, e := tx.GetAll(`SELECT * FROM task WHERE parent_id=? AND namespace=? AND status IN ('pending','failed','running','waiting_children') FOR UPDATE`, id, s.cfg.Namespace)
				if e != nil {
					return e
				}
				for _, row := range rows {
					c, e := s.run(row)
					if e != nil {
						return e
					}
					if e = s.cancelTx(tx, c); e != nil {
						return e
					}
				}
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

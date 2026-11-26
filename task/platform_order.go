package task

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/database/gdb"
)

// orderGate 是领取/投递共用的顺序门控（别名 t 为 task）：同 scope+key 存在更早序号且未终结的运行时阻塞。
// 门控判断“是否存在更早的阻塞项”而非序号连续性，序号空洞（清理、回滚）不会造成永久等待。
// 待重试、旧 needs_review、等待子任务会阻塞；异常与旧 dead 终态自动放行。
const orderGate = ` (t.ordering_key IS NULL OR NOT EXISTS (SELECT 1 FROM task b WHERE b.namespace=t.namespace AND b.order_scope=t.order_scope AND b.ordering_key=t.ordering_key AND b.order_seq<t.order_seq AND b.status NOT IN ('succeeded','cancelled','abnormal','dead')))`

// assignOrder 在创建运行的同一事务内分配序号；回滚时计数器一并回滚，不产生悬空号。
// 显式重试是新的运行，分配新序号，不能插回已推进的顺序。
func (s *Store) assignOrder(tx gdb.TX, id string, def Definition, replay string) (int64, error) {
	scope := def.OrderScope
	if scope == "" {
		scope = def.Type
	}
	row, err := tx.GetOne(`INSERT INTO task_order(namespace,scope,key,next_seq) VALUES(?,?,?,1) ON CONFLICT(namespace,scope,key) DO UPDATE SET next_seq=task_order.next_seq+1 RETURNING next_seq`, s.cfg.Namespace, scope, def.OrderingKey)
	if err != nil {
		return 0, err
	}
	seq := row["next_seq"].Int64()
	_, err = tx.Exec(`UPDATE task SET order_scope=?,ordering_key=?,order_seq=? WHERE id=?`, scope, def.OrderingKey, seq, id)
	return seq, err
}

// OrderView 是一个顺序键的当前状态。Head 为最早的阻塞运行（nil 表示无阻塞）。
type OrderView struct {
	Scope   string
	Key     string
	NextSeq int64 // 最近分配的序号
	Head    *Run
	Waiting int64 // Head 之后仍待执行的运行数
}

// OrderState 查询顺序键状态，供管理端展示“顺序阻塞”。
func (s *Store) OrderState(ctx context.Context, scope, key string) (*OrderView, error) {
	v := &OrderView{Scope: scope, Key: key}
	row, err := s.cfg.DB.GetOne(ctx, `SELECT next_seq FROM task_order WHERE namespace=? AND scope=? AND key=?`, s.cfg.Namespace, scope, key)
	if err != nil {
		return nil, err
	}
	if row.IsEmpty() {
		return nil, ErrNotFound
	}
	v.NextSeq = row["next_seq"].Int64()
	head, err := s.cfg.DB.GetOne(ctx, `SELECT * FROM task WHERE namespace=? AND order_scope=? AND ordering_key=? AND status NOT IN ('succeeded','cancelled','abnormal','dead') ORDER BY order_seq,created_at,id LIMIT 1`, s.cfg.Namespace, scope, key)
	if err != nil {
		return nil, err
	}
	if head.IsEmpty() {
		return v, nil
	}
	if v.Head, err = s.run(head); err != nil {
		return nil, err
	}
	n, err := s.cfg.DB.GetOne(ctx, `SELECT COUNT(*) AS n FROM task WHERE namespace=? AND order_scope=? AND ordering_key=? AND order_seq>? AND status NOT IN ('succeeded','cancelled','abnormal','dead')`, s.cfg.Namespace, scope, key, v.Head.OrderSeq)
	if err != nil {
		return nil, err
	}
	v.Waiting = n["n"].Int64()
	return v, nil
}

// SkipOrder 是显式授权的人工跳过：把阻塞中的有序运行置为 cancelled（outcome=skipped），
// 推进同键后续运行。仅允许未在运行的 pending/failed/dead 运行；CAS + 幂等 + 同事务审计，
// 审计中应记录操作人、原因与对后续运行的影响。
func (s *Store) SkipOrder(ctx context.Context, id string, expected int64, key string, fn Audit) (out *Run, err error) {
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		done, e := s.command(tx, key, id, "order_skip", expected)
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
			if r.Version != expected || row["ordering_key"].IsNil() || row["ordering_key"].String() == "" {
				return ErrConflict
			}
			switch r.Status {
			case StatusPending, StatusFailed, StatusDead:
			default:
				return fmt.Errorf("%w: only idle ordered runs can be skipped", ErrConflict)
			}
			now := s.stamp(s.now())
			if _, e = tx.Exec(`UPDATE task SET status='cancelled',outcome='skipped',error_code='order_skipped',finished_at=?,updated_at=?,version=version+1 WHERE id=?`, now, now, id); e != nil {
				return e
			}
			if _, e = tx.Exec(`DELETE FROM task_concurrency WHERE namespace=? AND active_task_id=?`, s.cfg.Namespace, id); e != nil {
				return e
			}
			if e = s.afterSettle(tx, r, StatusCancelled); e != nil {
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

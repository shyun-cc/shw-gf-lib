package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/google/uuid"
)

// DispatchProtocol 是执行通知消息的传输协议版本（与处理器版本、namespace 分别定义）。
const DispatchProtocol = 1

// ErrUnroutable 传输层确认消息无队列可路由；投递器保留待重试，不记已投递。
var ErrUnroutable = errors.New("task: dispatch_unroutable")

// ErrPoisonMessage 消息协议错误或指向本作用域不存在的任务，应进入死信隔离而不是重放。
var ErrPoisonMessage = errors.New("task: poison_message")

// DispatchMessage 是 RabbitMQ 执行通知的消息体，只携带路由信息，不复制业务载荷或秘密。
//
//	{"v":1,"ns":"project-worker","task_id":"018f...","gen":3}
type DispatchMessage struct {
	Version    int    `json:"v"`
	Namespace  string `json:"ns"`
	TaskID     string `json:"task_id"`
	Generation int64  `json:"gen"`
}

// Encode 序列化消息体（application/json）。
func (m DispatchMessage) Encode() []byte {
	b, _ := json.Marshal(m)
	return b
}

// MessageID 为 publisher confirm/mandatory 退回匹配用的唯一消息标识。
func (m DispatchMessage) MessageID() string {
	return m.TaskID + ":" + strconv.FormatInt(m.Generation, 10)
}

// DecodeDispatch 解析并校验消息体；协议不符返回 ErrPoisonMessage。
func DecodeDispatch(b []byte) (DispatchMessage, error) {
	var m DispatchMessage
	if len(b) > 4096 || json.Unmarshal(b, &m) != nil || m.Version != DispatchProtocol || m.Namespace == "" || m.Generation < 1 {
		return m, ErrPoisonMessage
	}
	if _, err := uuid.Parse(m.TaskID); err != nil {
		return m, ErrPoisonMessage
	}
	return m, nil
}

// OutboxItem 是投递器租约领取到的一条待发布通知。
type OutboxItem struct {
	ID       string
	Class    string
	Message  DispatchMessage
	Attempts int
}

// enqueue 递增任务投递代次，作废旧代次待发通知并写入新一代通知；必须与任务状态变更同事务。
func (s *Store) enqueue(tx gdb.TX, taskID, class string, at time.Time) (int64, error) {
	if !s.cfg.Outbox {
		return 0, nil
	}
	if class == "" {
		class = DefaultClass
	}
	row, err := tx.GetOne(`UPDATE task SET dispatch_generation=dispatch_generation+1 WHERE id=? RETURNING dispatch_generation`, taskID)
	if err != nil {
		return 0, err
	}
	gen := row["dispatch_generation"].Int64()
	if _, err = tx.Exec(`UPDATE task_outbox SET status='superseded',lease_owner=NULL,lease_until=NULL WHERE task_id=? AND status='pending' AND generation<?`, taskID, gen); err != nil {
		return 0, err
	}
	id, err := newID()
	if err != nil {
		return 0, err
	}
	now := s.stamp(s.now())
	_, err = tx.Exec(`INSERT INTO task_outbox(id,namespace,task_id,generation,class,status,available_at,created_at) VALUES(?,?,?,?,?,'pending',?,?)`, id, s.cfg.Namespace, taskID, gen, class, s.stamp(at), now)
	return gen, err
}

// dispatchable 是“任务当前可执行”的公共 SQL 条件（别名 t 为 task，p 为 task_schedule）。
func (s *Store) dispatchable() string {
	q := ` t.status='pending' AND t.available_at<=? AND (t.schedule_id IS NULL OR p.enabled) AND` + orderGate
	if s.cfg.TypeControl {
		q += ` AND EXISTS (SELECT 1 FROM task_type ty WHERE ty.namespace=t.namespace AND ty.type=t.type AND ty.consume_enabled)`
	}
	return q
}

// ClaimOutbox 以租约领取到期且任务当前可执行的通知，最多 limit 条。
// 暂停、未到期的任务不发布（无热循环）；TypeControl 下每类型最多发布其剩余全局名额条数。
// 同时把任务已不再待执行或代次已过期的旧通知标记为 superseded。
func (s *Store) ClaimOutbox(ctx context.Context, owner string, limit int, lease time.Duration) (out []OutboxItem, err error) {
	if owner == "" || limit < 1 || limit > 1000 || lease < time.Second {
		return nil, fmt.Errorf("task: invalid outbox claim")
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		now := s.now()
		if _, e := tx.Exec(`UPDATE task_outbox SET status='superseded',lease_owner=NULL,lease_until=NULL WHERE id IN (SELECT o.id FROM task_outbox o JOIN task t ON t.id=o.task_id WHERE o.namespace=? AND o.status='pending' AND (t.status<>'pending' OR t.dispatch_generation<>o.generation) LIMIT 1000)`, s.cfg.Namespace); e != nil {
			return e
		}
		rows, e := tx.GetAll(`SELECT o.id,o.class,o.task_id,o.generation,o.publish_attempts,t.type FROM task_outbox o JOIN task t ON t.id=o.task_id LEFT JOIN task_schedule p ON p.id=t.schedule_id WHERE o.namespace=? AND o.status='pending' AND o.available_at<=? AND (o.lease_until IS NULL OR o.lease_until<=?) AND t.dispatch_generation=o.generation AND`+s.dispatchable()+` ORDER BY o.available_at,o.id LIMIT ? FOR UPDATE OF o SKIP LOCKED`, s.cfg.Namespace, s.stamp(now), s.stamp(now), s.stamp(now), limit)
		if e != nil {
			return e
		}
		spare := map[string]int{}
		for _, row := range rows {
			typ := row["type"].String()
			if s.cfg.TypeControl {
				left, ok := spare[typ]
				if !ok {
					c, e := tx.GetOne(`SELECT ty.global_concurrency-(SELECT COUNT(*) FROM task r WHERE r.namespace=ty.namespace AND r.type=ty.type AND r.status='running') AS n FROM task_type ty WHERE ty.namespace=? AND ty.type=?`, s.cfg.Namespace, typ)
					if e != nil {
						return e
					}
					left = c["n"].Int()
				}
				if left <= 0 {
					spare[typ] = 0
					continue
				}
				spare[typ] = left - 1
			}
			if _, e = tx.Exec(`UPDATE task_outbox SET lease_owner=?,lease_until=?,publish_attempts=publish_attempts+1 WHERE id=?`, owner, s.stamp(now.Add(lease)), row["id"].String()); e != nil {
				return e
			}
			out = append(out, OutboxItem{ID: row["id"].String(), Class: row["class"].String(), Attempts: row["publish_attempts"].Int() + 1, Message: DispatchMessage{Version: DispatchProtocol, Namespace: s.cfg.Namespace, TaskID: row["task_id"].String(), Generation: row["generation"].Int64()}})
		}
		return nil
	})
	if err != nil {
		out = nil
	}
	return
}

// MarkOutboxSent 在 broker 确认可路由后记已投递；租约已被他人接管或通知已作废时返回 false。
// 确认后写回前崩溃会导致重复投递，由领取与代次校验去重。
func (s *Store) MarkOutboxSent(ctx context.Context, item OutboxItem, owner string) (bool, error) {
	var ok bool
	err := s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		res, e := tx.Exec(`UPDATE task_outbox SET status='sent',sent_at=?,lease_owner=NULL,lease_until=NULL,last_error='' WHERE id=? AND lease_owner=? AND status='pending'`, s.stamp(s.now()), item.ID, owner)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		ok = n == 1
		return e
	})
	return ok && err == nil, err
}

// MarkOutboxFailed 释放租约并在 retryAt 后重试发布（断连、不可路由、结果未知）。
func (s *Store) MarkOutboxFailed(ctx context.Context, item OutboxItem, owner, reason string, retryAt time.Time) error {
	if len(reason) > 1024 {
		reason = reason[:1024]
	}
	return s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		_, e := tx.Exec(`UPDATE task_outbox SET lease_owner=NULL,lease_until=NULL,available_at=?,last_error=? WHERE id=? AND lease_owner=? AND status='pending'`, s.stamp(retryAt), reason, item.ID, owner)
		return e
	})
}

// RepairDispatch 为“已可执行却没有有效投递责任”的任务补投新一代通知：
// 当前代次无通知，或已发送超过 staleAfter 仍未被领取（消息丢失）。补投不越过暂停/available_at。
func (s *Store) RepairDispatch(ctx context.Context, limit int, staleAfter time.Duration) (repaired int, err error) {
	if !s.cfg.Outbox || limit < 1 || limit > 1000 || staleAfter < time.Second {
		return 0, fmt.Errorf("task: invalid dispatch repair")
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		now := s.now()
		rows, e := tx.GetAll(`SELECT t.id,t.definition FROM task t LEFT JOIN task_schedule p ON p.id=t.schedule_id WHERE t.namespace=? AND`+s.dispatchable()+` AND NOT EXISTS (SELECT 1 FROM task_outbox o WHERE o.task_id=t.id AND o.generation=t.dispatch_generation AND (o.status='pending' OR (o.status='sent' AND o.sent_at>?))) ORDER BY t.available_at,t.id LIMIT ? FOR UPDATE OF t SKIP LOCKED`, s.cfg.Namespace, s.stamp(now), s.stamp(now.Add(-staleAfter)), limit)
		if e != nil {
			return e
		}
		for _, row := range rows {
			var def Definition
			if e = json.Unmarshal([]byte(row["definition"].String()), &def); e != nil {
				return e
			}
			if _, e = s.enqueue(tx, row["id"].String(), def.Class, now); e != nil {
				return e
			}
			repaired++
		}
		return nil
	})
	if err != nil {
		repaired = 0
	}
	return
}

// Delivery 处置结论。
const (
	// DeliveryClaimed 已原子领取并建立租约；恢复责任已落库（租约失效必由 Recover 处置），可 ACK 后执行。
	DeliveryClaimed = "claimed"
	// DeliveryStale 旧代次、已运行或已完成：责任在别处，可直接 ACK。
	DeliveryStale = "stale"
	// DeliveryDeferred 暂不可领取（暂停/名额/限速/互斥/未到期），已持久化延期通知，可 ACK。
	DeliveryDeferred = "deferred"
	// DeliveryPoison 协议错误或作用域内不存在的任务，应 Nack 进死信并告警。
	DeliveryPoison = "poison"
)

// Accept 处理一条执行通知：校验协议与作用域、代次去重、原子领取，领取不到时持久化延期通知。
// 返回 error 表示数据库不可用或责任尚未落库，调用方不得 ACK。
func (s *Store) Accept(ctx context.Context, instanceID string, body []byte, traceID string) (lease *Lease, decision string, err error) {
	msg, err := DecodeDispatch(body)
	if err != nil || msg.Namespace != s.cfg.Namespace {
		return nil, DeliveryPoison, nil
	}
	if len(traceID) > 256 {
		return nil, "", fmt.Errorf("task: trace ID too long")
	}
	err = s.transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		row, e := tx.GetOne(`SELECT * FROM task WHERE id=? AND namespace=? FOR UPDATE`, msg.TaskID, s.cfg.Namespace)
		if e != nil {
			return e
		}
		if row.IsEmpty() {
			decision = DeliveryPoison
			return nil
		}
		r, e := s.run(row)
		if e != nil {
			return e
		}
		if msg.Generation > r.DispatchGeneration {
			decision = DeliveryPoison
			return nil
		}
		if msg.Generation < r.DispatchGeneration || r.Status != StatusPending {
			decision = DeliveryStale
			return nil
		}
		if lease, e = s.claimTx(tx, instanceID, r.ID, traceID); e != nil {
			return e
		}
		if lease != nil {
			decision = DeliveryClaimed
			return nil
		}
		decision = DeliveryDeferred
		return s.deferDispatch(tx, r, msg.Generation)
	})
	if err != nil {
		return nil, "", err
	}
	return lease, decision, nil
}

// deferDispatch 把当前代次通知重新置为待发布，按延期次数退避（1s 起倍增，封顶 30s），不早于任务 available_at。
func (s *Store) deferDispatch(tx gdb.TX, r *Run, gen int64) error {
	now := s.now()
	row, err := tx.GetOne(`SELECT deferrals FROM task_outbox WHERE task_id=? AND generation=?`, r.ID, gen)
	if err != nil {
		return err
	}
	n := row["deferrals"].Int()
	if n > 5 {
		n = 5
	}
	at := now.Add(time.Duration(1<<n) * time.Second)
	if at.After(now.Add(30 * time.Second)) {
		at = now.Add(30 * time.Second)
	}
	if r.AvailableAt.After(at) {
		at = r.AvailableAt
	}
	if row.IsEmpty() {
		id, err := newID()
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO task_outbox(id,namespace,task_id,generation,class,status,available_at,created_at,deferrals) VALUES(?,?,?,?,?,'pending',?,?,1)`, id, s.cfg.Namespace, r.ID, gen, classOf(r.Definition), s.stamp(at), s.stamp(now))
		return err
	}
	_, err = tx.Exec(`UPDATE task_outbox SET status='pending',available_at=?,deferrals=deferrals+1,lease_owner=NULL,lease_until=NULL,sent_at=NULL WHERE task_id=? AND generation=?`, s.stamp(at), r.ID, gen)
	return err
}

func classOf(d Definition) string {
	if d.Class == "" {
		return DefaultClass
	}
	return d.Class
}

// SubmitTx 在调用方事务内提交手动运行（与业务数据及 Outbox 通知同事务提交或回滚）。
// tx 必须来自 StoreConfig.DB 同一数据库；跨库原子性无法承诺，应改用业务侧 Outbox。
func (s *Store) SubmitTx(ctx context.Context, tx gdb.TX, def Definition, requestKey string, fn Audit) (*Run, error) {
	if err := s.lock(ctx, tx); err != nil {
		return nil, err
	}
	return s.submit(ctx, tx, def, requestKey, fn)
}

// Publisher 是投递器所需的传输层发布能力：仅当消息持久化、可路由且 broker 确认后返回 nil。
type Publisher interface {
	Publish(ctx context.Context, class string, msg DispatchMessage) error
}

// Relay 是 Outbox 投递器。每次 RunOnce 租约领取一批通知并逐条发布，确认后写回。
type Relay struct {
	Store     *Store
	Publisher Publisher
	Owner     string        // 实例 ID 等唯一标识
	Batch     int           // 默认 100
	Lease     time.Duration // 发布租约，默认 30s
}

// RunOnce 返回本轮成功投递条数。单条发布失败不会中断本轮，按指数退避（1s..60s）保留重试。
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	if r.Store == nil || r.Publisher == nil || r.Owner == "" {
		return 0, fmt.Errorf("task: relay not configured")
	}
	batch, lease := r.Batch, r.Lease
	if batch == 0 {
		batch = 100
	}
	if lease == 0 {
		lease = 30 * time.Second
	}
	items, err := r.Store.ClaimOutbox(ctx, r.Owner, batch, lease)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, item := range items {
		if ctx.Err() != nil {
			return sent, ctx.Err()
		}
		pubCtx, cancel := context.WithTimeout(ctx, lease/2)
		err := r.Publisher.Publish(pubCtx, item.Class, item.Message)
		cancel()
		if err != nil {
			n := item.Attempts - 1
			if n > 6 {
				n = 6
			}
			delay := time.Duration(1<<n) * time.Second
			if delay > time.Minute {
				delay = time.Minute
			}
			if mErr := r.Store.MarkOutboxFailed(context.WithoutCancel(ctx), item, r.Owner, err.Error(), r.Store.now().Add(delay)); mErr != nil {
				return sent, mErr
			}
			continue
		}
		if _, err = r.Store.MarkOutboxSent(context.WithoutCancel(ctx), item, r.Owner); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

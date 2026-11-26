package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/google/uuid"
)

// PgRepository PG 实现（GF gdb；SKIP LOCKED 与租约校验用原生 SQL，强制主库连接语义由调用方保证）
type PgRepository struct{}

// NewPgRepository 构造
func NewPgRepository() *PgRepository { return &PgRepository{} }

const (
	tableTask    = "task"
	tableAttempt = "task_attempt"
)

// Find 按 ID 查
func (r *PgRepository) Find(ctx context.Context, id string) (*Task, error) {
	one, err := g.DB().Model(tableTask).Ctx(ctx).Where("id", id).Where("namespace", "").One()
	if err != nil {
		return nil, err
	}
	if one.IsEmpty() {
		return nil, nil
	}
	return hydrateTask(one), nil
}

// Save 插入或更新
func (r *PgRepository) Save(ctx context.Context, t *Task) error {
	payload := jsonb(t.Payload)
	if payload == nil {
		payload = "{}"
	}
	avail := t.AvailableAt
	if avail.IsZero() {
		avail = time.Now()
	}
	isNew := t.ID == ""
	if isNew {
		// 主键由组件生成 uuidv7（DDL 无 DEFAULT，插入全经组件，防绕过）
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		t.ID = id.String()
	}
	// 教训：GF Model.Data 是整体覆盖而非合并（与 Where 的 AND 累加语义不同），
	// 二次 Data 会丢弃首份字段集——id 必须并入同一份 map，不可链式追加
	m := g.DB().Model(tableTask).Ctx(ctx).Data(g.Map{
		"id":           t.ID,
		"task_no":      t.TaskNo,
		"type":         t.Type,
		"payload":      payload,
		"status":       t.Status,
		"priority":     t.Priority,
		"attempts":     t.Attempts,
		"max_attempts": t.MaxAttempts,
		"available_at": avail,
		"started_at":   t.StartedAt,
		"finished_at":  t.FinishedAt,
		"timeout":      t.Timeout,
		"progress":     t.Progress,
		"result":       jsonb(t.Result),
		"last_error":   t.LastError,
		"parent_id":    t.ParentID,
		"is_split":     t.IsSplit,
		"biz_ext":      jsonb(t.BizExt),
	})
	if isNew {
		_, err := m.Insert()
		return err
	}
	res, err := m.Where("id", t.ID).Where("namespace", "").Update()
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
}

// Claim SKIP LOCKED 抢占：priority DESC, available_at ASC 取 1 条转 running
func (r *PgRepository) Claim(ctx context.Context, workerID string) (*Task, error) {
	t, _, err := r.ClaimExecution(ctx, "", workerID)
	return t, err
}

// CreateAttempt 建租约（attempt id 由组件生成 uuidv7）
func (r *PgRepository) CreateAttempt(ctx context.Context, t *Task, workerID string) (*TaskAttempt, error) {
	row, scopeErr := g.DB().GetOne(ctx, `SELECT id FROM task WHERE id=? AND namespace='' AND status='running' AND attempts=?`, t.ID, t.Attempts)
	if scopeErr != nil {
		return nil, scopeErr
	}
	if row.IsEmpty() {
		return nil, ErrLeaseLost
	}

	existing, err := g.DB().GetOne(ctx, `SELECT * FROM task_attempt WHERE task_id=? AND attempt_no=? AND status='running'`, t.ID, t.Attempts)
	if err != nil {
		return nil, err
	}
	if !existing.IsEmpty() {
		if existing["worker_id"].String() != workerID {
			return nil, ErrLeaseLost
		}
		return &TaskAttempt{ID: existing["id"].String(), TaskID: t.ID, AttemptNo: t.Attempts, Status: AttemptRunning, WorkerID: workerID, StartedAt: existing["started_at"].Time()}, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	if _, err := g.DB().Model(tableAttempt).Ctx(ctx).Data(g.Map{
		"id":         id.String(),
		"task_id":    t.ID,
		"attempt_no": t.Attempts,
		"status":     AttemptRunning,
		"started_at": time.Now(),
		"worker_id":  workerID,
	}).Insert(); err != nil {
		return nil, err
	}
	return &TaskAttempt{ID: id.String(), TaskID: t.ID, AttemptNo: t.Attempts, Status: AttemptRunning, WorkerID: workerID, StartedAt: time.Now()}, nil
}

// FindActiveAttempt running 租约（uuidv7 同毫秒乱序，取权威序号 attempt_no 最新）
func (r *PgRepository) FindActiveAttempt(ctx context.Context, taskID string) (*TaskAttempt, error) {
	one, err := g.DB().Model(tableAttempt).Ctx(ctx).
		Where("task_id", taskID).Where("status", AttemptRunning).
		Order("attempt_no DESC").One()
	if err != nil {
		return nil, err
	}
	if one.IsEmpty() {
		return nil, nil
	}
	return &TaskAttempt{
		ID: one["id"].String(), TaskID: one["task_id"].String(),
		AttemptNo: one["attempt_no"].Int(), Status: one["status"].String(),
		WorkerID: one["worker_id"].String(),
	}, nil
}

// MarkAttemptSucceeded 租约校验回写（attempt + task 同事务；校验失败返回 false）
func (r *PgRepository) MarkAttemptSucceeded(ctx context.Context, a *TaskAttempt, result []byte) (bool, error) {
	return r.markAttemptFinished(ctx, a, AttemptSucceeded, StatusSucceeded, "", result)
}

// MarkAttemptFailed 失败回写
func (r *PgRepository) MarkAttemptFailed(ctx context.Context, a *TaskAttempt, errMsg string) (bool, error) {
	return r.markAttemptFinished(ctx, a, AttemptFailed, StatusFailed, errMsg, nil)
}

func (r *PgRepository) markAttemptFinished(ctx context.Context, a *TaskAttempt, attemptStatus, taskStatus, errMsg string, result []byte) (bool, error) {
	tx, err := g.DB().Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	row, err := tx.GetOne(`SELECT * FROM task WHERE id=? AND namespace='' FOR UPDATE`, a.TaskID)
	if err != nil {
		return false, err
	}
	if row.IsEmpty() || row["status"].String() != StatusRunning || row["attempts"].Int() != a.AttemptNo {
		return false, nil
	}
	// 租约校验：attempt 仍 running 才允许回写（result 以 JSON 字符串传参）
	resultParam := jsonQueryString(result)
	res, err := tx.Ctx(ctx).Exec(
		`UPDATE task_attempt SET status = ?, finished_at = NOW(), error = ?, result = ? WHERE id = ? AND task_id=? AND attempt_no=? AND status = ?`,
		attemptStatus, errMsg, resultParam, a.ID, a.TaskID, a.AttemptNo, AttemptRunning,
	)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return false, nil // 租约已被回收或已回写
	}

	if _, err = tx.Ctx(ctx).Exec(
		`UPDATE task SET status = ?, finished_at = NOW(), result = ?, last_error = ?, updated_at = NOW() WHERE id = ?`,
		taskStatus, resultParam, errMsg, a.TaskID,
	); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// MarkTimeout attempt 置 timeout
func (r *PgRepository) MarkTimeout(ctx context.Context, a *TaskAttempt) error {
	_, err := g.DB().Exec(ctx,
		`UPDATE task_attempt SET status = ?, finished_at = NOW() WHERE id = ? AND status = ? AND task_id IN (SELECT id FROM task WHERE namespace='')`,
		AttemptTimeout, a.ID, AttemptRunning)
	return err
}

// FindTimedOutTasks running 超 timeout
func (r *PgRepository) FindTimedOutTasks(ctx context.Context, limit int) ([]*Task, error) {
	rows, err := g.DB().GetAll(ctx,
		`SELECT * FROM task WHERE namespace='' AND status = ? AND timeout > 0 AND started_at IS NOT NULL AND started_at + (timeout || ' seconds')::interval < NOW() LIMIT ?`,
		StatusRunning, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*Task, 0, len(rows))
	for i := range rows {
		out = append(out, hydrateTask(rows[i]))
	}
	return out, nil
}

// FindFailedTasks RetryScan 候选（failed、到期、未达上限）
func (r *PgRepository) FindFailedTasks(ctx context.Context, limit int) ([]*Task, error) {
	rows, err := g.DB().GetAll(ctx,
		`SELECT * FROM task WHERE namespace='' AND status = ? AND available_at <= NOW() AND attempts < max_attempts LIMIT ?`,
		StatusFailed, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*Task, 0, len(rows))
	for i := range rows {
		out = append(out, hydrateTask(rows[i]))
	}
	return out, nil
}

// FindExhaustedFailedTasks failed 且 attempts >= max_attempts（RetryScan 转死）
func (r *PgRepository) FindExhaustedFailedTasks(ctx context.Context, limit int) ([]*Task, error) {
	rows, err := g.DB().GetAll(ctx,
		`SELECT * FROM task WHERE namespace='' AND status = ? AND attempts >= max_attempts LIMIT ?`,
		StatusFailed, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*Task, 0, len(rows))
	for i := range rows {
		out = append(out, hydrateTask(rows[i]))
	}
	return out, nil
}

// FindStuckAttempts running 超 1 小时的兜底扫描（timeout 回收的第一步按 task 走；
// 本接口用于 attempt 层孤儿租约）
func (r *PgRepository) FindStuckAttempts(ctx context.Context, limit int) ([]*TaskAttempt, error) {
	rows, err := g.DB().GetAll(ctx,
		`SELECT a.* FROM task_attempt a JOIN task t ON t.id = a.task_id
		 WHERE t.namespace='' AND a.status = ? AND t.timeout > 0 AND a.started_at + (t.timeout || ' seconds')::interval < NOW() LIMIT ?`,
		AttemptRunning, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*TaskAttempt, 0, len(rows))
	for i := range rows {
		out = append(out, &TaskAttempt{
			ID: rows[i]["id"].String(), TaskID: rows[i]["task_id"].String(),
			AttemptNo: rows[i]["attempt_no"].Int(), Status: rows[i]["status"].String(),
		})
	}
	return out, nil
}

// RescheduleToPending 仅 RetryScanner 调用（重试独占）
func (r *PgRepository) RescheduleToPending(ctx context.Context, t *Task, availableAt time.Time) error {
	_, err := g.DB().Exec(ctx,
		`UPDATE task SET status = ?, available_at = ?, updated_at = NOW() WHERE id = ? AND status='failed' AND namespace=''`,
		StatusPending, availableAt, t.ID)
	return err
}

// MarkDead 达上限转 dead
func (r *PgRepository) MarkDead(ctx context.Context, taskID string) error {
	_, err := g.DB().Exec(ctx,
		`UPDATE task SET status = ?, finished_at = NOW(), updated_at = NOW() WHERE id = ? AND status='failed' AND namespace=''`,
		StatusDead, taskID)
	return err
}

// BatchInsertChildren 批量建子（继承父五字段）
func (r *PgRepository) BatchInsertChildren(ctx context.Context, parent *Task, payloads [][]byte) ([]*Task, error) {
	children := make([]*Task, 0, len(payloads))
	for _, p := range payloads {
		child := &Task{
			TaskNo:      fmt.Sprintf("%s-c%d", parent.TaskNo, len(children)+1),
			Type:        parent.Type,
			Payload:     p,
			Status:      StatusPending,
			Priority:    parent.Priority,
			MaxAttempts: parent.MaxAttempts,
			Timeout:     parent.Timeout,
			ParentID:    &parent.ID,
			BizExt:      parent.BizExt,
		}
		if err := r.Save(ctx, child); err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	// 父标记已拆分
	if _, err := g.DB().Exec(ctx, `UPDATE task SET is_split = TRUE WHERE id = ?`, parent.ID); err != nil {
		return nil, err
	}
	return children, nil
}

// AggregateChildStatus 子任务聚合
func (r *PgRepository) AggregateChildStatus(ctx context.Context, parentID string) (*Aggregate, error) {
	rows, err := g.DB().GetAll(ctx, `SELECT status, COUNT(*) AS n FROM task WHERE parent_id = ? GROUP BY status`, parentID)
	if err != nil {
		return nil, err
	}
	agg := &Aggregate{}
	for i := range rows {
		n := int(rows[i]["n"].Int())
		switch rows[i]["status"].String() {
		case StatusSucceeded:
			agg.Succeeded += n
		case StatusFailed, StatusDead:
			agg.Failed += n
		case StatusRunning:
			agg.Running += n
		default:
			agg.Pending += n
		}
		agg.Total += n
	}
	// 派生：全部终态后定论；进度 = succeeded/total
	if agg.Total > 0 {
		agg.Progress = agg.Succeeded * 100 / agg.Total
	}
	switch {
	case agg.Total > 0 && agg.Succeeded == agg.Total:
		agg.DerivedStatus = StatusSucceeded
	case agg.Total > 0 && (agg.Failed == agg.Total):
		agg.DerivedStatus = StatusCompletedWithErrors
	case agg.Total > 0 && agg.Succeeded+agg.Failed == agg.Total:
		agg.DerivedStatus = StatusCompletedWithErrors // 有失败有成功
	case agg.Running > 0:
		agg.DerivedStatus = StatusRunning
	default:
		agg.DerivedStatus = StatusPending
	}
	return agg, nil
}

// jsonQueryString []byte → JSON 字符串（SQL 参数安全）；空返回 nil
func jsonQueryString(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func jsonb(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return string(b)
	}
	return v
}

func hydrateTask(one gdb.Record) *Task {
	t := &Task{
		ID:          one["id"].String(),
		TaskNo:      one["task_no"].String(),
		Type:        one["type"].String(),
		Payload:     []byte(one["payload"].String()),
		Status:      one["status"].String(),
		Priority:    one["priority"].Int(),
		Attempts:    one["attempts"].Int(),
		MaxAttempts: one["max_attempts"].Int(),
		Timeout:     one["timeout"].Int(),
		Progress:    one["progress"].Int(),
		Result:      []byte(one["result"].String()),
		LastError:   one["last_error"].String(),
		IsSplit:     one["is_split"].Bool(),
		BizExt:      []byte(one["biz_ext"].String()),
	}
	if v := one["parent_id"]; !v.IsNil() {
		pid := v.String()
		t.ParentID = &pid
	}
	if !one["available_at"].IsNil() {
		t.AvailableAt = one["available_at"].Time()
	}
	return t
}

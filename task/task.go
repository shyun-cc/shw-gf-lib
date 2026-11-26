// Package task 表驱动异步任务引擎（对应 PHP Foundation/Task，Table-First）。
//
// task 表是唯一真相源：队列消息只带 task_id（挂了不丢）；claim 用 PG
// FOR UPDATE SKIP LOCKED 抢占；task_attempt 是执行租约（回写校验仍 running）；
// 重试仅由 RetryScanner 独占触发（先回收 stuck 再重试 failed）；
// 指数退避 base=5s factor=2 cap=3600s（指数取 attempts-1）。
package task

import "time"

// 任务状态机
const (
	StatusPending             = "pending"
	StatusRunning             = "running"
	StatusSucceeded           = "succeeded"
	StatusFailed              = "failed"
	StatusCancelled           = "cancelled"
	StatusCompletedWithErrors = "completed_with_errors"
	StatusDead                = "dead"
)

// 尝试状态机
const (
	AttemptRunning   = "running"
	AttemptSucceeded = "succeeded"
	AttemptFailed    = "failed"
	AttemptTimeout   = "timeout"
)

// Task 任务行（字段对齐 PHP task 表 fillable；主键 uuidv7 文本）
type Task struct {
	ID          string
	TaskNo      string
	Type        string
	Payload     []byte
	Status      string
	Priority    int
	Attempts    int
	MaxAttempts int
	AvailableAt time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	Timeout     int // 秒；0 不限
	Progress    int
	Result      []byte
	LastError   string
	ParentID    *string
	IsSplit     bool
	BizExt      []byte
	CreatedAt   *time.Time
	UpdatedAt   *time.Time
}

// TaskAttempt 尝试行（执行租约；id/task_id 均 uuid 文本）
type TaskAttempt struct {
	ID         string
	TaskID     string
	AttemptNo  int
	Status     string
	StartedAt  time.Time
	FinishedAt *time.Time
	WorkerID   string
	Error      string
	Result     []byte
}

// CreationOptions 提交选项
type CreationOptions struct {
	AvailableAt time.Time // 单次任务首次可领取时间；零值使用当前时间
	TaskNo      string
	Type        string
	Payload     []byte
	Priority    int
	MaxAttempts int
	Timeout     int
	BizExt      []byte
	// ViaQueue true 时经 QueueProducer 发 task_id 消息（即时消费模式）；
	// false 为 pull 模式（单例拉取/并发拉取），不发消息
	ViaQueue bool
}

// Aggregate 子任务聚合结果（父任务查询时派生）
type Aggregate struct {
	Total     int
	Succeeded int
	Failed    int
	Running   int
	Pending   int
	// DerivedStatus 全子终态后派生：全成功→succeeded；有失败→completed_with_errors；否则 running/pending
	DerivedStatus string
	Progress      int // succeeded/total
}

// DDL PG 建表语句（tests/ 集成与项目建表用）。主键/FK 为 uuid：id 由组件内
// uuid.NewV7 生成，DDL 不带 DEFAULT（PG<18 无 uuidv7() 函数；且插入全经组件，
// 手工 SQL 插入因无 DEFAULT 失败是预期的防绕过特性）。
const DDL = `
CREATE TABLE IF NOT EXISTS task (
    id            UUID PRIMARY KEY,
    namespace     TEXT NOT NULL DEFAULT '',
    task_no       VARCHAR(64) NOT NULL DEFAULT '',
    type          VARCHAR(64) NOT NULL,
    payload       JSONB NOT NULL DEFAULT '{}',
    status        VARCHAR(32) NOT NULL DEFAULT 'pending',
    priority      INT NOT NULL DEFAULT 0,
    attempts      INT NOT NULL DEFAULT 0,
    max_attempts  INT NOT NULL DEFAULT 3,
    available_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    timeout       INT NOT NULL DEFAULT 0,
    progress      INT NOT NULL DEFAULT 0,
    result        JSONB,
    last_error    TEXT NOT NULL DEFAULT '',
    parent_id     UUID,
    is_split      BOOLEAN NOT NULL DEFAULT FALSE,
    biz_ext       JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_task_status_available ON task (status, available_at);
CREATE INDEX IF NOT EXISTS idx_task_parent ON task (parent_id);
CREATE INDEX IF NOT EXISTS idx_task_priority ON task (priority DESC, available_at ASC);

CREATE TABLE IF NOT EXISTS task_attempt (
    id          UUID PRIMARY KEY,
    task_id     UUID NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    attempt_no  INT NOT NULL,
    status      VARCHAR(32) NOT NULL DEFAULT 'running',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    worker_id   VARCHAR(64) NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    result      JSONB
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_attempt_number ON task_attempt(task_id,attempt_no);
CREATE INDEX IF NOT EXISTS idx_attempt_task ON task_attempt (task_id);
CREATE INDEX IF NOT EXISTS idx_attempt_status ON task_attempt (status, started_at);
`

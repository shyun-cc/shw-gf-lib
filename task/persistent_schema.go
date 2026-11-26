package task

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// UpgradeDDL 为已有 TIMESTAMPTZ task 表追加控制列。先停止旧执行器，再迁移、恢复孤儿、启动新版。
const UpgradeDDL = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_attempt_number ON task_attempt(task_id,attempt_no);
ALTER TABLE task ADD COLUMN IF NOT EXISTS namespace TEXT NOT NULL DEFAULT '';
ALTER TABLE task ADD COLUMN IF NOT EXISTS handler_version TEXT NOT NULL DEFAULT '';
ALTER TABLE task ADD COLUMN IF NOT EXISTS schedule_id UUID;
ALTER TABLE task ADD COLUMN IF NOT EXISTS schedule_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE task ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ;
ALTER TABLE task ADD COLUMN IF NOT EXISTS trigger_type TEXT NOT NULL DEFAULT '';
ALTER TABLE task ADD COLUMN IF NOT EXISTS request_key TEXT;
ALTER TABLE task ADD COLUMN IF NOT EXISTS replay_of UUID;
ALTER TABLE task ADD COLUMN IF NOT EXISTS definition JSONB NOT NULL DEFAULT '{}';
ALTER TABLE task ADD COLUMN IF NOT EXISTS active_attempt_id UUID;
ALTER TABLE task ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE task ADD COLUMN IF NOT EXISTS outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE task ADD COLUMN IF NOT EXISTS error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE task ADD COLUMN IF NOT EXISTS summary JSONB;
ALTER TABLE task_attempt ADD COLUMN IF NOT EXISTS lease_token UUID;
ALTER TABLE task_attempt ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
ALTER TABLE task_attempt ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;
ALTER TABLE task_attempt ADD COLUMN IF NOT EXISTS deadline_at TIMESTAMPTZ;
ALTER TABLE task_attempt ADD COLUMN IF NOT EXISTS checkpoint JSONB;
ALTER TABLE task_attempt ADD COLUMN IF NOT EXISTS trace_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_schedule_point ON task(schedule_id,scheduled_at) WHERE trigger_type='scheduled';
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_request ON task(namespace,request_key) WHERE request_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_task_route ON task(namespace,type,handler_version,status,available_at);
CREATE INDEX IF NOT EXISTS idx_task_lease_expiry ON task_attempt(lease_expires_at) WHERE status='running';
CREATE TABLE IF NOT EXISTS task_schedule (
 id UUID PRIMARY KEY, namespace TEXT NOT NULL, name TEXT NOT NULL, enabled BOOLEAN NOT NULL,
 version BIGINT NOT NULL, definition_version BIGINT NOT NULL, spec JSONB NOT NULL,
 next_run_at TIMESTAMPTZ NOT NULL, last_scheduled_at TIMESTAMPTZ, misfire_from TIMESTAMPTZ, misfire_until TIMESTAMPTZ, missed_count BIGINT NOT NULL DEFAULT 0,
 UNIQUE(namespace,name)
);
CREATE INDEX IF NOT EXISTS idx_schedule_due ON task_schedule(namespace,enabled,next_run_at);
CREATE TABLE IF NOT EXISTS task_instance (
 id UUID PRIMARY KEY, namespace TEXT NOT NULL, info JSONB NOT NULL,
 started_at TIMESTAMPTZ NOT NULL, heartbeat_at TIMESTAMPTZ NOT NULL, stopped_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS task_concurrency (
 namespace TEXT NOT NULL, key TEXT NOT NULL, active_task_id UUID NOT NULL REFERENCES task(id),
 PRIMARY KEY(namespace,key)
);
CREATE TABLE IF NOT EXISTS task_command (
 namespace TEXT NOT NULL, request_key TEXT NOT NULL, target_id UUID NOT NULL, action TEXT NOT NULL,
 fingerprint TEXT NOT NULL, result_id UUID, created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(namespace,request_key)
);
`

// PlatformDDL 为 v0.13 统一任务平台追加的表与列（在 UpgradeDDL 之后执行，可重复执行）。
// 已有 v0.12 TIMESTAMPTZ 库执行本常量；timestamp 墙上时间库用 PlatformSchema(TimeWall)。
const PlatformDDL = `
CREATE TABLE IF NOT EXISTS task_type (
 namespace TEXT NOT NULL, type TEXT NOT NULL, handler_version TEXT NOT NULL, class TEXT NOT NULL,
 submit_enabled BOOLEAN NOT NULL DEFAULT FALSE, consume_enabled BOOLEAN NOT NULL DEFAULT FALSE,
 global_concurrency INT NOT NULL DEFAULT 1, rate_limit INT NOT NULL DEFAULT 0, rate_window_ms BIGINT NOT NULL DEFAULT 0,
 policy JSONB NOT NULL, version BIGINT NOT NULL, registered_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(namespace,type)
);
CREATE TABLE IF NOT EXISTS task_config_revision (namespace TEXT PRIMARY KEY, revision BIGINT NOT NULL);
ALTER TABLE task_instance ADD COLUMN IF NOT EXISTS applied_revision BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_task_type_status ON task(namespace,type,status);
CREATE INDEX IF NOT EXISTS idx_attempt_started ON task_attempt(started_at);
ALTER TABLE task ADD COLUMN IF NOT EXISTS dispatch_generation BIGINT NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS task_outbox (
 id UUID PRIMARY KEY, namespace TEXT NOT NULL, task_id UUID NOT NULL REFERENCES task(id) ON DELETE CASCADE,
 generation BIGINT NOT NULL, class TEXT NOT NULL, status TEXT NOT NULL, available_at TIMESTAMPTZ NOT NULL,
 lease_owner TEXT, lease_until TIMESTAMPTZ, publish_attempts INT NOT NULL DEFAULT 0, deferrals INT NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL, sent_at TIMESTAMPTZ,
 UNIQUE(task_id,generation)
);
CREATE INDEX IF NOT EXISTS idx_outbox_due ON task_outbox(namespace,status,available_at);
ALTER TABLE task ADD COLUMN IF NOT EXISTS cancel_requested BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE task ADD COLUMN IF NOT EXISTS child_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_child_key ON task(parent_id,child_key) WHERE child_key IS NOT NULL AND replay_of IS NULL;
ALTER TABLE task ADD COLUMN IF NOT EXISTS order_scope TEXT;
ALTER TABLE task ADD COLUMN IF NOT EXISTS ordering_key TEXT;
ALTER TABLE task ADD COLUMN IF NOT EXISTS order_seq BIGINT;
CREATE INDEX IF NOT EXISTS idx_task_order ON task(namespace,order_scope,ordering_key,order_seq) WHERE ordering_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS task_order (
 namespace TEXT NOT NULL, scope TEXT NOT NULL, key TEXT NOT NULL, next_seq BIGINT NOT NULL,
 PRIMARY KEY(namespace,scope,key)
);
CREATE TABLE IF NOT EXISTS task_split (
 task_id UUID PRIMARY KEY REFERENCES task(id) ON DELETE CASCADE, namespace TEXT NOT NULL, state TEXT NOT NULL,
 cursor JSONB, split_completed BOOLEAN NOT NULL DEFAULT FALSE, generated BIGINT NOT NULL DEFAULT 0, batches BIGINT NOT NULL DEFAULT 0,
 continuations INT NOT NULL DEFAULT 0, summary_version BIGINT NOT NULL DEFAULT 0, updated_at TIMESTAMPTZ NOT NULL
);
`

// PlatformSchema 返回给定时间模式下的 PlatformDDL（已有库升级用）。
func PlatformSchema(mode string) (string, error) {
	if mode == "" || mode == TimeInstant {
		return PlatformDDL, nil
	}
	if mode == TimeWall {
		return strings.ReplaceAll(PlatformDDL, "TIMESTAMPTZ", "TIMESTAMP"), nil
	}
	return "", fmt.Errorf("task: invalid time mode")
}

// Schema 返回新库完整建表 SQL（含 PlatformDDL）。wall 模式只支持固定偏移地区（避免 DST 墙上时间歧义）。
func Schema(mode string) (string, error) {
	if mode == "" || mode == TimeInstant {
		return DDL + UpgradeDDL + PlatformDDL, nil
	}
	if mode == TimeWall {
		return strings.ReplaceAll(DDL+UpgradeDDL+PlatformDDL, "TIMESTAMPTZ", "TIMESTAMP"), nil
	}
	return "", fmt.Errorf("task: invalid time mode")
}

// ConvertTimeColumnsSQL 返回显式切换已有表时间列的迁移模板，不自动执行。
// 必须停所有执行器并备份；向相反模式生成 SQL 即为转换回退模板。
func ConvertTimeColumnsSQL(to, zone string) (string, error) {
	if to != TimeWall && to != TimeInstant {
		return "", fmt.Errorf("task: invalid time mode")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return "", err
	}
	var out strings.Builder
	tables := map[string][]string{"task": {"available_at", "started_at", "finished_at", "created_at", "updated_at", "scheduled_at"}, "task_attempt": {"started_at", "finished_at", "heartbeat_at", "lease_expires_at", "deadline_at"}, "task_schedule": {"next_run_at", "last_scheduled_at", "misfire_from", "misfire_until"}, "task_instance": {"started_at", "heartbeat_at", "stopped_at"}, "task_command": {"created_at"}, "task_type": {"registered_at", "updated_at"}, "task_outbox": {"available_at", "lease_until", "created_at", "sent_at"}, "task_split": {"updated_at"}}
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, table := range names {
		cols := tables[table]
		for _, col := range cols {
			fmt.Fprintf(&out, "ALTER TABLE %s ALTER COLUMN %s TYPE %s USING %s AT TIME ZONE '%s';\n", table, col, to, col, strings.ReplaceAll(zone, "'", "''"))
		}
	}
	return out.String(), nil
}

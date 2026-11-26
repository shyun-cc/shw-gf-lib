// gf_migrate_state 旁表与迁移完整性对账：goose 只记录「应用到了哪个
// 版本」，不校验已应用文件后续是否被删除 / 重命名 / 改内容（R2 / R1
// 风险）。本文件提供旁表 DDL、goose 版本表读取、对账决策纯函数
// reconcile 与补录 / 清理 SQL 执行层。Up 的接线见后续任务，本文件
// 不感知调用时机。

package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
)

// stateTableDDL gf_migrate_state 旁表 DDL：按迁移文件粒度记录完整性
// 基准（版本号 + 文件名 + 内容 hash 三元组），与 goose 版本表交叉
// 校验。幂等建表；recorded_at 由数据库默认值填充，不由应用侧传入。
const stateTableDDL = `CREATE TABLE IF NOT EXISTS gf_migrate_state (
	version_id  bigint PRIMARY KEY,
	filename    text NOT NULL,
	checksum    char(64) NOT NULL,
	recorded_at timestamptz NOT NULL DEFAULT now()
)`

// stateRow gf_migrate_state 旁表一行记录的对账视图。
type stateRow struct {
	Version  int64  // 迁移版本号（文件名数字前缀）
	Filename string // 记录时的迁移文件名
	Checksum string // 记录时文件内容的 sha256 hex（64 字符）
}

// fsFile 迁移文件的对账视图：版本号从文件名数字前缀解析（parseVersion），
// checksum 为文件内容的 sha256 hex（checksumHex），与旁表 char(64) 对齐。
type fsFile struct {
	Version  int64
	Filename string
	Checksum string
}

// parseVersion 从 goose 迁移文件名的数字前缀解析版本号：
// "00003_init.sql" → (3, true)。无数字前缀或数值超出 int64 时返回
// (0, false)，由调用方过滤非迁移文件（README、隐藏文件等）。
func parseVersion(filename string) (int64, bool) {
	i := 0
	for i < len(filename) && filename[i] >= '0' && filename[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(filename[:i], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// checksumHex 计算迁移文件内容的 sha256 hex（64 字符小写十六进制），
// 作为旁表 checksum 列的写入值与对账比对基准。
func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ensureStateTable 确保 gf_migrate_state 旁表存在（幂等 DDL，对账前
// 调用）。DB 错误统一以「校验迁移完整性失败」前缀包装，错误链可解出
// 底层原因。
func ensureStateTable(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, stateTableDDL); err != nil {
		return fmt.Errorf("校验迁移完整性失败: %w", err)
	}
	return nil
}

// listAppliedVersions 读取 goose 版本表（goose_db_version）中已应用
// （is_applied）的版本号，升序去重返回。
//
// 容错：goose 首次 Up 之前版本表尚未建立，此时返回空集（视为「无已
// 应用版本」）而非报错。表存在性经 information_schema.tables 判定，
// 纯标准 SQL，不依赖具体驱动的错误类型。DISTINCT 防御 goose down→up
// 在版本表留下同版本多行 is_applied 记录的形态。排除 version_id = 0：
// goose 建版本表时必然插入的零版本基线行（is_applied=true），它不对应
// 任何迁移文件，进入对账会被误判为 R2 缺失。
func listAppliedVersions(ctx context.Context, db *sql.DB) ([]int64, error) {
	const tableExists = `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_name = 'goose_db_version'
	)`
	var exists bool
	if err := db.QueryRowContext(ctx, tableExists).Scan(&exists); err != nil {
		return nil, fmt.Errorf("校验迁移完整性失败: %w", err)
	}
	if !exists {
		return nil, nil
	}

	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT version_id FROM goose_db_version WHERE is_applied AND version_id > 0 ORDER BY version_id`)
	if err != nil {
		return nil, fmt.Errorf("校验迁移完整性失败: %w", err)
	}
	defer rows.Close()

	var versions []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("校验迁移完整性失败: %w", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("校验迁移完整性失败: %w", err)
	}
	return versions, nil
}

// reconcile 迁移完整性对账决策（纯函数，无任何 IO）：以 goose 版本表
// 已应用版本为主线，交叉比对旁表记录与 FS 迁移文件，产出三类结论：
//
//   - backfill：版本表已应用、FS 文件在、但旁表缺记录的补录清单
//     （首次基线化与中途失败补录共用同一产出）；
//   - baseline：首次全量基线化标志——旁表原本为空且版本表非空。区别于
//     旁表半途丢失的中途补录（旁表非空），供上层记日志区分场景；
//   - orphans：旁表有记录但版本表无该版本的孤儿清单（不报错，交上层
//     调 deleteOrphanState 清理）。
//
// 硬错误两类（命中即返回，检查顺序即优先级）：
//
//   - R2 文件缺失：版本表已应用 v 但 FS 无版本 v 文件（被删除），或同
//     版本号下旁表文件名 ≠ FS 文件名（被重命名 / 仅改描述）——错误文案
//     「已应用的迁移文件缺失: <名字>（禁止删除或重命名已应用文件）」，
//     名字优先取旁表记录的文件名（最后已知名），旁表无记录时退回版本号；
//   - R1 内容被修改：同版本号同文件名但旁表 checksum ≠ FS checksum——
//     错误文案「已应用的迁移文件被修改: <文件名>（禁止修改已应用文件，
//     请新增迁移文件）」。
func reconcile(applied []int64, state []stateRow, files []fsFile) (backfill []fsFile, baseline bool, orphans []int64, err error) {
	stateByVer := make(map[int64]stateRow, len(state))
	for _, row := range state {
		stateByVer[row.Version] = row
	}
	fileByVer := make(map[int64]fsFile, len(files))
	for _, f := range files {
		fileByVer[f.Version] = f
	}

	backfill = make([]fsFile, 0)
	for _, v := range applied {
		// 纵深防御：零版本是 goose 建版本表时插入的基线行，不对应任何
		// 迁移文件（listAppliedVersions 的 SQL 已排除；此处兜底，防 SQL
		// 层未来回归把 0 放进对账被误判为 R2 缺失）。
		if v <= 0 {
			continue
		}
		f, hasFile := fileByVer[v]
		if !hasFile {
			// R2：FS 无版本 v 文件（被删除）。名字取旁表最后已知
			// 文件名，旁表也无记录时退回版本号。
			name := strconv.FormatInt(v, 10)
			if row, ok := stateByVer[v]; ok {
				name = row.Filename
			}
			return nil, false, nil, fmt.Errorf(
				"已应用的迁移文件缺失: %s（禁止删除或重命名已应用文件）", name)
		}
		row, hasState := stateByVer[v]
		if !hasState {
			// 旁表缺记录（首次基线化 / 中途失败补录）：进补录清单。
			backfill = append(backfill, f)
			continue
		}
		if row.Filename != f.Filename {
			// R2：同版本号但文件名变了（重命名 / 仅改描述）。报旁表
			// 记录的旧文件名——它才是「已应用却已不在 FS」的那个文件。
			return nil, false, nil, fmt.Errorf(
				"已应用的迁移文件缺失: %s（禁止删除或重命名已应用文件）", row.Filename)
		}
		if row.Checksum != f.Checksum {
			// R1：文件名未变但内容 hash 变了（被修改）。报 FS 当前
			// 文件名（此时与旁表记录一致）。
			return nil, false, nil, fmt.Errorf(
				"已应用的迁移文件被修改: %s（禁止修改已应用文件，请新增迁移文件）", f.Filename)
		}
	}

	// 基线化判定：旁表原本为空且版本表非空。旁表为空时已应用版本必然
	// 全部进了补录清单，无需再比对 backfill 长度；两表皆空的全新库不算
	// 基线化（无已应用版本可基线）。
	baseline = len(state) == 0 && len(applied) > 0

	// 孤儿检测：旁表有记录但版本表无该版本（goose 侧回滚 / 清库后的
	// 残留），不报错，交上层删除。
	appliedSet := make(map[int64]struct{}, len(applied))
	for _, v := range applied {
		appliedSet[v] = struct{}{}
	}
	orphans = make([]int64, 0)
	for _, row := range state {
		if _, ok := appliedSet[row.Version]; !ok {
			orphans = append(orphans, row.Version)
		}
	}
	return backfill, baseline, orphans, nil
}

// backfillState 把补录清单写入旁表（纯 INSERT：补录场景旁表必无该
// 版本记录，冲突即并发异常，应让错误暴露而非静默覆盖）。
func backfillState(ctx context.Context, db *sql.DB, files []fsFile) error {
	for _, f := range files {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO gf_migrate_state (version_id, filename, checksum) VALUES ($1, $2, $3)`,
			f.Version, f.Filename, f.Checksum); err != nil {
			return fmt.Errorf("校验迁移完整性失败: %w", err)
		}
	}
	return nil
}

// deleteOrphanState 删除旁表孤儿记录（版本表已无该版本的旁表行），
// reconcile 返回的 orphans 清单的落地动作。
func deleteOrphanState(ctx context.Context, db *sql.DB, versions []int64) error {
	for _, v := range versions {
		if _, err := db.ExecContext(ctx,
			`DELETE FROM gf_migrate_state WHERE version_id = $1`, v); err != nil {
			return fmt.Errorf("校验迁移完整性失败: %w", err)
		}
	}
	return nil
}

// recordApplied 单个迁移应用成功后写入旁表（UPSERT：版本号冲突时刷新
// 文件名与 checksum，重放安全）。
func recordApplied(ctx context.Context, db *sql.DB, file fsFile) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO gf_migrate_state (version_id, filename, checksum)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (version_id) DO UPDATE
		 SET filename = EXCLUDED.filename, checksum = EXCLUDED.checksum`,
		file.Version, file.Filename, file.Checksum); err != nil {
		return fmt.Errorf("校验迁移完整性失败: %w", err)
	}
	return nil
}

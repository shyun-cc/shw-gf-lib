// Package migrate 数据库迁移执行引擎（goose 迁移引擎的公司统一封装）。
//
// 定位：封装 goose 迁移执行器。
// 项目侧只保留四段接线代码：embed 声明 + fs.Sub + New 构造 Migrator +
// 挂载命令；迁移 SQL 与 embed 声明留在项目侧。goose 类型被完全封装在
// 本包内部，导出 API 零 goose 依赖——「项目源码不 import goose」由
// 编译器结构保证。
//
// 构建分身契约：默认构建即生产形态，`-tags dev` 才有 --fresh 能力
// （见 command_default.go / command_dev.go / fresh_dev.go）。
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"regexp"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/pressly/goose/v3"
)

// Dialect 数据库方言（迁移 SQL 的目标数据库类型）。
type Dialect int

const (
	// DialectPostgres PostgreSQL 方言（当前唯一支持的方言，扩方言时
	// 必须同步补 gooseDialect 映射，否则 New 构造期即拒绝）。
	DialectPostgres Dialect = iota + 1
)

// migrationNameRe 迁移文件名格式契约（R7）：{5 位数字}_{描述}.sql，
// 描述仅限小写字母 / 数字 / 下划线。文件名是迁移排序与版本比对的
// 唯一依据，位数不足、连字符、无下划线等格式失真的名字会在 New
// 构造期直接拒绝，不拖到执行期 goose 报错。
var migrationNameRe = regexp.MustCompile(`^\d{5}_[a-z0-9_]+\.sql$`)

// Config 迁移引擎配置，全部参数由项目侧注入，New 构造期完成全量校验。
type Config struct {
	// FS 必填：项目 embed 的迁移文件（一般是 embed.FS 经 fs.Sub
	// 取出的迁移目录）。
	FS fs.FS

	// Dialect 必填：目标数据库方言，目前仅 DialectPostgres。
	Dialect Dialect

	// DB 可选：数据库连接工厂。缺省 nil 时走 g.DB().Master()；
	// 测试或特殊部署环境可注入自定义连接来源。
	DB func() (*sql.DB, error)
}

// Migrator 数据库迁移执行器。
type Migrator struct {
	fs        fs.FS                   // 迁移文件系统（构造期已保证非 nil）
	dialect   Dialect                 // 目标数据库方言（构造期已保证受支持）
	dbFactory func() (*sql.DB, error) // 连接工厂，nil 表示走 g.DB().Master()
}

// New 创建迁移执行器。构造期完成全量校验，不把配置错误拖到执行期：
// FS 为 nil、方言未支持（零值及映射表外的一切值）或迁移文件名格式
// 不合规（R7）立即返回明确错误。
func New(cfg Config) (*Migrator, error) {
	if cfg.FS == nil {
		return nil, fmt.Errorf("迁移文件系统未初始化，请联系开发者")
	}
	if _, ok := gooseDialect(cfg.Dialect); !ok {
		return nil, fmt.Errorf("不支持的数据库方言")
	}
	if err := validateMigrationNames(cfg.FS); err != nil {
		return nil, err
	}
	return &Migrator{
		fs:        cfg.FS,
		dialect:   cfg.Dialect,
		dbFactory: cfg.DB,
	}, nil
}

// validateMigrationNames 校验 FS 内全部迁移文件名格式（R7）：仅
// .sql 后缀参与校验（README 等说明文件不受限），每个文件名须匹配
// {5 位数字}_{描述}.sql。goose 约定迁移文件平铺在 FS 根层，WalkDir
// 全量遍历兜底——嵌套路径含路径分隔符同样不匹配、同样拒绝。
func validateMigrationNames(fsys fs.FS) error {
	// 回调参数避开包名 path，后缀判断用 path.Ext（WalkDir 传入的
	// 路径以 / 分隔，path.Ext 与 filepath.Ext 行为一致）。
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".sql" {
			return nil
		}
		if !migrationNameRe.MatchString(p) {
			return fmt.Errorf("迁移文件名格式不合法: %s（须为 {5 位数字}_{描述}.sql，如 00001_create_users.sql）", p)
		}
		return nil
	})
}

// Up 执行增量迁移：仅应用未执行的迁移文件，已应用的自动跳过。
// 执行迁移 SQL 前依次过两道门，均先审后写：
//
//   - 完整性对账门：以 goose 版本表的已应用版本为主线，交叉比对
//     gf_migrate_state 旁表与 FS 迁移文件——已应用文件被删除、重命名
//     或改内容（R2/R1）在任何写库动作之前直接拒绝；旁表缺记录自动
//     补录、孤儿记录自动清理，首次接入时输出基线化日志；
//   - 粒度校验门：仅校验本轮待应用（版本不在已应用集内）的文件，
//     已应用文件不追溯——粒度规约接入晚于存量文件，追溯会把存量
//     项目一次性打红，且其内容已被完整性门锁定。
//
// 迁移应用成功后逐文件写旁表；写旁表失败时迁移本身已生效，返回错误
// 即可——下次 Up 该版本已进版本表而旁表缺记录，恰好落进对账门的
// 补录路径自愈，迁移 SQL 不会重复执行。
//
// 全部错误以中文文案 + %w 包装返回，错误链可解出底层原因。
func (m *Migrator) Up(ctx context.Context) error {
	g.Log().Info(ctx, "执行数据库增量迁移...")

	db, err := m.db()
	if err != nil {
		return fmt.Errorf("获取数据库连接失败: %w", err)
	}

	// ---- 完整性对账门：R1/R2 硬错误必须拦在任何写库之前 ----

	if err := ensureStateTable(ctx, db); err != nil {
		return err
	}
	applied, err := listAppliedVersions(ctx, db)
	if err != nil {
		return err
	}
	state, err := listStateRows(ctx, db)
	if err != nil {
		return err
	}
	migrations, err := collectMigrationFiles(m.fs)
	if err != nil {
		return err
	}
	fsFiles := make([]fsFile, len(migrations))
	for i, mf := range migrations {
		fsFiles[i] = mf.file
	}

	// reconcile 是纯函数：先拿到对账结论再决定写不写库，R1/R2 命中
	// 时旁表与业务库都未被触碰。
	backfill, baseline, orphans, err := reconcile(applied, state, fsFiles)
	if err != nil {
		return err
	}
	if baseline {
		g.Log().Info(ctx, "已锁定迁移基线，此后已应用文件不可变更")
	}
	if err := backfillState(ctx, db, backfill); err != nil {
		return err
	}
	if len(orphans) > 0 {
		g.Log().Warningf(ctx, "gf_migrate_state 存在 %d 条孤儿记录（版本表已无对应版本）: %v，自动清理", len(orphans), orphans)
	}
	if err := deleteOrphanState(ctx, db, orphans); err != nil {
		return err
	}

	// ---- 粒度校验门：仅待应用文件，provider.Up 之前拦截 ----

	appliedSet := make(map[int64]struct{}, len(applied))
	for _, v := range applied {
		appliedSet[v] = struct{}{}
	}
	for _, mf := range migrations {
		if _, ok := appliedSet[mf.file.Version]; ok {
			continue
		}
		if err := validateGranularity(mf.file.Filename, mf.content); err != nil {
			return err
		}
	}

	provider, err := goose.NewProvider(gooseDialectOf(m.dialect), db, m.fs)
	if err != nil {
		return fmt.Errorf("初始化迁移 provider 失败: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("执行迁移失败: %w", err)
	}

	if len(results) == 0 {
		g.Log().Info(ctx, "数据库已是最新版本，无待执行迁移")
	} else {
		g.Log().Infof(ctx, "迁移完成，本次执行 %d 个文件", len(results))
	}

	// ---- 应用成功后写旁表 ----

	fileByVer := make(map[int64]fsFile, len(migrations))
	for _, mf := range migrations {
		fileByVer[mf.file.Version] = mf.file
	}
	for _, r := range results {
		f, ok := fileByVer[r.Source.Version]
		if !ok {
			// 理论不可达：results 来源就是 m.fs，版本必在收集清单内。
			continue
		}
		if err := recordApplied(ctx, db, f); err != nil {
			return err
		}
	}
	return nil
}

// migrationFile 迁移文件的执行期视图：fsFile 对账三元组之外携带文件
// 原文——对账 checksum（锚定全文）与粒度校验（解析 Up 段）共用同一
// 次读盘结果。
type migrationFile struct {
	file    fsFile
	content string
}

// collectMigrationFiles 全量收集 FS 内迁移文件并读出原文：文件名取
// WalkDir 的完整相对路径（与旁表 filename 列同一口径），版本号解析自
// 文件名数字前缀，checksum 为 parseGooseFile 的全文 sha256。非 .sql
// 文件与无数字前缀的文件跳过——后者仅防御性兜底，New 的文件名校验
// 已保证 .sql 文件必有 5 位数字前缀。
func collectMigrationFiles(fsys fs.FS) ([]migrationFile, error) {
	var migrations []migrationFile
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".sql" {
			return nil
		}
		version, ok := parseVersion(p)
		if !ok {
			return nil
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("读取迁移文件失败: %s: %w", p, err)
		}
		_, checksum := parseGooseFile(string(raw))
		migrations = append(migrations, migrationFile{
			file:    fsFile{Version: version, Filename: p, Checksum: checksum},
			content: string(raw),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return migrations, nil
}

// listStateRows 读取 gf_migrate_state 旁表全部记录（reconcile 的对账
// 输入之一）。调用前提：ensureStateTable 已执行，表必存在。DB 错误
// 与 state.go 的 SQL 执行层同款「校验迁移完整性失败」前缀。
func listStateRows(ctx context.Context, db *sql.DB) ([]stateRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version_id, filename, checksum FROM gf_migrate_state`)
	if err != nil {
		return nil, fmt.Errorf("校验迁移完整性失败: %w", err)
	}
	defer rows.Close()

	var list []stateRow
	for rows.Next() {
		var r stateRow
		if err := rows.Scan(&r.Version, &r.Filename, &r.Checksum); err != nil {
			return nil, fmt.Errorf("校验迁移完整性失败: %w", err)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("校验迁移完整性失败: %w", err)
	}
	return list, nil
}

// db 取数据库连接：注入了 DB 工厂则用之，否则走 gf 默认数据源的
// master 连接（迁移是写操作，不经从库）。
func (m *Migrator) db() (*sql.DB, error) {
	if m.dbFactory != nil {
		return m.dbFactory()
	}
	return g.DB().Master()
}

// gooseDialect 将本包方言枚举映射为 goose 方言。未支持的方言
// （含零值、未来扩了枚举但漏补映射的值）返回 ok=false，由 New
// 构造期拒绝。
func gooseDialect(d Dialect) (goose.Dialect, bool) {
	switch d {
	case DialectPostgres:
		return goose.DialectPostgres, true
	default:
		return "", false
	}
}

// gooseDialectOf 取已通过构造期校验的方言对应的 goose 标识。
// 与 gooseDialect 的区别：本函数用于执行期，方言已校验，直接
// 忽略第二返回值（走到这里 ok 必为 true）。
func gooseDialectOf(d Dialect) goose.Dialect {
	gd, _ := gooseDialect(d)
	return gd
}

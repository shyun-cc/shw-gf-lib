//go:build integration && dev

// migrate 引擎 Fresh 的真 PostgreSQL 集成测试（change: add-migrate-package，task 4.2）。
//
// 被测对象：主库 migrate 包 dev 构建下的 Migrator.Fresh（--fresh 清库重建）。
// Fresh 的方法体物理上仅存在于 -tags dev 的构建（fresh_dev.go），因此本文件
// 构建标签为 integration && dev（两标签同时存在才编译）。重点覆盖 Config.DB
// 为 nil 的缺省连接路径：引擎内部 g.DB().Master() 拿到的连接走库默认
// search_path（public）——
// 与 Up 测试（migrate_test.go）的临时 schema 隔离不同，Fresh 的语义就是写死
// 清空并重建 public schema，这正是本测试要验证的行为。
//
// 安全警告（务必先读）：本测试会对 PG_DSN 指向的库执行
// DROP SCHEMA IF EXISTS public CASCADE + CREATE SCHEMA public——public
// schema 下所有既有对象（表、视图、序列等）将被全部清空，不可恢复！
// PG_DSN 必须指向专用的一次性测试库，严禁指向任何存有真实数据的库。
// 可用 docker 一键准备一次性测试库（跑完即弃）：
//
//	docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=testdb postgres:16
//
// 测试场景（spec「--fresh 清库重建」+ 粒度与 fresh 交互，task 5.2）：
//   - 既有用例：public 预置无关杂表（fresh_junk）→ Fresh → 断言：返回
//     nil；杂表随 DROP SCHEMA 消失（清库生效）；两个迁移文件的业务表与
//     goose 版本表在 public 重建（全量重放）；版本表行数 = 文件数 + 1 条
//     goose 基线行（version=0）。
//   - TestMigrateGranularity 系（粒度门 × Up）：待应用合规新文件（单对象
//     多语句：同表 ALTER + INDEX）通过并逐文件记录旁表 checksum /
//     待应用批次文件（一文件两 CREATE TABLE）在 provider.Up 之前被
//     「迁移文件粒度不合规」拒绝、库中无任何表被建。两用例是 Up 语义，
//     复用 migrate_test.go 的 setupPG 临时 schema 隔离（不触碰 public；
//     本文件标签 integration && dev 下该文件同包在编，helper 可直接用）。
//   - TestMigrateFresh 系新增（粒度门 × Fresh）：FS 含批次存量文件时
//     Fresh 在粒度门硬错误（fresh 清掉两表后全部文件按待应用走粒度校验，
//     拆分是 fresh 重建的前置条件）；拆分成单表文件后 Fresh 成功、两张
//     表都在 public、旁表重录齐全。
//
// 与 Up 测试共存：Up 测试全部落在随机临时 schema，本测试只动 public，
// 二者互不干扰、无执行顺序要求；各自 t.Cleanup 只清理自己建的对象
// （本测试清理 Fresh 重建出的表，不触碰 public 以外的任何 schema）。
//
// 环境要求（连接信息一律经环境变量注入，不硬编码凭证）：
//   - PG_DSN（必填，缺失则 Skip）：仅支持 URL 形式，
//     如 postgres://user:pass@127.0.0.1:5432/dbname（同 Up 测试约束）。
//
// 运行方式（见 doc.go 包注释；注意 Fresh 需要 dev 标签）：
//
//	cd tests
//	PG_DSN='postgres://user:pass@127.0.0.1:5432/testdb' \
//	  GOWORK=off go test -tags "integration dev" ./... -run MigrateFresh -v
package tests

import (
	"context"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	// 注册 gf 的 pgsql 驱动：引擎缺省路径 g.DB().Master() 依赖 gdb 驱动
	// 注册表。migrate_test.go 里有同样的 blank import（同包不同文件各写
	// 各的，幂等且互不冲突），本文件自带一份保证自包含。
	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/migrate"
)

// setupFreshPG 准备 Fresh 测试的 PG 环境：解析 PG_DSN（缺失则带安全警告
// Skip）、连通性预检、把 gf default 分组配置注入为不带 Namespace 的基础
// 节点（search_path 保持库默认，引擎连接因此落在 public——Fresh 的操作
// 目标），并在 public 预置杂表、注册 t.Cleanup。
//
// 与 Up 测试的 setupPG 需求不同（不建临时 schema、不切 Namespace），故
// 本函数独立；公共部分（parsePGDSN / setPGConfig / gooseSQL）全部复用
// migrate_test.go 的既有实现。返回的 fixture 沿用 Up 测试的 pgFixture，
// schema 字段按 Fresh 的语义固定填 "public"，tableExists / versionRows
// 两个断言辅助因此原样复用（tableExists 限定 table_schema = public；
// versionRows 走默认 search_path，与引擎建表连接一致）。
func setupFreshPG(t *testing.T) *pgFixture {
	t.Helper()

	// ① 环境检查：缺失则跳过。警告文案必须醒目——设置后本测试会清空
	// 指向库的 public schema，只允许一次性测试库。
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("未设置 PG_DSN，跳过 Fresh 集成测试。安全警告：设置后本测试会清空 PG_DSN 指向库的 public schema（DROP SCHEMA public CASCADE，既有数据全部丢失且不可恢复），务必指向专用一次性测试库；可用 docker 快速准备：docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=testdb postgres:16")
	}

	node, err := parsePGDSN(dsn)
	if err != nil {
		t.Fatalf("解析 PG_DSN 失败: %v（仅支持 URL 形式 postgres://user:pass@host:port/dbname，不支持键值形式）", err)
	}

	ctx := context.Background()

	// ② 基础连接（不带 Namespace，search_path 保持库默认 → public）：
	// 连通性预检，失败时给出可操作的 docker 提示。
	setPGConfig(t, node)
	if err := g.DB().PingMaster(); err != nil {
		t.Fatalf("PostgreSQL 连接失败（DSN 来自 PG_DSN 环境变量）: %v；可用 docker 快速准备一次性测试库：docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=testdb postgres:16", err)
	}

	// ③ 预置杂表：模拟 public 里既存的无关对象，Fresh 后应随
	// DROP SCHEMA public CASCADE 一并消失。
	if _, err := g.DB().Exec(ctx, "CREATE TABLE public.fresh_junk (id integer)"); err != nil {
		t.Fatalf("在 public 预置杂表 fresh_junk 失败: %v", err)
	}

	// ④ 清理自己建的对象：Fresh 重建出的两张业务表与 goose 版本表
	// （杂表通常已被 Fresh 删掉，一并兜底以防 Fresh 未执行的中断路径）。
	// 只删明确属于本测试的表，不再二次 DROP SCHEMA（Fresh 已清过一次
	// public），更不触碰 public 以外的任何 schema。清理前重设本测试的
	// 配置，防止全局配置已被其他测试改走。
	t.Cleanup(func() {
		setPGConfig(t, node)
		if _, err := g.DB().Exec(ctx,
			"DROP TABLE IF EXISTS public.fresh_junk, public.t1, public.t2, public.goose_db_version CASCADE"); err != nil {
			t.Errorf("清理 Fresh 测试在 public 建的表失败: %v", err)
		}
	})

	return &pgFixture{schema: "public", node: node}
}

// TestMigrateFreshDropsAndRecreates 验证 --fresh 清库重建语义：public 预置
// 杂表后 Fresh，杂表消失（DROP SCHEMA 生效）、两个迁移全量重放（业务表
// 与版本表在 public 重建）、版本表行数 = 2 文件 + 1 基线。引擎构造不注入
// DB 工厂，覆盖 g.DB().Master() 缺省连接路径。
func TestMigrateFreshDropsAndRecreates(t *testing.T) {
	f := setupFreshPG(t)
	ctx := context.Background()

	// 前置自检：杂表确实在 public，后面「已消失」断言才有意义
	if !f.tableExists(t, ctx, "fresh_junk") {
		t.Fatal("预置杂表 fresh_junk 未出现在 public schema，预置失败")
	}

	m, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(
				gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;"))},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(
				gooseSQL("CREATE TABLE t2 (id integer PRIMARY KEY, name text);", "DROP TABLE t2;"))},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}

	if err := m.Fresh(ctx); err != nil {
		t.Fatalf("Fresh 失败: %v", err)
	}

	// 杂表必须随 DROP SCHEMA public CASCADE 消失（清库生效的直接证据）
	if f.tableExists(t, ctx, "fresh_junk") {
		t.Fatal("Fresh 后杂表 fresh_junk 仍存在于 public，DROP SCHEMA 未生效")
	}
	// 两个迁移的业务表 + goose 版本表都应在 public 重建（全量重放）
	for _, table := range []string{"t1", "t2", "goose_db_version"} {
		if !f.tableExists(t, ctx, table) {
			t.Fatalf("Fresh 后 public schema 中未见表 %s（迁移未重放）", table)
		}
	}
	// 版本表行数 = 2 个迁移文件 + 1 条 goose 基线行（version=0，同 Up 测试）
	if got := f.versionRows(t, ctx); got != 3 {
		t.Fatalf("Fresh 后 goose_db_version 记录数 = %d, 期望 3（2 个迁移文件 + 1 条 goose 基线 version=0）", got)
	}
}

// ---------------------------------------------------------------------------
// 粒度与 fresh 交互（change: migrate-integrity-granularity，task 5.2）
// ---------------------------------------------------------------------------

// setupFreshSandboxPG fresh 系新用例的 PG 环境：与 setupFreshPG 同样的
// 安全前提（Fresh 会清空 PG_DSN 指向库的 public schema，必须指向一次性
// 测试库），但不预置杂表——新用例关注粒度门与旁表重录，DROP SCHEMA
// 语义已由既有 TestMigrateFreshDropsAndRecreates 验证，不重复。bizTables
// 为用例 FS 会建的业务表名清单，仅用于 t.Cleanup 兜底清理：Fresh 的
// 第一步就是 DROP SCHEMA public CASCADE（天然清场，无需开头预清），但
// 粒度门报错的失败路径可能残留对账门建的 gf_migrate_state、成功路径会
// 留下业务表与 goose 版本表——统一在 Cleanup 收尾，两次连跑不互相污染。
func setupFreshSandboxPG(t *testing.T, bizTables ...string) *pgFixture {
	t.Helper()

	// ① 环境检查：缺失则跳过。警告同 setupFreshPG——Fresh 会清 public。
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("未设置 PG_DSN，跳过 fresh 粒度集成测试。安全警告：设置后本测试会清空 PG_DSN 指向库的 public schema（DROP SCHEMA public CASCADE，既有数据全部丢失且不可恢复），务必指向专用一次性测试库")
	}

	node, err := parsePGDSN(dsn)
	if err != nil {
		t.Fatalf("解析 PG_DSN 失败: %v（仅支持 URL 形式 postgres://user:pass@host:port/dbname，不支持键值形式）", err)
	}

	ctx := context.Background()

	// ② 基础连接（不带 Namespace，search_path 保持库默认 → public）：
	// 连通性预检。
	setPGConfig(t, node)
	if err := g.DB().PingMaster(); err != nil {
		t.Fatalf("PostgreSQL 连接失败（DSN 来自 PG_DSN 环境变量）: %v", err)
	}

	// ③ 兜底清理：只删本用例可能留下的表（业务表 + 两个版本/旁表），
	// 清理前重设本测试的配置，防止全局配置已被其他测试改走。
	tables := make([]string, 0, len(bizTables)+2)
	for _, tb := range bizTables {
		tables = append(tables, "public."+tb)
	}
	tables = append(tables, "public.goose_db_version", "public.gf_migrate_state")
	t.Cleanup(func() {
		setPGConfig(t, node)
		if _, err := g.DB().Exec(ctx,
			"DROP TABLE IF EXISTS "+strings.Join(tables, ", ")+" CASCADE"); err != nil {
			t.Errorf("清理 fresh 粒度测试在 public 建的表失败: %v", err)
		}
	})

	return &pgFixture{schema: "public", node: node}
}

// TestMigrateGranularityCompliantNewFilePasses 场景 B6：待应用合规新文件
// （单对象多语句）通过。空库 Up：00001 建 users 表、00002 对同一张表
// ALTER 加列 + 建索引（Up 段两条语句、宿主集合恰为 {users}）——文件应
// 正常应用（业务表落地、ALTER 生效），旁表逐文件记录且 checksum 与 FS
// 全文 sha256 一致。
func TestMigrateGranularityCompliantNewFilePasses(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	usersSQL := gooseSQL("CREATE TABLE users (id integer PRIMARY KEY);", "DROP TABLE users;")
	statusSQL := gooseSQL(
		"ALTER TABLE users ADD COLUMN status text;\nCREATE INDEX idx_users_status ON users (status);",
		"DROP INDEX idx_users_status;\nALTER TABLE users DROP COLUMN status;")

	m, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_users.sql":     &fstest.MapFile{Data: []byte(usersSQL)},
			"00002_add_users_status.sql": &fstest.MapFile{Data: []byte(statusSQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m.Up(ctx); err != nil {
		t.Fatalf("合规文件（单对象多语句）Up 应通过, 却返回错误: %v", err)
	}

	// 业务效果：users 表存在且同文件内的 ALTER 生效（status 列在）。
	if !f.tableExists(t, ctx, "users") {
		t.Fatalf("Up 后临时 schema %s 中未见表 users", f.schema)
	}
	v, err := g.DB().GetValue(ctx,
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?",
		f.schema, "users", "status")
	if err != nil {
		t.Fatalf("查询 users 列信息失败: %v", err)
	}
	if v.Int() != 1 {
		t.Fatalf("users 表应有 status 列（同文件 ALTER 语句生效）, 列计数 = %d", v.Int())
	}

	// 旁表：2 条记录，三元组与 FS 全文 sha256 一致。
	rows := f.stateRowsAll(t, ctx)
	if len(rows) != 2 {
		t.Fatalf("旁表 gf_migrate_state 记录数 = %d, 期望 2（与迁移文件数一致）", len(rows))
	}
	assertStateRow(t, rows, 1, "00001_create_users.sql", usersSQL)
	assertStateRow(t, rows, 2, "00002_add_users_status.sql", statusSQL)
	if got := f.versionRows(t, ctx); got != 3 {
		t.Fatalf("goose_db_version 记录数 = %d, 期望 3（2 个迁移文件 + 1 条 goose 基线）", got)
	}
}

// TestMigrateGranularityMultiObjectFileRejected 场景 B7：待应用多对象文件
// 被拒（粒度门在 provider.Up 之前）。空库 FS 仅含一个批次式文件（Up 段
// 两条 CREATE TABLE，宿主集合 {a, b} 为多元素）→ Up 报「迁移文件粒度
// 不合规」，且拦截发生在任何迁移 SQL 执行之前：业务表 a/b 与 goose 版本
// 表都不存在（版本表由 goose provider 建，门在 provider 初始化之前；
// 旁表 gf_migrate_state 是对账门的幂等建表产物，允许存在）。
func TestMigrateGranularityMultiObjectFileRejected(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	batchSQL := gooseSQL(
		"CREATE TABLE a (id integer PRIMARY KEY);\nCREATE TABLE b (id integer PRIMARY KEY);",
		"DROP TABLE a;\nDROP TABLE b;")

	m, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_legacy_batch.sql": &fstest.MapFile{Data: []byte(batchSQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	err = m.Up(ctx)
	if err == nil {
		t.Fatal("批次文件（一文件两表）的 Up 应在粒度门报错, 却返回了 nil")
	}
	if !strings.Contains(err.Error(), "迁移文件粒度不合规") {
		t.Fatalf("Up 错误文案 = %q, 期望含「迁移文件粒度不合规」", err.Error())
	}
	if !strings.Contains(err.Error(), "00001_legacy_batch.sql") {
		t.Fatalf("Up 错误文案 = %q, 期望点名文件 00001_legacy_batch.sql", err.Error())
	}
	// 门在 provider.Up 之前：两张业务表都未建，goose 版本表也未建。
	for _, table := range []string{"a", "b", "goose_db_version"} {
		if f.tableExists(t, ctx, table) {
			t.Fatalf("粒度拒绝时不应执行任何迁移 SQL（临时 schema %s 不应出现表 %s）", f.schema, table)
		}
	}
}

// TestMigrateFreshBatchLegacyRejected 场景 B8：fresh 后批次存量文件在粒度
// 门被拒。FS 含批次文件（一文件两表）执行 Fresh：DROP SCHEMA public
// CASCADE 把 goose 版本表与 gf_migrate_state 旁表一并清掉 → 随后的 Up
// 中全部文件都是「待应用新文件」→ 批次文件在粒度门硬错误（拆分是 fresh
// 重建的前置条件），Fresh 整体返回「迁移文件粒度不合规」且两张表都未建。
func TestMigrateFreshBatchLegacyRejected(t *testing.T) {
	f := setupFreshSandboxPG(t, "a", "b")
	ctx := context.Background()

	batchSQL := gooseSQL(
		"CREATE TABLE a (id integer PRIMARY KEY);\nCREATE TABLE b (id integer PRIMARY KEY);",
		"DROP TABLE a;\nDROP TABLE b;")

	m, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_legacy_batch.sql": &fstest.MapFile{Data: []byte(batchSQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	err = m.Fresh(ctx)
	if err == nil {
		t.Fatal("含批次文件的 Fresh 应在粒度门报错, 却返回了 nil")
	}
	if !strings.Contains(err.Error(), "迁移文件粒度不合规") {
		t.Fatalf("Fresh 错误文案 = %q, 期望含「迁移文件粒度不合规」", err.Error())
	}
	// 重建被拦：批次文件里的两张表都未在 public 落地。
	for _, table := range []string{"a", "b"} {
		if f.tableExists(t, ctx, table) {
			t.Fatalf("粒度拒绝时不应执行任何迁移 SQL（public 不应出现表 %s）", table)
		}
	}
}

// TestMigrateFreshSplitFilesPass 场景 B9：批次拆分后 fresh 通过。把 B8 的
// 批次文件拆成两个单表文件后 Fresh：清库重建成功、两张业务表都在
// public、旁表重录齐全（2 条记录，三元组与 FS 全文 sha256 一致）——
// fresh 是清批次债的路径，代价是存量文件必须先拆分合规。
func TestMigrateFreshSplitFilesPass(t *testing.T) {
	f := setupFreshSandboxPG(t, "a", "b")
	ctx := context.Background()

	fileA := gooseSQL("CREATE TABLE a (id integer PRIMARY KEY);", "DROP TABLE a;")
	fileB := gooseSQL("CREATE TABLE b (id integer PRIMARY KEY);", "DROP TABLE b;")

	m, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_a.sql": &fstest.MapFile{Data: []byte(fileA)},
			"00002_create_b.sql": &fstest.MapFile{Data: []byte(fileB)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m.Fresh(ctx); err != nil {
		t.Fatalf("拆分后的 Fresh 应成功, 却返回错误: %v", err)
	}

	// 两张业务表都在 public（清库重建完成）。
	for _, table := range []string{"a", "b"} {
		if !f.tableExists(t, ctx, table) {
			t.Fatalf("Fresh 后 public schema 中未见表 %s（迁移未重放）", table)
		}
	}
	// 版本表 3 行 = 2 个迁移 + 1 条 goose 基线。
	if got := f.versionRows(t, ctx); got != 3 {
		t.Fatalf("Fresh 后 goose_db_version 记录数 = %d, 期望 3（2 个迁移文件 + 1 条 goose 基线）", got)
	}
	// 旁表重录齐全：2 条记录，三元组与 FS 全文 sha256 一致。
	rows := f.stateRowsAll(t, ctx)
	if len(rows) != 2 {
		t.Fatalf("旁表 gf_migrate_state 记录数 = %d, 期望 2（fresh 后全量重录）", len(rows))
	}
	assertStateRow(t, rows, 1, "00001_create_a.sql", fileA)
	assertStateRow(t, rows, 2, "00002_create_b.sql", fileB)
}

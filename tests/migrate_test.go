//go:build integration

// migrate 引擎 Up 的真 PostgreSQL 集成测试（change: add-migrate-package，task 4.1）。
//
// 被测对象：主库 migrate 包的 Up 执行引擎（仅引擎，不测命令分身——后者由
// 并行任务负责）。重点覆盖 Config.DB 为 nil 的缺省连接路径：引擎内部走
// g.DB().Master()，因此本测试把 PG_DSN 注入 gf 的 default 分组数据库配置
// （gdb.SetConfig），不注入 DB 工厂，让引擎拿默认数据源（与 seqnum 集成
// 测试注入 MYSQL_DSN 的方式同构）。
//
// 测试场景（三个 + 完整性对账五条）：
//  1. Up 应用迁移：2 个合法 goose 迁移文件应用后业务表与版本表
//     （goose_db_version）均落在临时 schema，版本记录数正确，且再次
//     Up 幂等（返回 nil、无重复执行）；
//  2. 二跑零文件：同一 FS 第二次 Up 成功，版本表记录数不变（无待执行文件）；
//  3. 坏 SQL 中途失败：好文件 + 语法错误文件，Up 返回「执行迁移失败」前缀
//     错误，且 errors.Unwrap 逐层可解出底层错误（引擎 %w 包装不吞链；
//     链结构：migrate 错误 → goose PartialError → 驱动错误）；
//     4-8. 完整性对账与基线化（change: migrate-integrity-granularity，task
//     5.1，函数名统一 TestMigrateIntegrity 前缀，-run 'TestMigrateIntegrity'
//     可单独跑齐）：存量基线化补录与增量并存 / 基线化判定下缺文件仍拒 /
//     改文件内容 R1 拒 / 改名（版本号不变）R2 拒 / 空库首跑正常应用并记录
//     旁表。旁表 gf_migrate_state 的断言直接经 g.DB() 查表比对三元组
//     （version_id / filename / checksum），基线化是否发生以旁表补录
//     行为断言代替日志断言（现有测试无拦截 g.Log 的模式）。
//
// 环境要求（连接信息全部经环境变量注入，不硬编码凭证）：
//   - PG_DSN（必填，缺失则 Skip）：仅支持 URL 形式，
//     如 postgres://user:pass@127.0.0.1:5432/dbname（键值形式 host=...
//     user=... 不支持，给定时测试直接报错并提示格式要求）；
//   - 连接固定 sslmode=disable（gf pgsql 驱动的既有行为），DSN URL 中的
//     query 参数（含 sslmode）会被忽略。
//
// schema 隔离方案：每个测试在目标库上建独立临时 schema
// （gf_lib_migrate_test_<8位随机hex>），经 gf 配置节点的 Namespace 字段
// （pgsql 驱动将其映射为 lib/pq 的 search_path 连接参数）把本测试全部连接
// 限制在临时 schema 内——goose 版本表与迁移建的业务表都不触碰库中既有
// schema（尤其 public）；t.Cleanup 时 DROP SCHEMA ... CASCADE。建临时
// schema 用的是不带 Namespace 的基础连接（search_path 保持默认），仅执行
// CREATE SCHEMA，不对 public 做任何 DDL。
//
// 运行方式（见 doc.go 包注释）：
//
//	cd tests
//	PG_DSN='postgres://user:pass@127.0.0.1:5432/dbname' \
//	  GOWORK=off go test -tags integration ./... -run Migrate -v
package tests

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	// 注册 gf 的 pgsql 驱动：引擎缺省路径 g.DB().Master() 依赖 gdb 驱动
	// 注册表，测试进程需自行 import 注册（同本目录 seqnum 测试对 mysql
	// 驱动的处理方式）。
	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/migrate"
)

// pgFixture 单个测试的 PostgreSQL 环境：基础连接节点 + 独立临时 schema。
type pgFixture struct {
	schema string         // 本测试专属临时 schema 名
	node   gdb.ConfigNode // DSN 解析出的基础节点（Namespace 按需切换）
}

// setupPG 准备 PG 环境：解析 PG_DSN（缺失则 Skip）、连通性预检、建独立
// 临时 schema 并把 gf default 分组配置切到该 schema，注册 t.Cleanup 清理。
//
// 返回的 fixture 上，后续 g.DB() 的所有连接（含引擎经 g.DB().Master()
// 取到的连接）search_path 均指向临时 schema，天然与库中既有数据隔离。
func setupPG(t *testing.T) *pgFixture {
	t.Helper()

	// ① 环境检查：PG 连接串必填，缺失则跳过（不阻塞无环境的机器与 CI）
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("未设置 PG_DSN（仅支持 URL 形式，如 postgres://用户:密码@127.0.0.1:5432/库名），跳过集成测试；运行方式见 doc.go 包注释")
	}

	node, err := parsePGDSN(dsn)
	if err != nil {
		t.Fatalf("解析 PG_DSN 失败: %v（仅支持 URL 形式 postgres://user:pass@host:port/dbname，不支持键值形式）", err)
	}

	ctx := context.Background()

	// ② 基础连接（不带 Namespace，search_path 保持库默认）：连通性预检。
	// 失败时给出可操作的提示（docker 一键命令），而非驱动深处的模糊报错。
	setPGConfig(t, node)
	if err := g.DB().PingMaster(); err != nil {
		t.Fatalf("PostgreSQL 连接失败（DSN 来自 PG_DSN 环境变量）: %v；"+
			"可用 docker 快速准备：docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=testdb postgres:16", err)
	}

	// ③ 建独立临时 schema：仅 CREATE SCHEMA 一条语句，不动 public 等既有
	// schema；随机后缀保证并发/重复执行不冲突。
	suffix := make([]byte, 4)
	if _, err := crand.Read(suffix); err != nil {
		t.Fatalf("生成临时 schema 随机后缀失败: %v", err)
	}
	schema := "gf_lib_migrate_test_" + hex.EncodeToString(suffix)
	if _, err := g.DB().Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("建临时 schema %s 失败: %v", schema, err)
	}

	// ④ 切到临时 schema：此后本测试经 g.DB() 的全部连接（含引擎缺省路径
	// g.DB().Master()）search_path 均指向临时 schema。
	node.Namespace = schema
	setPGConfig(t, node)

	// ⑤ 幂等清理：DROP SCHEMA CASCADE 一并带走版本表与业务表。
	// 清理前重设本测试的配置，防止全局配置已被其他测试改走。
	t.Cleanup(func() {
		node.Namespace = schema
		setPGConfig(t, node)
		if _, err := g.DB().Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("清理临时 schema %s 失败: %v", schema, err)
		}
	})

	return &pgFixture{schema: schema, node: node}
}

// parsePGDSN 解析 URL 形式的 PG 连接串为 gf 配置节点字段。
// 不支持键值形式（host=... user=...），由调用方负责提示格式要求。
func parsePGDSN(dsn string) (gdb.ConfigNode, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return gdb.ConfigNode{}, fmt.Errorf("URL 解析失败: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return gdb.ConfigNode{}, fmt.Errorf("scheme 为 %q，期望 postgres:// 或 postgresql://（URL query 参数含 sslmode 会被忽略，驱动固定 sslmode=disable）", u.Scheme)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return gdb.ConfigNode{}, errors.New("URL 缺少库名（path 部分），期望 postgres://user:pass@host:port/dbname")
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	pass, _ := u.User.Password()
	return gdb.ConfigNode{
		Type: "pgsql",
		User: u.User.Username(),
		Pass: pass,
		Host: u.Hostname(),
		Port: port,
		Name: name,
	}, nil
}

// setPGConfig 注入 g.DB() 的 default 分组配置（gins 首次 g.DB() 前生效，
// 后续 Master() 每次动态读取全局配置，换 Namespace 即换连接池）。
func setPGConfig(t *testing.T, node gdb.ConfigNode) {
	t.Helper()
	if err := gdb.SetConfig(gdb.Config{
		gdb.DefaultGroupName: gdb.ConfigGroup{node},
	}); err != nil {
		t.Fatalf("注入 gdb 配置失败: %v", err)
	}
}

// gooseSQL 构造一份合法的 goose SQL 迁移文件内容（Up + Down 注记成对）。
func gooseSQL(up, down string) string {
	return "-- +goose Up\n" + up + "\n-- +goose Down\n" + down + "\n"
}

// tableExists 断言辅助：查 information_schema（限定本测试临时 schema），
// 不依赖 search_path，避免连接形态影响判断。
func (f *pgFixture) tableExists(t *testing.T, ctx context.Context, table string) bool {
	t.Helper()
	v, err := g.DB().GetValue(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		f.schema, table)
	if err != nil {
		t.Fatalf("查询 information_schema 失败: %v", err)
	}
	return v.Int() == 1
}

// versionRows 查临时 schema 内 goose 版本表（goose_db_version）的记录数，
// 间接验证「应用了几个迁移」「二跑未重复应用」。
//
// 注意 goose 的既有行为：首次建版本表时会插入一条 version=0 的基线行
// （见 goose provider 的 tryEnsureVersionTable → Insert(version 0)），
// 因此 N 个迁移文件对应 N+1 行。
func (f *pgFixture) versionRows(t *testing.T, ctx context.Context) int {
	t.Helper()
	v, err := g.DB().GetValue(ctx, "SELECT COUNT(*) FROM goose_db_version")
	if err != nil {
		t.Fatalf("查询 goose_db_version 失败: %v", err)
	}
	return v.Int()
}

// TestMigrateUpAppliesMigrations 场景 1：Up 应用迁移。
// 两个合法迁移文件应用后两张业务表都建在临时 schema（goose 版本表同在），
// 版本记录数 = 文件数；随后再次 Up 返回 nil 且不产生新记录（幂等）。
// 引擎构造不注入 DB 工厂，覆盖 g.DB().Master() 缺省连接路径。
func TestMigrateUpAppliesMigrations(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

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

	// 第一次 Up：应成功应用 2 个文件
	if err := m.Up(ctx); err != nil {
		t.Fatalf("第一次 Up 失败: %v", err)
	}

	// 业务表与版本表必须都在本测试的临时 schema（隔离验证）
	for _, table := range []string{"t1", "t2", "goose_db_version"} {
		if !f.tableExists(t, ctx, table) {
			t.Fatalf("Up 后临时 schema %s 中未见表 %s", f.schema, table)
		}
	}
	if got := f.versionRows(t, ctx); got != 3 {
		t.Fatalf("goose_db_version 记录数 = %d, 期望 3（2 个迁移文件 + 1 条 goose 基线 version=0）", got)
	}

	// 再次 Up：幂等，返回 nil 且不新增版本记录
	if err := m.Up(ctx); err != nil {
		t.Fatalf("第二次 Up 应幂等成功, 却返回错误: %v", err)
	}
	if got := f.versionRows(t, ctx); got != 3 {
		t.Fatalf("第二次 Up 后 goose_db_version 记录数 = %d, 期望仍为 3（不重复应用）", got)
	}
}

// TestMigrateUpSecondRunZeroPending 场景 2：二跑零文件幂等。
// 同一 FS 第二次 Up 成功（版本表已记录全部版本，无待执行文件），
// 版本记录数不变。
func TestMigrateUpSecondRunZeroPending(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	m, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(
				gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;"))},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}

	if err := m.Up(ctx); err != nil {
		t.Fatalf("第一次 Up 失败: %v", err)
	}
	if got := f.versionRows(t, ctx); got != 2 {
		t.Fatalf("第一次 Up 后 goose_db_version 记录数 = %d, 期望 2（1 个迁移文件 + 1 条 goose 基线）", got)
	}

	// 二跑：版本已全部记录，Up 应零文件成功返回
	if err := m.Up(ctx); err != nil {
		t.Fatalf("二跑（零待执行文件）Up 应成功, 却返回错误: %v", err)
	}
	if got := f.versionRows(t, ctx); got != 2 {
		t.Fatalf("二跑后 goose_db_version 记录数 = %d, 期望仍为 2（零文件执行）", got)
	}
}

// TestMigrateUpBadSQLFailsWithErrorChain 场景 3：坏 SQL 中途失败。
// 好文件（00001）+ 语义错误文件（00002，goose 注记合法、SQL 语法本身
// 合法但引用不存在的类型——粒度校验门只做纯语法解析，未知类型名是
// 执行期错误，确保错误发生在 provider 执行期而非收集期/粒度门）：Up
// 必须报错，文案带「执行迁移失败」前缀，且 errors.Unwrap 链逐层可解
// ——第一层是 goose 的 PartialError，第二层是底层驱动错误（spec 要求
// %w 不吞链；测试不 import goose，只按链深度断言）。
//
// 注：粒度门引入（change: migrate-integrity-granularity）后，纯语法
// 错误（如 CREATE TABLEX）会在 provider.Up 之前被「迁移文件解析失败」
// 拦截、触达不了本用例要验证的执行期错误链路径，故坏 SQL 选用语义
// 错误形态。
func TestMigrateUpBadSQLFailsWithErrorChain(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	m, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(
				gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;"))},
			// 语义错误：语法合法（粒度门解析通过、宿主单对象 t_bad 合规），
			// 执行期 PG 报 type 不存在
			"00002_bad_syntax.sql": &fstest.MapFile{Data: []byte(
				gooseSQL("CREATE TABLE t_bad (id nosuchtype);", "DROP TABLE t_bad;"))},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}

	err = m.Up(ctx)
	if err == nil {
		t.Fatal("坏 SQL 迁移的 Up 应返回错误, 却返回了 nil")
	}
	if !strings.HasPrefix(err.Error(), "执行迁移失败") {
		t.Fatalf("Up 错误文案 = %q, 期望以「执行迁移失败」开头（%%w 包装的中文前缀）", err.Error())
	}
	// 链第一层：goose PartialError（部分迁移已应用、含失败详情）
	err1 := errors.Unwrap(err)
	if err1 == nil {
		t.Fatalf("Up 错误 %q 的 Unwrap 链断裂（第一层为 nil, 期望 goose 的部分迁移错误）", err.Error())
	}
	// 链第二层：底层驱动错误（PG 的 syntax error）
	err2 := errors.Unwrap(err1)
	if err2 == nil {
		t.Fatalf("错误链未通到底层（第二层为 nil, 期望 PG 驱动错误）: %v", err1)
	}
	// 补充验证：失败前的 00001 已成功应用（PartialError 语义——中途失败而非全部回滚）
	if !f.tableExists(t, ctx, "t1") {
		t.Fatalf("坏 SQL 场景下首个好迁移应已应用（临时 schema %s 应有表 t1）", f.schema)
	}
}

// ---------------------------------------------------------------------------
// 完整性对账与基线化（change: migrate-integrity-granularity，task 5.1）
// ---------------------------------------------------------------------------

// migStateRow gf_migrate_state 旁表一行记录的测试视图（version_id /
// filename / checksum 三元组，对账断言的比对基准）。
type migStateRow struct {
	Version  int64
	Filename string
	Checksum string
}

// stateRowsAll 查询本测试 schema 内 gf_migrate_state 旁表全部记录，按
// 版本号索引返回。TestMigrateIntegrity 系列的旁表断言（补录是否发生、
// 记录是否齐全、checksum 是否与 FS 一致）全部经此。连接走 g.DB() 与
// 引擎同一路径（A 组用例的 search_path 即临时 schema）。
func (f *pgFixture) stateRowsAll(t *testing.T, ctx context.Context) map[int64]migStateRow {
	t.Helper()
	all, err := g.DB().GetAll(ctx,
		"SELECT version_id, filename, checksum FROM gf_migrate_state")
	if err != nil {
		t.Fatalf("查询 gf_migrate_state 失败: %v", err)
	}
	rows := make(map[int64]migStateRow, len(all))
	for _, r := range all {
		v := r["version_id"].Int64()
		rows[v] = migStateRow{
			Version:  v,
			Filename: r["filename"].String(),
			Checksum: r["checksum"].String(),
		}
	}
	return rows
}

// fileChecksum 计算迁移文件全文原文的 sha256 hex（64 字符小写十六进制），
// 与旁表 checksum 列同口径：含 goose 注解行在内的文件字节内容（对齐
// 主库 state.go 的 checksumHex）。测试侧对 FS 内容自算期望值与旁表
// 落库值比对，证明「记录的就是 FS 当前内容的 hash」。
func fileChecksum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// assertStateRow 断言旁表中某版本记录的 filename 与 checksum 均与期望
// 一致（期望 checksum 由该文件全文自算）。任一不符即 Fatal。
func assertStateRow(t *testing.T, rows map[int64]migStateRow, version int64, filename, content string) {
	t.Helper()
	row, ok := rows[version]
	if !ok {
		t.Fatalf("旁表 gf_migrate_state 缺版本 %d 的记录（现共 %d 条）", version, len(rows))
	}
	if row.Filename != filename {
		t.Fatalf("旁表版本 %d 的 filename = %q, 期望 %q", version, row.Filename, filename)
	}
	if want := fileChecksum(content); row.Checksum != want {
		t.Fatalf("旁表版本 %d 的 checksum = %q, 期望 %q（文件全文 sha256）", version, row.Checksum, want)
	}
}

// TestMigrateIntegrityBaselineBackfillWithIncrement 场景 A1：存量基线化
// 补录与增量迁移并存。模拟存量项目升级路径：先 Up 应用 2 个文件 → 手工
// DELETE 旁表（模拟升级到新版本时旁表为空——旧版本无旁表机制）→ FS
// 增量加 00003 新文件 → 再 Up 必须无报错地完成三件事：按 FS 当前内容
// 自动补录 1/2 的旁表记录（基线化，不解析存量文件粒度）、正常应用增量
// 00003、应用后写 3 号旁表记录。旁表最终 3 条且三元组全部与 FS 一致——
// 补录发生即基线化发生（旁表行为断言代替日志断言）。
func TestMigrateIntegrityBaselineBackfillWithIncrement(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	// 文件内容在前后两个 Migrator 间逐字复用同一变量：checksum 断言基准
	// 与基线化补录来源都是 FS 当前内容，两处必须一致。
	t1SQL := gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;")
	t2SQL := gooseSQL("CREATE TABLE t2 (id integer PRIMARY KEY, name text);", "DROP TABLE t2;")
	t3SQL := gooseSQL("CREATE TABLE t3 (id integer PRIMARY KEY);", "DROP TABLE t3;")

	// 第一步：先跑一次 Up 应用 2 个文件（goose 版本表落 1、2，旁表也落
	// 1、2——随后清空模拟存量形态）。
	m1, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(t2SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m1.Up(ctx); err != nil {
		t.Fatalf("前置 Up（应用 2 个文件）失败: %v", err)
	}

	// 模拟旁表空（存量项目升级 / 上次应用成功但旁表写入前中断，两种成因
	// 同一对账路径）。
	if _, err := g.DB().Exec(ctx, "DELETE FROM gf_migrate_state"); err != nil {
		t.Fatalf("清空旁表 gf_migrate_state 失败: %v", err)
	}
	if got := len(f.stateRowsAll(t, ctx)); got != 0 {
		t.Fatalf("前置自检失败：旁表记录数 = %d, 期望 0", got)
	}

	// 第二步：升级后首次 Up——旁表为空 + 版本表非空触发基线化判定，FS
	// 同时多了增量 00003。
	m2, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(t2SQL)},
			"00003_create_t3.sql": &fstest.MapFile{Data: []byte(t3SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m2.Up(ctx); err != nil {
		t.Fatalf("基线化 + 增量 Up 应无报错, 却返回错误: %v", err)
	}

	// 增量已应用：00003 的业务表落地。
	if !f.tableExists(t, ctx, "t3") {
		t.Fatalf("基线化 Up 后临时 schema %s 中未见表 t3（增量未应用）", f.schema)
	}
	// 旁表 3 条：1/2 为基线化补录、3 为应用后写入，三元组全部与 FS 一致。
	rows := f.stateRowsAll(t, ctx)
	if len(rows) != 3 {
		t.Fatalf("旁表 gf_migrate_state 记录数 = %d, 期望 3（2 条基线化补录 + 1 条应用后记录）", len(rows))
	}
	assertStateRow(t, rows, 1, "00001_create_t1.sql", t1SQL)
	assertStateRow(t, rows, 2, "00002_create_t2.sql", t2SQL)
	assertStateRow(t, rows, 3, "00003_create_t3.sql", t3SQL)
	// 版本表 4 行 = 3 个迁移 + 1 条 goose 基线（version=0）。
	if got := f.versionRows(t, ctx); got != 4 {
		t.Fatalf("goose_db_version 记录数 = %d, 期望 4（3 个迁移文件 + 1 条 goose 基线）", got)
	}
}

// TestMigrateIntegrityMissingFileRejectedUnderBaseline 场景 A2：基线化判定
// 下已应用文件缺失仍拒绝。版本表已应用 1/2、旁表为空（基线化判定成立），
// 但 FS 删掉了 00002 的文件——spec 要求即使处于基线化判定下也按完整性
// 对账的缺失错误拒绝（锁定一个已知缺文件的状态没有意义）。FS 同时含
// 待应用新文件 00003，验证硬错误拦在任何写库之前：00003 不被执行、
// 旁表保持为空（补录也未发生）。
func TestMigrateIntegrityMissingFileRejectedUnderBaseline(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	t1SQL := gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;")
	t2SQL := gooseSQL("CREATE TABLE t2 (id integer PRIMARY KEY, name text);", "DROP TABLE t2;")
	t3SQL := gooseSQL("CREATE TABLE t3 (id integer PRIMARY KEY);", "DROP TABLE t3;")

	m1, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(t2SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m1.Up(ctx); err != nil {
		t.Fatalf("前置 Up（应用 2 个文件）失败: %v", err)
	}
	if _, err := g.DB().Exec(ctx, "DELETE FROM gf_migrate_state"); err != nil {
		t.Fatalf("清空旁表 gf_migrate_state 失败: %v", err)
	}

	// FS 删掉 00002（版本表已应用版本 2 无对应文件），另含待应用新文件
	// 00003——即使有增量待执行也必须先被 R2 拦下。
	m2, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00003_create_t3.sql": &fstest.MapFile{Data: []byte(t3SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	err = m2.Up(ctx)
	if err == nil {
		t.Fatal("基线化判定下缺已应用文件的 Up 应返回错误, 却返回了 nil")
	}
	if !strings.Contains(err.Error(), "已应用的迁移文件缺失") {
		t.Fatalf("Up 错误文案 = %q, 期望含「已应用的迁移文件缺失」", err.Error())
	}
	// 旁表已被清空（基线化判定），缺失文件名无处可取、退回版本号——
	// 精确断言缺失的是版本 2，防止其它版本的报错顶替本用例语义。
	if !strings.Contains(err.Error(), "已应用的迁移文件缺失: 2") {
		t.Fatalf("Up 错误文案 = %q, 期望含「已应用的迁移文件缺失: 2」（旁表无记录时退回版本号）", err.Error())
	}
	// 拦在任何写库之前：待应用文件未执行（t3 未建），旁表无补录。
	if f.tableExists(t, ctx, "t3") {
		t.Fatalf("R2 拒绝时不应执行任何迁移 SQL（临时 schema %s 不应出现 t3）", f.schema)
	}
	if got := len(f.stateRowsAll(t, ctx)); got != 0 {
		t.Fatalf("R2 拒绝时旁表不应有补录（记录数 = %d, 期望 0）", got)
	}
}

// TestMigrateIntegrityModifiedFileRejected 场景 A3：已应用文件被原地修改
// （R1）。Up 一次成功后在 FS 内替换 00001 的内容（同文件名、不同字节），
// 再 Up 必须报「已应用的迁移文件被修改」并点名该文件——checksum 对账
// 锚定文件全文，修改已应用文件是硬错误，无任何关闭开关。
func TestMigrateIntegrityModifiedFileRejected(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	t1SQL := gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;")
	t2SQL := gooseSQL("CREATE TABLE t2 (id integer PRIMARY KEY, name text);", "DROP TABLE t2;")

	m1, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(t2SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m1.Up(ctx); err != nil {
		t.Fatalf("前置 Up（应用 2 个文件）失败: %v", err)
	}

	// 00001 内容被原地修改（本身仍是合法 SQL——R1 在对账期拦截，与内容
	// 是否可执行无关）。
	t1Modified := gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY, note text);", "DROP TABLE t1;")
	m2, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1Modified)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(t2SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	err = m2.Up(ctx)
	if err == nil {
		t.Fatal("改已应用文件内容后的 Up 应返回错误, 却返回了 nil")
	}
	if !strings.Contains(err.Error(), "已应用的迁移文件被修改") {
		t.Fatalf("Up 错误文案 = %q, 期望含「已应用的迁移文件被修改」", err.Error())
	}
	if !strings.Contains(err.Error(), "00001_create_t1.sql") {
		t.Fatalf("Up 错误文案 = %q, 期望点名被修改的文件 00001_create_t1.sql", err.Error())
	}
	// R1 拒绝时旁表原记录保持不动（对账先出结论、后写库）：仍 2 条，
	// 且 1 号记录的 checksum 仍是应用时（未修改前）内容的 hash。
	rows := f.stateRowsAll(t, ctx)
	if len(rows) != 2 {
		t.Fatalf("R1 拒绝时旁表记录数 = %d, 期望 2（原记录不动）", len(rows))
	}
	assertStateRow(t, rows, 1, "00001_create_t1.sql", t1SQL)
}

// TestMigrateIntegrityRenamedFileRejected 场景 A4：已应用文件仅改描述被
// 重命名（R2，版本号不变）。Up 一次成功后把 00003_create_t3.sql 换名为
// 00003_rename_t3.sql（版本号 3 不变、内容不变），再 Up 必须报「已应用的
// 迁移文件缺失」且点名旁表记录的旧文件名——旧名才是「已应用却已不在
// FS」的那个文件。
func TestMigrateIntegrityRenamedFileRejected(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	t1SQL := gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;")
	t2SQL := gooseSQL("CREATE TABLE t2 (id integer PRIMARY KEY, name text);", "DROP TABLE t2;")
	t3SQL := gooseSQL("CREATE TABLE t3 (id integer PRIMARY KEY);", "DROP TABLE t3;")

	m1, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(t2SQL)},
			"00003_create_t3.sql": &fstest.MapFile{Data: []byte(t3SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m1.Up(ctx); err != nil {
		t.Fatalf("前置 Up（应用 3 个文件）失败: %v", err)
	}

	// 同版本号改名（内容不变）：旁表记录的文件名与 FS 当前文件名不符。
	m2, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(t2SQL)},
			"00003_rename_t3.sql": &fstest.MapFile{Data: []byte(t3SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	err = m2.Up(ctx)
	if err == nil {
		t.Fatal("改已应用文件名后的 Up 应返回错误, 却返回了 nil")
	}
	if !strings.Contains(err.Error(), "已应用的迁移文件缺失") {
		t.Fatalf("Up 错误文案 = %q, 期望含「已应用的迁移文件缺失」", err.Error())
	}
	// 必须报旁表记录的旧文件名（最后已知名），而非 FS 当前新名。
	if !strings.Contains(err.Error(), "00003_create_t3.sql") {
		t.Fatalf("Up 错误文案 = %q, 期望点名旁表旧文件名 00003_create_t3.sql", err.Error())
	}
	if strings.Contains(err.Error(), "00003_rename_t3.sql") {
		t.Fatalf("Up 错误文案 = %q, 不应报 FS 当前新文件名（应报旁表旧名）", err.Error())
	}
	// R2 拒绝时旁表原记录保持不动：仍 3 条，3 号记录的文件名仍是旧名。
	rows := f.stateRowsAll(t, ctx)
	if len(rows) != 3 {
		t.Fatalf("R2 拒绝时旁表记录数 = %d, 期望 3（原记录不动）", len(rows))
	}
	assertStateRow(t, rows, 3, "00003_create_t3.sql", t3SQL)
}

// TestMigrateIntegrityBaselineWithBatchLegacy 场景 A6：批次存量文件基线化
// 后照常运行且被锁死。这是真实存量项目的升级路径实证（CHANGELOG 的
// BREAKING 升级影响条款）：模拟「旧版本（无粒度门时代）已应用批次文件
// （一文件两表）」的库——版本表已应用、旁表为空（升级前无旁表机制），
// FS 含该批次文件 → 基线化以 FS 当前内容补录、不解析批次文件粒度、
// Up 零文件成功返回；随后批次文件被原地修改 → R1 硬错误（批次文件自此
// 锁死，拆分唯一出口 --fresh）。
//
// 构造全程真实路径：先以合规单表 FS 跑 Up 让 goose 落版本表，清空旁表
// 后把版本 2 的 FS 文件替换为批次形态（旁表无记录时补录以 FS 当前内容
// 为准，不比文件名）——不手工伪造 goose 版本表 DDL。
func TestMigrateIntegrityBaselineWithBatchLegacy(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	t1SQL := gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;")
	// 批次形态：一文件两表（旧版本无粒度门时代合法应用的存量）。
	batchSQL := gooseSQL(
		"CREATE TABLE t2 (id integer PRIMARY KEY);\nCREATE TABLE t3 (id integer PRIMARY KEY);",
		"DROP TABLE t2;\nDROP TABLE t3;")

	// 第一步：合规单表 FS 应用版本 1、2（goose 建版本表）。
	m1, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(
				gooseSQL("CREATE TABLE t2 (id integer PRIMARY KEY);", "DROP TABLE t2;"))},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m1.Up(ctx); err != nil {
		t.Fatalf("前置 Up（应用 2 个文件）失败: %v", err)
	}

	// 第二步：清旁表（模拟升级到新版本时旁表为空），同时把版本 2 的文件
	// 换成批次形态——数据库里 v2 已应用，旁表无记录。
	if _, err := g.DB().Exec(ctx, "DELETE FROM gf_migrate_state"); err != nil {
		t.Fatalf("清空旁表 gf_migrate_state 失败: %v", err)
	}
	m2, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql":   &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_batch_t2_t3.sql": &fstest.MapFile{Data: []byte(batchSQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	// 基线化 Up：批次文件已应用（v2 ∈ 版本表）不进粒度门，零待应用文件，
	// 必须无报错返回——「存量批次文件基线化后照常跑」的直接实证。
	if err := m2.Up(ctx); err != nil {
		t.Fatalf("批次存量的基线化 Up 应照常成功, 却返回错误: %v", err)
	}
	rows := f.stateRowsAll(t, ctx)
	if len(rows) != 2 {
		t.Fatalf("旁表 gf_migrate_state 记录数 = %d, 期望 2（基线化补录）", len(rows))
	}
	// v2 补录的是批次文件当前内容（含其批次形态 checksum）。
	assertStateRow(t, rows, 2, "00002_batch_t2_t3.sql", batchSQL)

	// 第三步：批次文件被原地修改 → R1 硬错误（批次文件自此锁死）。
	batchModified := gooseSQL(
		"CREATE TABLE t2 (id integer PRIMARY KEY, note text);\nCREATE TABLE t3 (id integer PRIMARY KEY);",
		"DROP TABLE t2;\nDROP TABLE t3;")
	m3, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql":   &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_batch_t2_t3.sql": &fstest.MapFile{Data: []byte(batchModified)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	err = m3.Up(ctx)
	if err == nil {
		t.Fatal("修改已应用批次文件后的 Up 应报 R1 错误, 却返回了 nil")
	}
	if !strings.Contains(err.Error(), "已应用的迁移文件被修改") {
		t.Fatalf("Up 错误文案 = %q, 期望含「已应用的迁移文件被修改」", err.Error())
	}
}

// TestMigrateIntegrityEmptyDBNoBaseline 场景 A5：空库首跑不触发基线化。
// 全新临时 schema（goose 版本表与旁表均无记录）直接 Up 全部文件：基线化
// 的前提「版本表非空」不成立，全部文件按待应用新文件正常应用、应用成功
// 后逐文件写旁表。行为上以旁表断言验证正常记录路径：记录数 = 文件数、
// 三元组与 FS 全文 sha256 一致。
func TestMigrateIntegrityEmptyDBNoBaseline(t *testing.T) {
	f := setupPG(t)
	ctx := context.Background()

	t1SQL := gooseSQL("CREATE TABLE t1 (id integer PRIMARY KEY);", "DROP TABLE t1;")
	t2SQL := gooseSQL("CREATE TABLE t2 (id integer PRIMARY KEY, name text);", "DROP TABLE t2;")

	m, err := migrate.New(migrate.Config{
		FS: fstest.MapFS{
			"00001_create_t1.sql": &fstest.MapFile{Data: []byte(t1SQL)},
			"00002_create_t2.sql": &fstest.MapFile{Data: []byte(t2SQL)},
		},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("构造 Migrator 失败: %v", err)
	}
	if err := m.Up(ctx); err != nil {
		t.Fatalf("空库首跑 Up 应成功, 却返回错误: %v", err)
	}

	// 旁表记录数 = 文件数（正常应用后逐文件写入，无基线化补录参与），
	// checksum 与 FS 全文 sha256 一致。
	rows := f.stateRowsAll(t, ctx)
	if len(rows) != 2 {
		t.Fatalf("旁表 gf_migrate_state 记录数 = %d, 期望 2（与迁移文件数一致）", len(rows))
	}
	assertStateRow(t, rows, 1, "00001_create_t1.sql", t1SQL)
	assertStateRow(t, rows, 2, "00002_create_t2.sql", t2SQL)
	if got := f.versionRows(t, ctx); got != 3 {
		t.Fatalf("goose_db_version 记录数 = %d, 期望 3（2 个迁移文件 + 1 条 goose 基线）", got)
	}
}

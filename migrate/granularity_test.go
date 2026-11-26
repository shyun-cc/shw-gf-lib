// 粒度判定的同包内部测试（package migrate）：parseGooseFile /
// validateGranularity 均为未导出函数（Up 接线在包内完成），外部测试
// 包无法触达。全部用例以真实 SQL 文本经 pg_query 全链路解析驱动，
// 不手工构造 AST——既贴近真实迁移文件，也顺带验证 pg_query 对各语句
// 形态的实际产出。不触真库。
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseGooseFile(t *testing.T) {
	t.Run("Up段截取至Down段且丢弃Down", func(t *testing.T) {
		content := "-- +goose Up\n" +
			"CREATE TABLE users (id uuid PRIMARY KEY);\n" +
			"\n" +
			"-- +goose Down\n" +
			"DROP TABLE orders;\n"
		upSQL, _ := parseGooseFile(content)
		// 行 3 的空行位于 Down 注解前、属 Up 段，保留为一个 \n；
		// Down 注解行本身及其后内容全部丢弃。
		want := "CREATE TABLE users (id uuid PRIMARY KEY);\n"
		if upSQL != want {
			t.Fatalf("Up 段截取不符:\n got: %q\nwant: %q", upSQL, want)
		}
	})

	t.Run("无Down段截取到文件尾", func(t *testing.T) {
		content := "-- +goose Up\nCREATE TABLE users (id uuid PRIMARY KEY);\n"
		upSQL, _ := parseGooseFile(content)
		if want := "CREATE TABLE users (id uuid PRIMARY KEY);\n"; upSQL != want {
			t.Fatalf("Up 段截取不符:\n got: %q\nwant: %q", upSQL, want)
		}
	})

	t.Run("注解夹在语句中间被剥离", func(t *testing.T) {
		content := "-- +goose Up\n" +
			"CREATE FUNCTION public.fn() RETURNS int\n" +
			"-- +goose StatementBegin\n" +
			"AS $$ SELECT 1 $$;\n" +
			"-- +goose StatementEnd\n" +
			"\n" +
			"-- +goose Down\n" +
			"DROP FUNCTION public.fn();\n"
		upSQL, _ := parseGooseFile(content)
		want := "CREATE FUNCTION public.fn() RETURNS int\n" +
			"AS $$ SELECT 1 $$;\n"
		if upSQL != want {
			t.Fatalf("注解剥离不符:\n got: %q\nwant: %q", upSQL, want)
		}
	})

	t.Run("Up段前的注解行不进Up段", func(t *testing.T) {
		content := "-- +goose NO TRANSACTION\n" +
			"-- +goose Up\n" +
			"SELECT 1;\n"
		upSQL, _ := parseGooseFile(content)
		if want := "SELECT 1;\n"; upSQL != want {
			t.Fatalf("Up 段截取不符:\n got: %q\nwant: %q", upSQL, want)
		}
	})

	t.Run("无Up注解返回空Up段", func(t *testing.T) {
		upSQL, _ := parseGooseFile("SELECT 1;\n")
		if upSQL != "" {
			t.Fatalf("无 Up 注解时 Up 段应为空, got %q", upSQL)
		}
	})

	t.Run("checksum为含注解原文全文sha256", func(t *testing.T) {
		content := "-- +goose NO TRANSACTION\n" +
			"-- +goose Up\n" +
			"CREATE TABLE users (id uuid PRIMARY KEY);\n" +
			"-- +goose Down\n" +
			"DROP TABLE users;\n"
		_, checksum := parseGooseFile(content)
		sum := sha256.Sum256([]byte(content))
		want := hex.EncodeToString(sum[:])
		if checksum != want {
			t.Fatalf("校验和不符:\n got: %s\nwant: %s", checksum, want)
		}
		if len(checksum) != 64 {
			t.Fatalf("校验和应为 64 字符 hex, got %d 字符: %s", len(checksum), checksum)
		}
	})
}

func TestGooseAnnotationCommand(t *testing.T) {
	cases := []struct {
		line      string
		wantCmd   string
		wantIsAnn bool
	}{
		{line: "-- +goose Up", wantCmd: "up", wantIsAnn: true},
		{line: "  -- +goose UP  ", wantCmd: "up", wantIsAnn: true},
		{line: "-- +goose Down", wantCmd: "down", wantIsAnn: true},
		{line: "-- +goose StatementBegin", wantCmd: "statementbegin", wantIsAnn: true},
		{line: "-- +goose NO TRANSACTION", wantCmd: "no", wantIsAnn: true},
		{line: "SELECT 1;", wantIsAnn: false},
		{line: "-- 普通注释", wantIsAnn: false},
		{line: "-- +goose", wantCmd: "", wantIsAnn: true},
		// 粘连写法（无空白分隔）不是注解，不得误判为 Up/Down：
		{line: "-- +gooseUp", wantIsAnn: false},
		{line: "-- +gooseDown", wantIsAnn: false},
	}
	for _, c := range cases {
		cmd, isAnn := gooseAnnotationCommand(c.line)
		if cmd != c.wantCmd || isAnn != c.wantIsAnn {
			t.Errorf("gooseAnnotationCommand(%q) = (%q, %v), want (%q, %v)",
				c.line, cmd, isAnn, c.wantCmd, c.wantIsAnn)
		}
	}
}

// TestGranularity 粒度判定表驱动全量用例：wantErr 非空时要求错误文案
// 逐字相等（错误文案是对外契约）；为空要求通过。
func TestGranularity(t *testing.T) {
	const fn = "00002_create_users.up.sql"

	cases := []struct {
		name    string
		sql     string
		wantErr string
	}{
		// ---- 合规：单对象多语句 ----
		{
			name: "单表多语句通过_建表加加索引同表",
			sql: "-- +goose Up\n" +
				"ALTER TABLE users ADD COLUMN email text;\n" +
				"CREATE INDEX idx_users_email ON users(email);\n",
		},
		{
			name: "Down段语句不参与判定",
			sql: "-- +goose Up\n" +
				"CREATE TABLE users (id uuid PRIMARY KEY);\n" +
				"-- +goose Down\n" +
				"DROP TABLE orders;\n" +
				"DROP TABLE another;\n",
		},
		{
			name: "无Down段文件",
			sql: "-- +goose Up\n" +
				"CREATE TABLE users (id uuid PRIMARY KEY);\n",
		},
		{
			name: "注解夹在函数语句中间剥离后通过",
			sql: "-- +goose Up\n" +
				"CREATE FUNCTION public.fn() RETURNS int\n" +
				"-- +goose StatementBegin\n" +
				"AS $$ SELECT 1 $$;\n" +
				"-- +goose StatementEnd\n",
		},
		{
			name: "同表DML混合通过",
			sql: "-- +goose Up\n" +
				"INSERT INTO users(id) VALUES (1);\n" +
				"UPDATE users SET email = 'a@b.c' WHERE id = 1;\n" +
				"DELETE FROM users WHERE id = 2;\n",
		},
		{
			name: "SET白名单混合宿主语句通过",
			sql: "-- +goose Up\n" +
				"SET search_path TO public;\n" +
				"CREATE TABLE users (id uuid PRIMARY KEY);\n",
		},
		{
			name: "事务语句白名单混合宿主语句通过",
			sql: "-- +goose Up\n" +
				"BEGIN;\n" +
				"CREATE TABLE users (id uuid PRIMARY KEY);\n" +
				"COMMIT;\n",
		},
		{
			name: "建序列通过",
			sql: "-- +goose Up\n" +
				"CREATE SEQUENCE seq_order;\n",
		},
		{
			name: "改序列通过",
			sql: "-- +goose Up\n" +
				"ALTER SEQUENCE seq_order RESTART WITH 10;\n",
		},
		{
			name: "枚举加值通过",
			sql: "-- +goose Up\n" +
				"ALTER TYPE order_status ADD VALUE 'refunded' BEFORE 'paid';\n",
		},
		{
			name: "复合类型通过",
			sql: "-- +goose Up\n" +
				"CREATE TYPE address AS (city text, zip text);\n",
		},
		{
			name: "建触发器宿主为所在表",
			sql: "-- +goose Up\n" +
				"CREATE TRIGGER trg_audit AFTER INSERT ON users\n" +
				"FOR EACH ROW EXECUTE FUNCTION audit_fn();\n",
		},
		{
			name: "建视图加视图注释同名通过",
			sql: "-- +goose Up\n" +
				"CREATE VIEW v_users AS SELECT id FROM users;\n" +
				"COMMENT ON VIEW v_users IS '用户视图';\n",
		},
		{
			name: "物化视图通过",
			sql: "-- +goose Up\n" +
				"CREATE MATERIALIZED VIEW mv_users AS SELECT * FROM users;\n",
		},
		{
			name: "CREATE TABLE AS通过",
			sql: "-- +goose Up\n" +
				"CREATE TABLE t_backup AS SELECT * FROM users;\n",
		},
		{
			name: "单表GRANT通过",
			sql: "-- +goose Up\n" +
				"GRANT SELECT, UPDATE ON users TO public;\n",
		},
		{
			name: "单对象REVOKE通过",
			sql: "-- +goose Up\n" +
				"REVOKE SELECT ON users FROM public;\n",
		},
		{
			name: "建schema通过",
			sql: "-- +goose Up\n" +
				"CREATE SCHEMA IF NOT EXISTS analytics;\n",
		},
		{
			name: "建扩展通过",
			sql: "-- +goose Up\n" +
				"CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\";\n",
		},
		{
			name: "单表TRUNCATE通过",
			sql: "-- +goose Up\n" +
				"TRUNCATE users;\n",
		},
		{
			name: "单表DROP通过",
			sql: "-- +goose Up\n" +
				"DROP TABLE IF EXISTS users;\n",
		},
		{
			// 列注释宿主 = 列所属的表（列是表的附属，与索引同口径），
			// 而非列名——单独列注释文件宿主集合为 {users}。
			name: "列注释宿主取所属表通过",
			sql: "-- +goose Up\n" +
				"COMMENT ON COLUMN users.email IS '邮箱';\n",
		},
		{
			name: "建表加同表列注释通过",
			sql: "-- +goose Up\n" +
				"CREATE TABLE users (id uuid PRIMARY KEY, email text);\n" +
				"COMMENT ON COLUMN users.email IS '邮箱';\n",
		},
		{
			name: "限定名列注释宿主取表通过",
			sql: "-- +goose Up\n" +
				"COMMENT ON COLUMN public.users.email IS '邮箱';\n",
		},

		// ---- 拒绝：多对象 ----
		{
			name: "一文件两建表拒绝",
			sql: "-- +goose Up\n" +
				"CREATE TABLE users (id uuid PRIMARY KEY);\n" +
				"CREATE TABLE orders (id uuid PRIMARY KEY);\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: users, orders；须一个目标对象一个文件）",
		},
		{
			// 列注释宿主是所属表：与另一张表的 DDL 同文件即跨对象拒绝
			// （若列名误当宿主，此文件会以 {orders, email} 误拒或漏放，
			// 本用例锁定宿主语义）。
			name: "建表加它表列注释拒绝",
			sql: "-- +goose Up\n" +
				"CREATE TABLE orders (id uuid PRIMARY KEY);\n" +
				"COMMENT ON COLUMN users.email IS '邮箱';\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: orders, users；须一个目标对象一个文件）",
		},
		{
			name: "单语句多对象DROP TABLE拒绝",
			sql: "-- +goose Up\n" +
				"DROP TABLE a, b;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: a, b；须一个目标对象一个文件）",
		},
		{
			name: "单语句多对象DROP SEQUENCE拒绝",
			sql: "-- +goose Up\n" +
				"DROP SEQUENCE IF EXISTS seq_a, seq_b;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: seq_a, seq_b；须一个目标对象一个文件）",
		},
		{
			name: "多表TRUNCATE拒绝",
			sql: "-- +goose Up\n" +
				"TRUNCATE users, orders;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: users, orders；须一个目标对象一个文件）",
		},
		{
			name: "多表GRANT拒绝",
			sql: "-- +goose Up\n" +
				"GRANT SELECT ON users, orders TO public;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: users, orders；须一个目标对象一个文件）",
		},
		{
			name: "不同表DDL混合拒绝",
			sql: "-- +goose Up\n" +
				"ALTER TABLE users ADD COLUMN email text;\n" +
				"CREATE INDEX idx_orders_no ON orders(order_no);\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: users, orders；须一个目标对象一个文件）",
		},
		{
			name: "不同表DML混合拒绝",
			sql: "-- +goose Up\n" +
				"INSERT INTO a(id) VALUES (1);\n" +
				"UPDATE b SET x = 1;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: a, b；须一个目标对象一个文件）",
		},
		{
			name: "视图加物化视图拒绝",
			sql: "-- +goose Up\n" +
				"CREATE VIEW v_users AS SELECT 1;\n" +
				"CREATE MATERIALIZED VIEW mv_users AS SELECT 1;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: v_users, mv_users；须一个目标对象一个文件）",
		},
		{
			name: "同表触发器混函数定义拒绝_触发器宿主是表",
			sql: "-- +goose Up\n" +
				"CREATE FUNCTION audit_fn() RETURNS trigger AS $$ BEGIN RETURN NULL; END $$ LANGUAGE plpgsql;\n" +
				"CREATE TRIGGER trg AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION audit_fn();\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: audit_fn, users；须一个目标对象一个文件）",
		},
		{
			name: "对象名按出现序去重后拼接",
			sql: "-- +goose Up\n" +
				"CREATE TABLE a (id int);\n" +
				"CREATE TABLE b (id int);\n" +
				"CREATE INDEX idx_a ON a(id);\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（涉及多个对象: a, b；须一个目标对象一个文件）",
		},

		// ---- 拒绝：空集合 ----
		{
			name: "SET加BEGIN全无宿主拒绝",
			sql: "-- +goose Up\n" +
				"SET search_path TO public;\n" +
				"BEGIN;\n" +
				"COMMIT;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（未包含任何针对数据库对象的语句；须一个目标对象一个文件）",
		},
		{
			name: "纯SELECT无宿主拒绝",
			sql: "-- +goose Up\n" +
				"SELECT 1;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（未包含任何针对数据库对象的语句；须一个目标对象一个文件）",
		},
		{
			name: "Up段只有SQL注释行拒绝",
			sql: "-- +goose Up\n" +
				"-- 只有注释，没有语句\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（未包含任何针对数据库对象的语句；须一个目标对象一个文件）",
		},
		{
			name:    "无Up注解空Up段拒绝",
			sql:     "SELECT 1;\n",
			wantErr: "迁移文件粒度不合规: 00002_create_users.up.sql（未包含任何针对数据库对象的语句；须一个目标对象一个文件）",
		},

		// ---- 拒绝：白名单外节点（fail-closed） ----
		{
			name: "Do语句白名单外拒绝",
			sql: "-- +goose Up\n" +
				"DO $$ BEGIN NULL; END $$;\n",
			wantErr: "迁移文件包含无法判定宿主对象的语句: 00002_create_users.up.sql（语句类型: DoStmt；不支持该语句类型时向 gf-lib 维护方反馈）",
		},
		{
			name: "CALL语句白名单外拒绝",
			sql: "-- +goose Up\n" +
				"CALL some_proc(1);\n",
			wantErr: "迁移文件包含无法判定宿主对象的语句: 00002_create_users.up.sql（语句类型: CallStmt；不支持该语句类型时向 gf-lib 维护方反馈）",
		},
		{
			name: "SELECT INTO建表形态白名单外拒绝",
			sql: "-- +goose Up\n" +
				"SELECT * INTO new_t FROM users;\n",
			wantErr: "迁移文件包含无法判定宿主对象的语句: 00002_create_users.up.sql（语句类型: SelectStmt；不支持该语句类型时向 gf-lib 维护方反馈）",
		},
		{
			name: "DROP VIEW的removeType未映射拒绝",
			sql: "-- +goose Up\n" +
				"DROP VIEW v_users;\n",
			wantErr: "迁移文件包含无法判定宿主对象的语句: 00002_create_users.up.sql（语句类型: DropStmt；不支持该语句类型时向 gf-lib 维护方反馈）",
		},
		{
			name: "建角色语句白名单外拒绝",
			sql: "-- +goose Up\n" +
				"CREATE ROLE readonly;\n",
			wantErr: "迁移文件包含无法判定宿主对象的语句: 00002_create_users.up.sql（语句类型: CreateRoleStmt；不支持该语句类型时向 gf-lib 维护方反馈）",
		},
		{
			name: "白名单外语句混合规语句仍拒绝",
			sql: "-- +goose Up\n" +
				"CREATE TABLE users (id uuid PRIMARY KEY);\n" +
				"CALL some_proc(1);\n",
			wantErr: "迁移文件包含无法判定宿主对象的语句: 00002_create_users.up.sql（语句类型: CallStmt；不支持该语句类型时向 gf-lib 维护方反馈）",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateGranularity(fn, c.sql)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过, got 错误: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望错误 %q, got nil", c.wantErr)
			}
			if err.Error() != c.wantErr {
				t.Fatalf("错误文案不符:\n got: %s\nwant: %s", err.Error(), c.wantErr)
			}
		})
	}
}

// TestGranularityParseError 语法错误走解析失败分支：pg_query 的原始
// 错误文本不便于逐字断言，只验证前缀（文件名定位）与错误链可解出底层。
func TestGranularityParseError(t *testing.T) {
	err := validateGranularity("00003_bad.up.sql", "-- +goose Up\nCREATE TEBLE x (id int);\n")
	if err == nil {
		t.Fatal("语法错误应返回解析失败错误, got nil")
	}
	if prefix := "迁移文件解析失败: 00003_bad.up.sql: "; !strings.HasPrefix(err.Error(), prefix) {
		t.Fatalf("错误应含解析失败前缀 %q, got %q", prefix, err.Error())
	}
}

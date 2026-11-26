//go:build dev

// Fresh 的同包内部测试（package migrate）：Fresh 的未知方言 default
// 分支只能通过直接构造内部状态的 Migrator 覆盖（New 构造期会拒绝
// 未知方言，外部测试包无法造出该状态）。不触真库。
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// errFreshFakeDB 内部测试专用假连接错误，验证错误链可解出底层原因。
var errFreshFakeDB = errors.New("假数据库连接失败（单测注入）")

// TestFreshUnknownDialect 未知方言（映射表外的值）走 default 分支：
// Fresh 在取连接前即拒绝，dbFactory 不被调用——「先方言分派后取
// 连接」的直接证明。该分支兜「未来扩枚举进了 New 映射表但 Fresh
// 漏实现」的纵深防御场景。
func TestFreshUnknownDialect(t *testing.T) {
	called := false
	m := &Migrator{
		dialect: Dialect(99), // New 构造期会拒绝的值，直接构造绕过校验
		fs:      fstest.MapFS{},
		dbFactory: func() (*sql.DB, error) {
			called = true
			return nil, errFreshFakeDB
		},
	}

	err := m.Fresh(context.Background())
	if err == nil {
		t.Fatal("Fresh() 未知方言期望返回错误，实际成功")
	}
	if !strings.Contains(err.Error(), "不支持的数据库方言") {
		t.Fatalf("Fresh() 错误文案 %q 未包含期望文案 %q", err.Error(), "不支持的数据库方言")
	}
	if called {
		t.Fatal("未知方言应在取连接前拒绝，dbFactory 不应被调用")
	}
}

// TestFreshPostgresDBFactoryFailed PG 路径：注入失败的连接工厂，
// Fresh 返回「获取数据库连接失败: 」前缀错误且 %w 链可解出底层
// 错误；工厂被调用证明 PG 分支确已进入取连接步骤。不触真库。
func TestFreshPostgresDBFactoryFailed(t *testing.T) {
	called := false
	m := &Migrator{
		dialect: DialectPostgres,
		fs:      fstest.MapFS{},
		dbFactory: func() (*sql.DB, error) {
			called = true
			return nil, errFreshFakeDB
		},
	}

	err := m.Fresh(context.Background())
	if err == nil {
		t.Fatal("Fresh() 连接工厂失败时期望返回错误，实际成功")
	}
	if !called {
		t.Fatal("PG 路径应先取连接，dbFactory 未被调用")
	}
	if !strings.HasPrefix(err.Error(), "获取数据库连接失败: ") {
		t.Fatalf("Fresh() 错误缺少中文前缀，实际: %q", err.Error())
	}
	if unwrapped := errors.Unwrap(err); !errors.Is(unwrapped, errFreshFakeDB) {
		t.Fatalf("errors.Unwrap 未解出底层工厂错误，实际: %v", unwrapped)
	}
}

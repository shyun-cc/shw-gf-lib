// Package migrate_test migrate 引擎的单元测试（外部测试包，不触真库）。
//
// 覆盖：New 构造期校验各分支（含迁移文件名格式 R7）、DB 注入工厂
// 被调用与失败错误前缀、Up 错误链 errors.Unwrap 可解出底层错误。
// goose 不出现在本文件，真库行为由 tests/ 集成测试（-tags
// integration）验证。
package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/shyun-cc/shw-gf-lib/migrate"
)

// errFakeDB 单测专用假连接错误，用于验证错误链可解出工厂返回的底层错误。
var errFakeDB = errors.New("假数据库连接失败（单测注入）")

// TestNew 合法配置应构造成功（对照分支，保证后续错误分支测试有意义）。
func TestNew(t *testing.T) {
	m, err := migrate.New(migrate.Config{
		FS:      fstest.MapFS{},
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		t.Fatalf("New() 不期望返回错误: %v", err)
	}
	if m == nil {
		t.Fatal("New() 成功时不应返回 nil Migrator")
	}
}

// TestNewValidation 覆盖 New 构造期校验的两条错误分支：
// FS 为 nil、方言零值，各自返回含指定中文文案的错误。
func TestNewValidation(t *testing.T) {
	testCases := []struct {
		name    string
		cfg     migrate.Config
		wantErr string
	}{
		{
			name:    "FS 为 nil",
			cfg:     migrate.Config{FS: nil, Dialect: migrate.DialectPostgres},
			wantErr: "迁移文件系统未初始化，请联系开发者",
		},
		{
			name:    "方言零值",
			cfg:     migrate.Config{FS: fstest.MapFS{}, Dialect: 0},
			wantErr: "不支持的数据库方言",
		},
		{
			name:    "方言映射表外的值",
			cfg:     migrate.Config{FS: fstest.MapFS{}, Dialect: migrate.Dialect(99)},
			wantErr: "不支持的数据库方言",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := migrate.New(tc.cfg)
			if err == nil {
				t.Fatalf("New() 期望返回错误，实际成功: m=%v", m)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("New() 错误文案 %q 未包含期望文案 %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestNewFilenameValidation 迁移文件名格式校验（R7）：合法名（含存量
// 批次名 00003_init.sql）与非 .sql 文件（不参与校验）构造成功；位数
// 不足 / 连字符 / 无下划线等格式失真名构造失败且错误文案带文件名。
func TestNewFilenameValidation(t *testing.T) {
	testCases := []struct {
		name    string
		files   []string
		wantErr string // 空串表示期望构造成功
	}{
		{
			name:  "合法名 00001_create_users.sql",
			files: []string{"00001_create_users.sql"},
		},
		{
			name:  "存量批次名天然合规 00003_init.sql",
			files: []string{"00003_init.sql"},
		},
		{
			name:    "位数不足 1_x.sql",
			files:   []string{"1_x.sql"},
			wantErr: "迁移文件名格式不合法: 1_x.sql（须为 {5 位数字}_{描述}.sql，如 00001_create_users.sql）",
		},
		{
			name:    "连字符 00002-init.sql",
			files:   []string{"00002-init.sql"},
			wantErr: "迁移文件名格式不合法: 00002-init.sql（须为 {5 位数字}_{描述}.sql，如 00001_create_users.sql）",
		},
		{
			name:    "无下划线 00002init.sql",
			files:   []string{"00002init.sql"},
			wantErr: "迁移文件名格式不合法: 00002init.sql（须为 {5 位数字}_{描述}.sql，如 00001_create_users.sql）",
		},
		{
			name:  "非 .sql 文件不参与校验 README.txt",
			files: []string{"README.txt"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mapFS := fstest.MapFS{}
			for _, name := range tc.files {
				mapFS[name] = &fstest.MapFile{Data: []byte("-- " + name)}
			}
			m, err := migrate.New(migrate.Config{FS: mapFS, Dialect: migrate.DialectPostgres})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("New() 不期望返回错误: %v", err)
				}
				if m == nil {
					t.Fatal("New() 成功时不应返回 nil Migrator")
				}
				return
			}
			if err == nil {
				t.Fatalf("New() 期望返回错误，实际成功: m=%v", m)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("New() 错误文案 %q 与期望文案 %q 不符", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestUpDBFactoryCalled 注入的 DB 工厂应被调用；工厂失败时 Up 返回
// 「获取数据库连接失败: 」前缀错误，且错误链可解出工厂返回的底层错误。
// 不真正连库。
func TestUpDBFactoryCalled(t *testing.T) {
	called := false
	m, err := migrate.New(migrate.Config{
		FS:      fstest.MapFS{},
		Dialect: migrate.DialectPostgres,
		DB: func() (*sql.DB, error) {
			called = true
			return nil, errFakeDB
		},
	})
	if err != nil {
		t.Fatalf("New() 不期望返回错误: %v", err)
	}

	err = m.Up(context.Background())
	if err == nil {
		t.Fatal("Up() 期望返回错误，实际成功")
	}
	if !called {
		t.Fatal("注入的 DB 工厂未被调用")
	}
	if !strings.HasPrefix(err.Error(), "获取数据库连接失败: ") {
		t.Fatalf("Up() 错误缺少中文前缀，实际: %q", err.Error())
	}
}

// TestUpErrorChain Up 的取连接错误分支用 %w 包装，errors.Unwrap 应
// 解出注入的底层错误（不依赖 errors.Is 的模糊匹配）。
func TestUpErrorChain(t *testing.T) {
	m, err := migrate.New(migrate.Config{
		FS:      fstest.MapFS{},
		Dialect: migrate.DialectPostgres,
		DB:      func() (*sql.DB, error) { return nil, errFakeDB },
	})
	if err != nil {
		t.Fatalf("New() 不期望返回错误: %v", err)
	}

	err = m.Up(context.Background())
	if err == nil {
		t.Fatal("Up() 期望返回错误，实际成功")
	}
	if unwrapped := errors.Unwrap(err); !errors.Is(unwrapped, errFakeDB) {
		t.Fatalf("errors.Unwrap 未解出底层工厂错误，实际: %v", unwrapped)
	}
}

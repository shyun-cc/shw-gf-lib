// state 对账决策的内部测试（package migrate）：reconcile 是私有纯函数，
// 外部测试包无法触达。只测决策表（构造输入直接断言输出）与
// parseVersion / checksumHex 两个纯工具函数，不触真库——SQL 执行层
// 与 ensureStateTable 的真库行为由 tests/ 集成测试覆盖。
package migrate

import (
	"reflect"
	"strings"
	"testing"
)

// 决策表共用数据：三个版本的基准三元组与篡改 hash。
// cs* 长度固定 64，与旁表 char(64) 列形态一致。
var (
	f1 = fsFile{Version: 1, Filename: "00001_init.sql", Checksum: strings.Repeat("a", 64)}
	f2 = fsFile{Version: 2, Filename: "00002_add_users.sql", Checksum: strings.Repeat("b", 64)}
	s1 = stateRow{Version: 1, Filename: "00001_init.sql", Checksum: strings.Repeat("a", 64)}
	s2 = stateRow{Version: 2, Filename: "00002_add_users.sql", Checksum: strings.Repeat("b", 64)}
	// cs2Modified 版本 2 被篡改后的 hash（R1 场景专用）。
	cs2Modified = strings.Repeat("c", 64)
)

// TestReconcile 对账决策表全量覆盖。wantFill / wantOrph 用空切片字面量
// 而非 nil：reconcile 契约是无错误时返回非 nil 空切片，DeepEqual 区分
// 二者。
func TestReconcile(t *testing.T) {
	testCases := []struct {
		name     string
		applied  []int64
		state    []stateRow
		files    []fsFile
		wantFill []fsFile
		wantBase bool
		wantOrph []int64
		wantErr  string // 空串表示期望无错误
	}{
		{
			name:     "正常全对",
			applied:  []int64{1, 2},
			state:    []stateRow{s1, s2},
			files:    []fsFile{f1, f2},
			wantFill: []fsFile{},
			wantBase: false,
			wantOrph: []int64{},
		},
		{
			// goose 建版本表时必然插入 version=0 基线行（is_applied=true），
			// listAppliedVersions 的 SQL 已排除；此用例锁定 reconcile 侧
			// 语义：即便 0 混入 applied（防御 SQL 层未来回归），也不得
			// 被误判为 R2 缺失。
			name:     "applied 混入 goose 零版本基线行不误判",
			applied:  []int64{0, 1, 2},
			state:    []stateRow{s1, s2},
			files:    []fsFile{f1, f2},
			wantFill: []fsFile{},
			wantBase: false,
			wantOrph: []int64{},
		},
		{
			name:     "首次基线化（旁表空+版本表非空）",
			applied:  []int64{1, 2},
			state:    []stateRow{},
			files:    []fsFile{f1, f2},
			wantFill: []fsFile{f1, f2},
			wantBase: true,
			wantOrph: []int64{},
		},
		{
			name:     "R1 改动（同名同版本但内容 hash 变）",
			applied:  []int64{2},
			state:    []stateRow{s2},
			files:    []fsFile{{Version: 2, Filename: "00002_add_users.sql", Checksum: cs2Modified}},
			wantFill: nil,
			wantBase: false,
			wantOrph: nil,
			wantErr:  "已应用的迁移文件被修改: 00002_add_users.sql（禁止修改已应用文件，请新增迁移文件）",
		},
		{
			name:     "R2 缺失（FS 无该版本文件，旁表有记录时报旁表文件名）",
			applied:  []int64{1, 2},
			state:    []stateRow{s1, s2},
			files:    []fsFile{f1},
			wantFill: nil,
			wantBase: false,
			wantOrph: nil,
			wantErr:  "已应用的迁移文件缺失: 00002_add_users.sql（禁止删除或重命名已应用文件）",
		},
		{
			name:     "R2 缺失（旁表无记录时退回版本号）",
			applied:  []int64{1, 2},
			state:    []stateRow{s1},
			files:    []fsFile{f1},
			wantFill: nil,
			wantBase: false,
			wantOrph: nil,
			wantErr:  "已应用的迁移文件缺失: 2（禁止删除或重命名已应用文件）",
		},
		{
			name:    "R2 仅改描述改名（同版本号、文件名变、内容 hash 未变）",
			applied: []int64{2},
			state:   []stateRow{{Version: 2, Filename: "00002_init.sql", Checksum: strings.Repeat("b", 64)}},
			files:   []fsFile{{Version: 2, Filename: "00002_renamed.sql", Checksum: strings.Repeat("b", 64)}},
			wantErr: "已应用的迁移文件缺失: 00002_init.sql（禁止删除或重命名已应用文件）",
		},
		{
			name:    "R2 与 R1 同时命中时优先报缺失（改名检查在内容检查之前）",
			applied: []int64{2},
			state:   []stateRow{{Version: 2, Filename: "00002_init.sql", Checksum: strings.Repeat("b", 64)}},
			files:   []fsFile{{Version: 2, Filename: "00002_renamed.sql", Checksum: cs2Modified}},
			wantErr: "已应用的迁移文件缺失: 00002_init.sql（禁止删除或重命名已应用文件）",
		},
		{
			name:     "孤儿（旁表有记录、版本表无该版本，不报错）",
			applied:  []int64{1},
			state:    []stateRow{s1, {Version: 3, Filename: "00003_old.sql", Checksum: strings.Repeat("d", 64)}},
			files:    []fsFile{f1},
			wantFill: []fsFile{},
			wantBase: false,
			wantOrph: []int64{3},
		},
		{
			name:     "中途失败补录（版本表 [1,2]、旁表只有 [1]、FS [1,2] → 补录 2、非基线化）",
			applied:  []int64{1, 2},
			state:    []stateRow{s1},
			files:    []fsFile{f1, f2},
			wantFill: []fsFile{f2},
			wantBase: false,
			wantOrph: []int64{},
		},
		{
			name:     "全新库两表皆空不算基线化",
			applied:  []int64{},
			state:    []stateRow{},
			files:    []fsFile{f1, f2},
			wantFill: []fsFile{},
			wantBase: false,
			wantOrph: []int64{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			backfill, baseline, orphans, err := reconcile(tc.applied, tc.state, tc.files)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("reconcile() 不期望返回错误: %v", err)
				}
				if !reflect.DeepEqual(backfill, tc.wantFill) {
					t.Fatalf("reconcile() backfill = %v, 期望 %v", backfill, tc.wantFill)
				}
				if baseline != tc.wantBase {
					t.Fatalf("reconcile() baseline = %v, 期望 %v", baseline, tc.wantBase)
				}
				if !reflect.DeepEqual(orphans, tc.wantOrph) {
					t.Fatalf("reconcile() orphans = %v, 期望 %v", orphans, tc.wantOrph)
				}
				return
			}

			if err == nil {
				t.Fatalf("reconcile() 期望返回错误 %q, 实际成功", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("reconcile() 错误文案 %q, 期望逐字等于 %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestParseVersion 版本号解析：合法数字前缀（含前导零）解析成功，
// 非数字开头与 int64 溢出拒绝。
func TestParseVersion(t *testing.T) {
	testCases := []struct {
		filename string
		want     int64
		wantOK   bool
	}{
		{filename: "00003_init.sql", want: 3, wantOK: true},
		{filename: "1_create_table.sql", want: 1, wantOK: true},
		{filename: "00012_alter_orders_add_status.sql", want: 12, wantOK: true},
		{filename: "README.md", want: 0, wantOK: false},
		{filename: ".DS_Store", want: 0, wantOK: false},
		{filename: "abc_1.sql", want: 0, wantOK: false},
		{filename: "", want: 0, wantOK: false},
		{filename: "99999999999999999999_overflow.sql", want: 0, wantOK: false},
	}
	for _, tc := range testCases {
		got, ok := parseVersion(tc.filename)
		if got != tc.want || ok != tc.wantOK {
			t.Fatalf("parseVersion(%q) = (%d, %v), 期望 (%d, %v)",
				tc.filename, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestChecksumHex sha256 已知向量 + 长度 64 + 小写十六进制断言，
// 锁定与旁表 char(64) 列的对齐契约。
func TestChecksumHex(t *testing.T) {
	testCases := []struct {
		data []byte
		want string
	}{
		{data: nil, want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{data: []byte("abc"), want: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	}
	for _, tc := range testCases {
		got := checksumHex(tc.data)
		if got != tc.want {
			t.Fatalf("checksumHex(%q) = %q, 期望 %q", tc.data, got, tc.want)
		}
		if len(got) != 64 || got != strings.ToLower(got) {
			t.Fatalf("checksumHex 应返回 64 字符小写 hex, 实际 %q", got)
		}
	}
}

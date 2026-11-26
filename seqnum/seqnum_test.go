// seqnum 单元测试（change: add-package-tests，task 3.4）。
//
// 覆盖：intToCode/codeToInt 三进制互转、validateFirstChar 范围校验、
// NextCode 连续自增与 Prefix/Suffix/Padding/Length 拼装。
//
// 关键规避点：NextCode 在 INCR 结果为 1（键不存在/首次）时会走
// recoverFromDB → g.DB()，单测环境无 DB 配置会报错。因此所有 NextCode
// 用例都先预置 Redis 键 "seq_num:<Name>" 为 ≥1 的值，使 INCR 结果 >1，
// 完全绕开 DB 恢复路径（DB 恢复场景由 tests/ 集成测试覆盖，见
// tests/seqnum_recovery_test.go）。
package seqnum

import (
	"context"
	"sync"
	"testing"

	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestIntToCode 整数转进制编码：36/10/26 三种字典，含 0 与跨字母数字边界值（9/10/35/36）
func TestIntToCode(t *testing.T) {
	cases := []struct {
		name     string
		num      int
		charType int
		want     string
	}{
		// 36 进制（0-9A-Z）：数字→字母边界在 9/10，进位边界在 35/36
		{"36进制-零值", 0, CharTypeBoth, "0"},
		{"36进制-个位数字", 9, CharTypeBoth, "9"},
		{"36进制-数字进字母", 10, CharTypeBoth, "A"},
		{"36进制-最大单字符", 35, CharTypeBoth, "Z"},
		{"36进制-字母后进位", 36, CharTypeBoth, "10"},
		{"36进制-两位最大值", 1295, CharTypeBoth, "ZZ"},
		{"36进制-两位进三位", 1296, CharTypeBoth, "100"},
		// 10 进制（0-9）：9/10 是位数进位边界
		{"10进制-零值", 0, CharTypeNumber, "0"},
		{"10进制-个位最大", 9, CharTypeNumber, "9"},
		{"10进制-进两位", 10, CharTypeNumber, "10"},
		{"10进制-两位进三位", 100, CharTypeNumber, "100"},
		// 26 进制（A-Z，A=0）：9/10 对应 J/K，25/26 对应 Z/BA
		{"26进制-零值映射A", 0, CharTypeLetter, "A"},
		{"26进制-个位中间", 9, CharTypeLetter, "J"},
		{"26进制-跨位前", 10, CharTypeLetter, "K"},
		{"26进制-最大单字符", 25, CharTypeLetter, "Z"},
		{"26进制-字母后进位", 26, CharTypeLetter, "BA"},
		// 26 进制 A=0：两位最大值 "ZZ"=25*26+25=675，进三位 "BAA"=676
		{"26进制-两位最大值", 675, CharTypeLetter, "ZZ"},
		{"26进制-两位进三位", 676, CharTypeLetter, "BAA"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := intToCode(c.num, c.charType); got != c.want {
				t.Fatalf("intToCode(%d, %d) = %q, 期望 %q", c.num, c.charType, got, c.want)
			}
		})
	}
}

// TestCodeToInt 进制编码转整数：与 intToCode 对称的解码，含字典外字符与空串返回 0
func TestCodeToInt(t *testing.T) {
	cases := []struct {
		name     string
		code     string
		charType int
		want     int
	}{
		{"36进制-零值", "0", CharTypeBoth, 0},
		{"36进制-个位数字", "9", CharTypeBoth, 9},
		{"36进制-字母开头", "A", CharTypeBoth, 10},
		{"36进制-最大单字符", "Z", CharTypeBoth, 35},
		{"36进制-两位编码", "10", CharTypeBoth, 36},
		{"36进制-两位最大", "ZZ", CharTypeBoth, 1295},
		{"10进制-个位", "9", CharTypeNumber, 9},
		{"10进制-多位", "100", CharTypeNumber, 100},
		{"26进制-A即零", "A", CharTypeLetter, 0},
		{"26进制-最大单字符", "Z", CharTypeLetter, 25},
		{"26进制-进位编码", "BA", CharTypeLetter, 26},
		{"26进制-两位最大", "ZZ", CharTypeLetter, 675},
		{"26进制-三位编码", "BAZ", CharTypeLetter, 701},
		// 异常输入：字典外字符（含小写、非字典符号）与空串统一返回 0
		{"36进制-小写字典外", "a1", CharTypeBoth, 0},
		{"10进制-字母字典外", "1A", CharTypeNumber, 0},
		{"26进制-数字字典外", "0A", CharTypeLetter, 0},
		{"10进制-符号字典外", "-", CharTypeNumber, 0},
		{"36进制-空串", "", CharTypeBoth, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := codeToInt(c.code, c.charType); got != c.want {
				t.Fatalf("codeToInt(%q, %d) = %d, 期望 %d", c.code, c.charType, got, c.want)
			}
		})
	}
}

// TestCodeRoundTrip 三进制互转往返：codeToInt(intToCode(n)) 必须还原为 n
func TestCodeRoundTrip(t *testing.T) {
	// 取样覆盖各进制的关键区间：0、个位边界、进位边界、多位
	samples := []int{0, 1, 8, 9, 10, 25, 35, 36, 99, 100, 1295, 1296, 46655}
	for _, charType := range []int{CharTypeBoth, CharTypeNumber, CharTypeLetter} {
		for _, n := range samples {
			code := intToCode(n, charType)
			if back := codeToInt(code, charType); back != n {
				t.Fatalf("charType=%d 往返不一致: %d → %q → %d", charType, n, code, back)
			}
		}
	}
}

// TestValidateFirstChar 首字符范围校验：未配置跳过、边界含端点、越界与字典外报错
func TestValidateFirstChar(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		rule    CodeRule
		wantErr bool
	}{
		// FcMin/FcMax 均为 0 视为未配置，任何编码直接放行
		{"未配置范围-直接通过", "9ZZ", CodeRule{CharType: CharTypeBoth}, false},
		// 36 进制字典索引：'0'-'9' 为 0-9，'A'-'Z' 为 10-35
		{"范围0-9-数字开头通过", "9ZZ", CodeRule{CharType: CharTypeBoth, FcMin: 0, FcMax: 9}, false},
		{"范围0-9-下边界通过", "0AB", CodeRule{CharType: CharTypeBoth, FcMin: 0, FcMax: 9}, false},
		{"范围0-9-字母开头报错", "A10", CodeRule{CharType: CharTypeBoth, FcMin: 0, FcMax: 9}, true},
		{"范围10-35-字母开头通过", "A00", CodeRule{CharType: CharTypeBoth, FcMin: 10, FcMax: 35}, false},
		{"范围10-35-上边界通过", "Z00", CodeRule{CharType: CharTypeBoth, FcMin: 10, FcMax: 35}, false},
		{"范围10-35-数字开头报错", "9AB", CodeRule{CharType: CharTypeBoth, FcMin: 10, FcMax: 35}, true},
		// 字典外字符（小写不在任何字典）：报「不在字典中」
		{"字典外字符-小写报错", "a01", CodeRule{CharType: CharTypeBoth, FcMin: 0, FcMax: 35}, true},
		{"字典外字符-数字对26进制报错", "0AB", CodeRule{CharType: CharTypeLetter, FcMin: 0, FcMax: 25}, true},
		// 已配置范围但编码为空：报「编码为空」
		{"空编码-配置范围时报错", "", CodeRule{CharType: CharTypeBoth, FcMin: 1, FcMax: 9}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateFirstChar(c.code, c.rule)
			if c.wantErr && err == nil {
				t.Fatalf("validateFirstChar(%q, %+v) 期望报错，实际通过", c.code, c.rule)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validateFirstChar(%q, %+v) 不应报错: %v", c.code, c.rule, err)
			}
		})
	}
}

// TestNextCodeSequence 连续自增：10 进制 + Prefix + Padding 补齐到 Length
func TestNextCodeSequence(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	// 预置键值为 100：INCR 结果从 101 起，绕开首次 INCR=1 的 DB 恢复路径
	rule := CodeRule{
		Name:     "ut_next_dec",
		Prefix:   "PO",
		Suffix:   "",
		Length:   5,
		Padding:  "0",
		CharType: CharTypeNumber,
	}
	if _, err := g.Redis().Set(ctx, "seq_num:"+rule.Name, 100); err != nil {
		t.Fatalf("预置 Redis 键失败: %v", err)
	}
	// 恢复安全契约：计数与 floor 基准缓存成对存在（生产由首次恢复流程建立；
	// 预置计数=已恢复状态，须同步预置 floor，否则 NextCode 会视为冷启动走 DB 恢复）
	if _, err := g.Redis().Set(ctx, "seq_num:"+rule.Name+":floor", 100); err != nil {
		t.Fatalf("预置 floor 键失败: %v", err)
	}

	// 连续三次调用：值 101/102/103 → 编码 "00101"/"00102"/"00103" → 拼前缀
	wantSeq := []string{"PO00101", "PO00102", "PO00103"}
	for i, want := range wantSeq {
		got, err := NextCode(ctx, rule)
		if err != nil {
			t.Fatalf("第 %d 次调用 NextCode 失败: %v", i+1, err)
		}
		if got != want {
			t.Fatalf("第 %d 次调用 NextCode = %q, 期望 %q", i+1, got, want)
		}
	}
}

// TestNextCodeCharTypes 各进制与前后缀拼装：36 进制跨字母数字边界、26 进制字母补齐、Suffix 拼接
func TestNextCodeCharTypes(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		rule   CodeRule
		preset int    // 预置的 Redis 键值（≥1，使 INCR 结果 >1 绕开 DB）
		want   string // 第一次 NextCode 的期望值
		want2  string // 第二次 NextCode 的期望值（验证连续自增）
	}{
		{
			// 36 进制：预置 34 → 首次 INCR=35 → "Z"；再次 INCR=36 → "10"（数字→字母→进位边界）
			name:   "36进制-Z后进位",
			rule:   CodeRule{Name: "ut_next_36", Prefix: "OD", Suffix: "S", Length: 4, Padding: "0", CharType: CharTypeBoth},
			preset: 34,
			want:   "OD000ZS",
			want2:  "OD0010S",
		},
		{
			// 26 进制：预置 24 → 首次 INCR=25 → "Z"；再次 INCR=26 → "BA"，Padding="A" 补齐
			name:   "26进制-Z后进位",
			rule:   CodeRule{Name: "ut_next_26", Prefix: "", Suffix: "", Length: 3, Padding: "A", CharType: CharTypeLetter},
			preset: 24,
			want:   "AAZ",
			want2:  "ABA",
		},
		{
			// 编码长度已超 Length 时只补不截：12345 为 5 位，Length=3 不截断
			name:   "10进制-超长不截断",
			rule:   CodeRule{Name: "ut_next_long", Prefix: "", Suffix: "", Length: 3, Padding: "0", CharType: CharTypeNumber},
			preset: 12344,
			want:   "12345",
			want2:  "12346",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := g.Redis().Set(ctx, "seq_num:"+c.rule.Name, c.preset); err != nil {
				t.Fatalf("预置 Redis 键失败: %v", err)
			}
			// 恢复安全契约：预置计数须成对预置 floor 基准（同 TestNextCodeSequence 注释）
			if _, err := g.Redis().Set(ctx, "seq_num:"+c.rule.Name+":floor", c.preset); err != nil {
				t.Fatalf("预置 floor 键失败: %v", err)
			}
			for i, want := range []string{c.want, c.want2} {
				got, err := NextCode(ctx, c.rule)
				if err != nil {
					t.Fatalf("第 %d 次调用 NextCode 失败: %v", i+1, err)
				}
				if got != want {
					t.Fatalf("第 %d 次调用 NextCode = %q, 期望 %q", i+1, got, want)
				}
			}
		})
	}
}

// TestNextCodeFirstCharReject NextCode 的首字符校验联动：编码首字符越界时整体报错
func TestNextCodeFirstCharReject(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	// 规则要求首字符必须是字母（索引 10-35），预置 8 → INCR=9 → 编码 "9"
	// 首字符索引 9 < FcMin=10，应报错（预置 8 保证 INCR 结果 >1，不触发 DB）
	rule := CodeRule{
		Name:     "ut_next_fc",
		CharType: CharTypeBoth,
		FcMin:    10,
		FcMax:    35,
	}
	if _, err := g.Redis().Set(ctx, "seq_num:"+rule.Name, 8); err != nil {
		t.Fatalf("预置 Redis 键失败: %v", err)
	}
	// 恢复安全契约：预置计数须成对预置 floor 基准
	if _, err := g.Redis().Set(ctx, "seq_num:"+rule.Name+":floor", 8); err != nil {
		t.Fatalf("预置 floor 键失败: %v", err)
	}

	if _, err := NextCode(ctx, rule); err == nil {
		t.Fatal("首字符越界时 NextCode 应报错，实际成功")
	}
}

// ---- 恢复安全与 GetCurrentCode（change: fix-existing-gaps）----

// withFakeRecover 替换 DB 恢复注入点为伪实现（返回固定 max），测试结束还原
func withFakeRecover(t *testing.T, max int) {
	t.Helper()
	old := recoverFromDBFn
	recoverFromDBFn = func(_ context.Context, _ CodeRule) int { return max }
	t.Cleanup(func() { recoverFromDBFn = old })
}

// TestNextCode_ConcurrentRecoveryNoDuplicates Redis 丢数据后并发恢复不重号：
// 预置计数后 DEL key 模拟丢数据，伪 DB 最大序号 M=100，并发 10 个 NextCode
// 应全部 > 100 且互不相同（恢复锁互斥 + 二次 INCR 推进 + 未抢锁者自旋重试）
func TestNextCode_ConcurrentRecoveryNoDuplicates(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()
	withFakeRecover(t, 100)

	rule := CodeRule{
		Name: "recover-race", Prefix: "RD", Suffix: "S",
		Length: 5, Padding: "0", CharType: CharTypeNumber,
	}

	// 预置计数 50 后整体删除（计数 + floor 基准一起丢），确保并发起点是「冷启动」
	if _, err := g.Redis().Set(ctx, "seq_num:recover-race", 50); err != nil {
		t.Fatalf("预置计数失败: %v", err)
	}
	if _, err := g.Redis().Del(ctx, "seq_num:recover-race", "seq_num:recover-race:floor"); err != nil {
		t.Fatalf("清空计数失败: %v", err)
	}

	const n = 10
	results := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, err := NextCode(ctx, rule)
			if err != nil {
				t.Errorf("并发 NextCode[%d] 报错: %v", i, err)
				return
			}
			results[i] = code
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for i, code := range results {
		if code == "" {
			t.Fatalf("结果[%d] 为空", i)
		}
		// 剥前后缀解码序号
		seqPart := code[2 : len(code)-1]
		seq := codeToInt(seqPart, CharTypeNumber)
		if seq <= 100 {
			t.Fatalf("码 %s（序号 %d）未越过 DB 最大序号 100——重号窗口未关闭", code, seq)
		}
		if seen[code] {
			t.Fatalf("重复码: %s", code)
		}
		seen[code] = true
	}
	// 无重号不丢号前提下，计数可能因竞态自旋产生少量「放弃的 INCR」而高于 100+n
	// （放弃值在恢复提升时被原子覆盖，不发码）；下界收紧、上界放宽到合理范围
	cur, err := g.Redis().Get(ctx, "seq_num:recover-race")
	if err != nil {
		t.Fatalf("读取计数失败: %v", err)
	}
	if cur.Int() < 100+n || cur.Int() > 100+3*n {
		t.Fatalf("恢复后计数应在 [%d, %d]，实际 %d", 100+n, 100+3*n, cur.Int())
	}
	_ = mr
}

// TestNextCode_RecoverySequentialFollowsDBMax 单调用恢复：码紧跟 DB 最大码（无跳号）
func TestNextCode_RecoverySequentialFollowsDBMax(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()
	withFakeRecover(t, 100)

	rule := CodeRule{
		Name: "recover-single", Prefix: "RD", Suffix: "S",
		Length: 5, Padding: "0", CharType: CharTypeNumber,
	}

	first, err := NextCode(ctx, rule)
	if err != nil {
		t.Fatalf("NextCode 失败: %v", err)
	}
	if first != "RD00101S" {
		t.Fatalf("恢复后首码应为 RD00101S（DB最大100+1），实际 %s", first)
	}
	second, err := NextCode(ctx, rule)
	if err != nil {
		t.Fatalf("第二次 NextCode 失败: %v", err)
	}
	if second != "RD00102S" {
		t.Fatalf("第二次应为 RD00102S，实际 %s", second)
	}
}

// TestGetCurrentCode_NotIncrementing 读当前码：两次同值且不递增计数
func TestGetCurrentCode_NotIncrementing(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	rule := CodeRule{
		Name: "current-code", Prefix: "CC", Suffix: "S",
		Length: 5, Padding: "0", CharType: CharTypeNumber,
	}

	if _, err := g.Redis().Set(ctx, "seq_num:current-code", 100); err != nil {
		t.Fatalf("预置计数失败: %v", err)
	}
	// 恢复安全契约：预置计数须成对预置 floor 基准（其后 NextCode 断言依赖常规路径）
	if _, err := g.Redis().Set(ctx, "seq_num:current-code:floor", 100); err != nil {
		t.Fatalf("预置 floor 失败: %v", err)
	}

	a, err := GetCurrentCode(ctx, rule)
	if err != nil {
		t.Fatalf("GetCurrentCode 失败: %v", err)
	}
	b, err := GetCurrentCode(ctx, rule)
	if err != nil {
		t.Fatalf("第二次 GetCurrentCode 失败: %v", err)
	}
	if a == "" || a != b {
		t.Fatalf("两次读值应相同且非空: %q vs %q", a, b)
	}
	if a != "CC00100S" {
		t.Fatalf("当前码应为 CC00100S，实际 %s", a)
	}

	// 读操作不递增：其后 NextCode 应得 101
	next, err := NextCode(ctx, rule)
	if err != nil {
		t.Fatalf("NextCode 失败: %v", err)
	}
	if next != "CC00101S" {
		t.Fatalf("读码不应影响 NextCode，期望 CC00101S，实际 %s", next)
	}
}

// TestGetCurrentCode_EmptyWhenNoCounter 尚无计数时返回空串
func TestGetCurrentCode_EmptyWhenNoCounter(t *testing.T) {
	testredis.Start(t)
	ctx := context.Background()

	code, err := GetCurrentCode(ctx, CodeRule{Name: "never-used", CharType: CharTypeNumber})
	if err != nil {
		t.Fatalf("无计数场景不应报错: %v", err)
	}
	if code != "" {
		t.Fatalf("无计数应返回空串，实际 %q", code)
	}
}

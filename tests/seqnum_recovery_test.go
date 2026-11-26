//go:build integration

// seqnum DB 恢复集成测试（change: add-package-tests，task 6.1 完整实现）。
//
// 被测场景：Redis 序列号键不存在时，NextCode 首次 INCR 返回 1，
// 触发 recoverFromDB 从 MySQL 表查 MAX 编码、剥离 Prefix/Suffix、
// 按进制解码后 +1，并回写 Redis（见主库 seqnum/seqnum.go）。
//
// 注意：recoverFromDB 是私有函数，包外只能经公开入口 NextCode 触发，
// 因此测试设计为「预置 DB MAX 编码 → 清空 Redis 键 → 调 NextCode → 断言恢复值」。
//
// 环境要求（连接信息全部经环境变量注入，不硬编码凭证）：
//   - MYSQL_DSN（必填，缺失则 Skip）：格式如 root:pass@tcp(127.0.0.1:3306)/testdb；
//   - REDIS_ADDR（可选）：真实 Redis 地址如 127.0.0.1:6379；
//     未设置时自动降级为进程内 miniredis（集成测试允许两种形态）。
//
// 运行方式（见 doc.go 包注释）：
//
//	cd tests
//	MYSQL_DSN='root:pass@tcp(127.0.0.1:3306)/testdb' \
//	  GOWORK=off go test -tags integration ./... -v
package tests

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	// 注册 gf 的 mysql 驱动：g.DB() 依赖 gdb 驱动注册表，生产代码由接入方
	// 项目负责 import，测试进程需在此自行注册（同主库 internal/testredis
	// 对 redis adapter 的处理方式）。
	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"

	// 注册 gredis 的 go-redis adapter：缺少它 g.Redis() 因无 adapter 返回 nil
	// （gins 在首次 g.Redis() 时把 adapter 固化进包级单例，必须提前 import）。
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/alicebob/miniredis/v2"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/frame/g"

	"github.com/shyun-cc/shw-gf-lib/seqnum"
)

// miniredis 进程级单例：只启动一次、常驻不关闭。
//
// 原因：gins.Redis()（frame/g 包）会按分组缓存已创建的 *gredis.Redis 实例
// 且无法从包外清除，若每个测试换一个新实例（如 RunT 各自随机端口），
// 缓存中的旧实例会指向已关闭的 miniredis，后续 g.Redis() 全部失败。
// 同主库 internal/testredis 的教训，这里保持一致的进程级单例形态。
var (
	mrOnce     sync.Once
	mr         *miniredis.Miniredis
	mrStartErr error
)

// setupRedis 注入 GoFrame 默认分组的 Redis 配置：
// 设置了 REDIS_ADDR 则指向真实 Redis；未设置则降级为进程内 miniredis。
func setupRedis(t *testing.T) {
	t.Helper()

	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		gredis.SetConfig(&gredis.Config{Address: addr})
		return
	}
	mrOnce.Do(func() {
		mr, mrStartErr = miniredis.Run()
	})
	if mrStartErr != nil {
		t.Fatalf("启动 miniredis 失败（REDIS_ADDR 未设置时的兜底方案）: %v", mrStartErr)
	}
	gredis.SetConfig(&gredis.Config{Address: mr.Addr()})
}

// TestRecoverFromDB 验证 NextCode 的 DB 恢复路径：
// DB 中已存在 MAX 编码且 Redis 键丢失时，应从 MAX 恢复 +1 而非从 1 开始，
// 且恢复值回写 Redis 后的后续调用按常规自增。
func TestRecoverFromDB(t *testing.T) {
	// ① 环境检查：MySQL 连接串必填，缺失则跳过（不阻塞无环境的机器与 CI）
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 MYSQL_DSN（格式：用户:密码@tcp(主机:3306)/库名），跳过集成测试；运行方式见 doc.go 包注释")
	}
	ctx := context.Background()

	// ② 注入 g.DB() 配置：default 分组指向 MYSQL_DSN。
	// gins.Database 在 gdb.IsConfigured() 为真时不读配置文件，
	// 因此 SetConfig 后 g.DB() 直接生效，无需准备配置文件。
	if err := gdb.SetConfig(gdb.Config{
		gdb.DefaultGroupName: gdb.ConfigGroup{
			gdb.ConfigNode{Type: "mysql", Link: dsn},
		},
	}); err != nil {
		t.Fatalf("注入 gdb 配置失败: %v", err)
	}

	// ③ 注入 g.Redis() 配置：REDIS_ADDR 真实 Redis，否则 miniredis 兜底
	setupRedis(t)

	// 连通性预检：失败时给出可操作的提示（docker 一键命令），而非驱动深处的模糊报错
	if err := g.DB().PingMaster(); err != nil {
		t.Fatalf("MySQL 连接失败（DSN 来自 MYSQL_DSN 环境变量）: %v；"+
			"可用 docker 快速准备：docker run -d -p 3306:3306 -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=testdb mysql:8", err)
	}
	if _, err := g.Redis().Do(ctx, "PING"); err != nil {
		t.Fatalf("Redis 连接失败: %v；可设置 REDIS_ADDR 指向真实 Redis，或留空由 miniredis 兜底", err)
	}

	// 用例表：三个进制各一例。rows 为递增灌入的存量编码（末行即 MAX，
	// recoverFromDB 按 code DESC 取首行）；所有编码等长，保证字符串序 = 数值序。
	cases := []struct {
		name  string
		rule  seqnum.CodeRule
		rows  []string // 存量编码（2-3 行递增，确保 MAX 语义）
		want1 string   // 第一次 NextCode：走 DB 恢复，期望 MAX+1 编码
		want2 string   // 第二次 NextCode：恢复值回写 Redis 后的常规自增
	}{
		{
			// 十进制主路径：MAX="PO00100S" → 剥 PO/S 得 "00100" → 100+1=101 为恢复基准
			// → 首码越过基准：102 → Padding="0" 补齐 5 位 → "PO00102S"
			name: "十进制-MAX恢复并回写Redis",
			rule: seqnum.CodeRule{
				Name:       "integration_test_recover", // 对应 Redis 键 seq_num:integration_test_recover
				Prefix:     "PO",
				Suffix:     "S",
				Length:     5,
				Padding:    "0",
				CharType:   seqnum.CharTypeNumber,
				TableName:  "integration_test_seqnum", // 临时表，测试自建自清理
				ColumnName: "code",
			},
			rows:  []string{"PO00098S", "PO00099S", "PO00100S"},
			want1: "PO00102S",
			want2: "PO00103S",
		},
		{
			// 36 进制跨位进位：MAX="OD00ZZS" → "00ZZ"=1295 → 恢复基准 1296
			// → intToCode="100"（两位进三位）补齐 "0100"；首码越过基准：1297="0101" → "OD0101S"
			name: "三十六进制-跨位进位恢复",
			rule: seqnum.CodeRule{
				Name:       "integration_test_recover_36",
				Prefix:     "OD",
				Suffix:     "S",
				Length:     4,
				Padding:    "0",
				CharType:   seqnum.CharTypeBoth,
				TableName:  "integration_test_seqnum_36",
				ColumnName: "code",
			},
			rows:  []string{"OD00Z9S", "OD00ZZS"}, // "Z9"=1269 < "ZZ"=1295
			want1: "OD0101S",
			want2: "OD0102S",
		},
		{
			// 26 进制（A=0）：MAX="AAAZR" → 剥后缀 R 得 "AAAZ"=25 → 恢复基准 26
			// → intToCode="BA"（Z 后进位）补齐 "AABA"；首码越过基准：27="AABB" → "AABBR"
			name: "二十六进制-Padding字母补齐",
			rule: seqnum.CodeRule{
				Name:       "integration_test_recover_26",
				Prefix:     "",
				Suffix:     "R",
				Length:     4,
				Padding:    "A",
				CharType:   seqnum.CharTypeLetter,
				TableName:  "integration_test_seqnum_26",
				ColumnName: "code",
			},
			rows:  []string{"AAAYR", "AAAZR"}, // "Y"=24 < "Z"=25
			want1: "AABBR",
			want2: "AABCR",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			redisKey := "seq_num:" + c.rule.Name

			// ③-前置：删除 Redis 计数键与 floor 基准缓存键，模拟 Redis 丢数据
			// （首次 INCR=1 才会触发 DB 恢复；floor 键残留会使判定走「未越过基准」
			//   路径而非「计数丢失」路径，删两键才贴合丢数据语义——真 Redis 持久，
			//   miniredis 时代进程级单例掩盖了这一点）
			if _, err := g.Redis().Del(ctx, redisKey); err != nil {
				t.Fatalf("预清理 Redis 键 %s 失败: %v", redisKey, err)
			}
			if _, err := g.Redis().Del(ctx, redisKey+":floor"); err != nil {
				t.Fatalf("预清理 Redis floor 键 %s 失败: %v", redisKey+":floor", err)
			}

			// ① 建临时表：先 DROP 旧表保证可重复执行；id 自增主键 + code varchar(32)
			dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s", c.rule.TableName)
			if _, err := g.DB().Exec(ctx, dropSQL); err != nil {
				t.Fatalf("预清理旧表失败 table=%s: %v", c.rule.TableName, err)
			}
			createSQL := fmt.Sprintf(
				"CREATE TABLE %s (id INT AUTO_INCREMENT PRIMARY KEY, %s VARCHAR(32) NOT NULL)",
				c.rule.TableName, c.rule.ColumnName)
			if _, err := g.DB().Exec(ctx, createSQL); err != nil {
				t.Fatalf("建临时表失败 table=%s: %v", c.rule.TableName, err)
			}

			// ①-续：灌入递增存量编码，末行为 MAX
			insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (?)", c.rule.TableName, c.rule.ColumnName)
			for _, code := range c.rows {
				if _, err := g.DB().Exec(ctx, insertSQL, code); err != nil {
					t.Fatalf("灌入存量编码 %q 失败: %v", code, err)
				}
			}

			// ⑥ 幂等清理：测试结束 DROP 临时表 + Del Redis 键
			t.Cleanup(func() {
				if _, err := g.DB().Exec(ctx, dropSQL); err != nil {
					t.Errorf("清理临时表失败 table=%s: %v", c.rule.TableName, err)
				}
				if _, err := g.Redis().Del(ctx, redisKey, redisKey+":floor"); err != nil {
					t.Errorf("清理 Redis 键 %s 失败: %v", redisKey, err)
				}
			})

			// ④ 调 NextCode：键不存在 → INCR=1 → recoverFromDB 取 MAX 剥前后缀解码 +1 得恢复值，
			// 再把计数「推过恢复值」（spec seqnum-recovery-safety：恢复后从恢复值之上继续，
			// 发出的码严格 > 恢复基准 floor=MAX+1）——故首码 = MAX+2，第二个 = MAX+3
			got1, err := seqnum.NextCode(ctx, c.rule)
			if err != nil {
				t.Fatalf("第一次 NextCode 失败: %v", err)
			}
			// ⑤ 断言首码 = MAX+2（严格越过恢复基准 MAX+1，而非从 1 重新开始）
			if got1 != c.want1 {
				t.Fatalf("第一次 NextCode = %q, 期望 DB 恢复值 %q", got1, c.want1)
			}

			// 第二次调用：验证恢复值已回写 Redis，此后按常规自增（不再走 DB）
			got2, err := seqnum.NextCode(ctx, c.rule)
			if err != nil {
				t.Fatalf("第二次 NextCode 失败: %v", err)
			}
			if got2 != c.want2 {
				t.Fatalf("第二次 NextCode = %q, 期望 %q（恢复值回写 Redis 后的常规自增）", got2, c.want2)
			}
		})
	}
}

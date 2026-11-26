# seqnum — 原子编码生成器（Redis INCR + DB 恢复）

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `seqnum/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

seqnum 是原子编码生成器（对应 PHP CodeRule），生成「前缀 + 定长序号 + 后缀」形态的业务编码：订单号 `PO00101`、合同号带尾缀 `RD00101S` 这类需要**全局唯一且递增**的字符串标识。

三件事在一处解决：

- **原子性**：序号取自 Redis `INCR`（`seq_num:<Name>` 计数键），多实例并发取号不重号；
- **丢数据自愈**：Redis 计数丢失（清库 / 过期 / 首次冷启动）时，以 DB 里该规则已发出的**最大码**为基准互斥恢复，恢复期间不发出任何历史已用码（防重号机制见第 4 节）；
- **编码规则**：36 / 10 / 26 三进制编码 + 首字符范围校验 + 前后缀与补齐拼装，规则由 `CodeRule` 结构逐字段声明。

它管「号怎么发」，不管「号怎么用」——编码落到哪张表、业务含义是什么由项目定义；恢复基准恰恰从那张表读最大码，所以 `TableName` / `ColumnName` 是规则的一部分。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节），gf Redis 与数据库配置就绪（`g.Redis()` / `g.DB()` 可用；DB 仅在恢复路径触达，常规取号只打 Redis）。

```go
package controller

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/response"
	"github.com/shyun-cc/shw-gf-lib/seqnum"

	"github.com/gogf/gf/v2/net/ghttp"
)

// orderNoRule 订单号规则：PO + 5 位 10 进制序号（PO00001、PO00002……）。
// TableName/ColumnName 指向存码的业务表——Redis 丢数据时从该表最大码恢复
var orderNoRule = seqnum.CodeRule{
	Name:       "order_no",
	Prefix:     "PO",
	Suffix:     "",
	Length:     5,
	Padding:    "0",
	CharType:   seqnum.CharTypeNumber,
	TableName:  "orders",
	ColumnName: "order_no",
}

// CreateOrder 取号下单：NextCode 每次返回全新编码（并发安全）
func CreateOrder(r *ghttp.Request) {
	code, err := seqnum.NextCode(r.Context(), orderNoRule)
	if err != nil {
		// 错误链可解出底层原因；Redis 故障 / 恢复超时的 code 为 99901
		response.WriteException(r, exception.NewInfrastructure(50001, "订单号生成失败"))
		return
	}
	// ...以 code 落库 orders 表（项目自身逻辑）...
	response.Success(r, g.Map{"order_no": code})
}

// PreviewCurrentCode 读当前编码（不递增计数）：连续调用返回相同值
func PreviewCurrentCode(ctx context.Context) (string, error) {
	return seqnum.GetCurrentCode(ctx, orderNoRule)
}
```

## 3. CodeRule 参数语义

`seqnum.CodeRule` 十个字段（每次调用随参传入，无全局 SetConfig）：

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `Name` | `string` | 规则名，决定 Redis 计数键 `seq_num:<Name>`（与恢复基准键 `seq_num:<Name>:floor`）——同规则的 `Name` 必须全局一致，不同规则不可同名 |
| `Prefix` | `string` | 编码前缀，拼在序号前（如 `PO`） |
| `Suffix` | `string` | 编码后缀，拼在序号后（如 `S`） |
| `Length` | `int` | 序号段目标长度，不足用 `Padding` 左补；序号已超长时**只补不截**（如 5 位规则发到第 123456 号，输出 6 位序号） |
| `Padding` | `string` | 补齐字符。数字进制用 `"0"`；26 进制字母编码用 `"A"`（A 即 0） |
| `CharType` | `int` | 进制：`CharTypeBoth = 36`（字典 `0-9A-Z`）/ `CharTypeNumber = 10`（`0-9`）/ `CharTypeLetter = 26`（`A-Z`，A=0）。零值等未列值走 36 进制 |
| `TableName` | `string` | 恢复基准表：存该编码的业务表名。恢复时按 `ColumnName DESC` 取最大码 |
| `ColumnName` | `string` | 恢复基准列：该表存编码的列名 |
| `FcMin` | `int` | 首字符允许的字典索引下界（36 进制中 `0`-`9` 为 0-9、`A`-`Z` 为 10-35；26 进制 `A`-`Z` 为 0-25） |
| `FcMax` | `int` | 首字符允许的字典索引上界。`FcMin`/`FcMax` 均为 0 视为未配置，跳过校验 |

**首字符校验语义**：配置了范围后，生成的编码首字符索引落在 `[FcMin, FcMax]` 才放行，越界（含字典外字符）返回错误「首字符 %c 索引 %d 超出允许范围 [%d, %d]」。用途是「编码必须字母开头」一类展示规则——注意它只校验不跳号，越界时调用收到的是错误而非跳到下一个合法码。

## 4. 契约

**取号主路径**：`NextCode` 对 `seq_num:<Name>` 执行 `INCR`，结果序号在恢复基准（floor 缓存）之上时直接编码返回——单次 Redis 往返，高并发下线性吞吐。

**丢数据互斥恢复（防重号核心）**：`INCR` 结果无法确认越过 DB 已发最大码时（冷启动首次取号、Redis 计数被清、floor 基准未建立），进入互斥恢复：

1. 抢 [redislock](redislock.md) 恢复锁（`seqnum-recover:<Name>`，TTL 10 秒）；
2. 抢到者读 DB 最大码（`SELECT <ColumnName> FROM <TableName> ORDER BY <ColumnName> DESC LIMIT 1`，剥前后缀解码 +1；表空或读失败按 1 起）；
3. 以 Lua 脚本原子提升计数（仅当现值小于基准时写入基准，**绝不覆盖更大的现值**），再 `INCR` 取得越过基准的码；
4. 在锁内 `SETNX` 写入 floor 基准缓存——后续调用读缓存快速判定，不再进锁；
5. 未抢到锁者每 50 毫秒自旋重入，累计超过 5 秒返回错误「序列号恢复等待超时（>%s）rule=%s」（code `CodeSeqNumGenFailed`，即 99901）。

正确性不变量：**任何发出的码都大于 DB 恢复基准**；floor 缓存只由恢复流程在恢复锁内写入（并发半途的计数现值不可靠，据其缓存会把基准压低导致重号）。

**读当前码**：`GetCurrentCode` 返回当前计数对应的完整编码，不递增计数——连续调用返回相同值；尚无计数（首次 `NextCode` 之前）返回空串。可用于「下一个号是什么」的展示场景，但取号一律走 `NextCode`。

**进制编码**：序号→编码按字典逐位转换（36 进制 `9`→`A` 在 10 处进位、`Z` 后进位 `10`；26 进制 A=0，`Z` 后是 `BA`）；编码→序号反向解码用于恢复时解析 DB 最大码，字典外字符与空串解出 0（按基准 1 起）。

**Redis / DB 键与触达**：

| 键 | 用途 | 触达时机 |
| --- | --- | --- |
| `seq_num:<Name>` | 计数器 | 每次取号 INCR |
| `seq_num:<Name>:floor` | 恢复基准缓存 | 恢复锁内写，取号时读 |
| `seqnum-recover:<Name>`（redislock 键，实际为 `redis-lock:seqnum-recover:<Name>`） | 恢复互斥 | 仅恢复路径 |

## 5. FAQ

**Q：Redis 被清空 / 挂了重启，会发出重复编码吗？**

不会，这是本组件的核心契约。计数丢失后第一次取号触发互斥恢复：以 DB 最大码为基准把计数推过去，恢复期间（含并发取号）发出的码全部大于历史已用码。前提是**编码确实落在了 `TableName` 指向的表里**——取了号没落库的「弃号」会被跳过（不回收），不影响不重号。

**Q：恢复会不会造成号段大跳号？**

正常情况不跳：恢复后首码恰为「DB 最大码 +1」（如 DB 最大 `RD00100S`，恢复后首码 `RD00101S`）。唯一例外是恢复期间并发取号的竞态自旋可能产生少量被放弃的 INCR 值（被原子提升覆盖、不发码），表现为极小的跳号，属安全裕量。

**Q：Length 填 5，发到 99999 之后呢？**

序号段升为 6 位继续发（只补不截），编码变为 `PO100000` 形态。`Length` 是最小长度不是上限——若业务要求编码严格定长（不容忍升位），需在接近上限时人工干预规则（换 `Name` 起跳新段等，属项目决策）。

**Q：FcMin/FcMax 配置后首字符越界，是自动跳到下一个合法码吗？**

不是。越界时 `NextCode` 返回错误（不吞错、不跳号），调用方按业务决定重试或换规则。「强制字母开头」类需求建议直接用 26 进制 + `Padding: "A"`，而非 36 进制配范围校验。

**Q：多套编码规则（订单号 / 合同号 / 入库单号）怎么定义？**

各定义一个 `CodeRule`，`Name` 互不相同（Redis 计数键按 `Name` 隔离），`TableName`/`ColumnName` 各指向自己的业务表。规则是纯数据结构，可以包级变量持有也可以每次构造。

**Q：GetCurrentCode 能代替 NextCode 取号吗？**

不能。`GetCurrentCode` 只读不递增，两次调用同值——拿它当号发必然重号。它只用于展示/对账「当前发到哪了」。

**Q：集成环境怎么验证恢复路径？**

`tests/seqnum_recovery_test.go` 是真 MySQL 的集成测试（`-tags integration` 构建，默认不触发）：在 `tests/` 目录（独立 go module）下设 `MYSQL_DSN`（必填，缺失自动 Skip）与 `REDIS_ADDR`（可选，未设降级 miniredis），`GOWORK=off go test -tags integration ./...` 运行。

## 6. 维护者注记

**算法逐位翻译的教训**：本包从 PHP CodeRule 收编，36/10/26 进制与首字符校验是逐位翻译并逐值测试的（边界值 9/10、35/36、25/26、1295/1296、675/676 全部表驱动锁死）——进制转换的「看起来差不多」是最容易漂的地方，改动 `intToCode` / `codeToInt` 必须连带边界用例。

**恢复路径的注入点**：`recoverFromDBFn` 是同包变量（生产为 `g.DB()` 直连实现，单测替换为伪实现覆盖并发恢复安全），对外不可见。改动恢复流程时并发不重号测试（10 并发全大于 M 且互不相同）是回归闸门。

**floor 缓存只由恢复锁内写入**是防重号不变量的支点：取号路径绝不写 floor（并发半途读到的计数现值不可靠）。任何「顺手在读路径补缓存」的优化都会打开重号窗口，属禁止改动。

**测试形态**（`seqnum/seqnum_test.go`，miniredis）：三进制互转全边界、round-trip、首字符校验全分支、连续自增与拼装（预置计数 + floor 成对预置绕开 DB）、并发恢复不重号（伪 DB 基准 M=100，10 并发全 >100 且互异）、单调用恢复紧跟 DB 最大码、GetCurrentCode 不递增与无计数空串。集成层（`tests/seqnum_recovery_test.go`，`-tags integration`）以真 MySQL 验证恢复基准的 SQL 形态。

**错误码约定**：恢复等待超时用 `exception.CodeSeqNumGenFailed`（99901，见 [docs/exception.md](exception.md) 十六段表）；其余错误（Redis INCR 失败等）为 `fmt.Errorf` 包装，`%w` 链可解底层原因。

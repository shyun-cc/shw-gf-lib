# cryptox — 前端通讯与数据库字段加密统一封装

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `cryptox/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

cryptox 是前端通讯加密与数据库字段加密的通用封装（AES-256-GCM），把「加解密怎么做」收敛为一处：密文字节排列、版本前缀、密钥派生规则都定死在包内，前端 JS 与 Go 端按同一契约对接（完整契约见第 4 节，前端只需照它实现解密，不必读 Go 源码）。

两条使用面：

- **前端通讯加密**：敏感字段（手机号、身份证号）出参加密、入参解密；
- **数据库字段加密**：落库前 `Encrypt`、读出后 `Decrypt`，配合 `IsEncrypted` 判定字段是否已是密文形态。

它**不负责**密钥管理——密钥从哪来（环境变量、配置中心）是项目决策；也不做 PHP 互操作。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节）。本组件无 Redis / DB / gf 配置依赖。密钥 ≥32 字节（主路径）时**零初始化**——不调任何 `SetConfig`，直接使用：

```go
// 密钥来自项目自身的环境变量（≥32 字节随机值，如 openssl rand -base64 32 的输出）
func encryptValue(plaintext string) (string, error) {
	return cryptox.Encrypt(plaintext, os.Getenv("APP_AES_KEY"))
}
```

仅当使用 <32 字节短密钥（**已废弃路径**，见第 4 节与第 6 节血统注记）时需要一次 boot 层初始化——该路径计划 v1.0 移除，存量用法维持行为不变：

```go
package boot

import (
	"github.com/shyun-cc/shw-gf-lib/cryptox"
)

// InitCryptox 短密钥路径（已废弃）的 Salt 配置：两端必须一致；无默认值，
// 不配置则短密钥路径报错。≥32 字节密钥不需要本函数。
func InitCryptox() {
	cryptox.SetConfig(cryptox.Config{
		Salt: "项目自定的固定盐值",
	})
}
```

业务侧使用（controller 出入参加解密）：

```go
package controller

import (
	"context"
	"os"

	"github.com/shyun-cc/shw-gf-lib/cryptox"
	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/response"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
)

// 从环境变量读取至少 32 字节的随机密钥。
var cryptoKey = os.Getenv("APP_CRYPTO_KEY")

// writeProfile 返回敏感字段时加密出参：前端按第 4 节契约解密展示
func writeProfile(r *ghttp.Request, storedPhone string) {
	phone, err := cryptox.Encrypt(storedPhone, cryptoKey)
	if err != nil {
		response.WriteException(r, exception.NewInfrastructure(50001, "手机号加密失败"))
		return
	}
	response.Success(r, g.Map{"phone": phone})
}

// UpdateProfile 入参解密（前端上传 v1: 密文）；数据库字段加密同形态：
// 落库前 Encrypt、读出后 Decrypt，IsEncrypted 判定是否已是密文
func UpdateProfile(r *ghttp.Request) {
	encrypted := r.Get("phone").String()
	plain, err := cryptox.Decrypt(encrypted, cryptoKey)
	if err != nil {
		response.WriteException(r, exception.NewDomain(42202, "手机号解密失败"))
		return
	}
	// ...用 plain 落库/校验（项目自身逻辑）...
	_ = plain
	response.Success(r, g.Map{})
}

// maybeDecrypt 存量数据兼容示例：新数据是密文（v1: 前缀或裸格式），旧数据是明文
func maybeDecrypt(ctx context.Context, stored string) string {
	if cryptox.IsEncrypted(stored) {
		if plain, err := cryptox.Decrypt(stored, cryptoKey); err == nil {
			return plain
		}
	}
	return stored
}
```

## 3. Config 参数语义

`cryptox.SetConfig(cfg Config)` 的两个字段（boot 层初始化时调用一次；零值字段跳过更新——`Salt == ""`、`Iterations <= 0` 不生效）。**两个字段均只作用于已废弃的短密钥（<32 字节）派生路径**，godoc 已标 `Deprecated`（计划 v1.0 随该路径一并移除）；≥32 字节密钥的主路径不读取任何配置：

| 字段 | 类型 | 必填 | 语义 |
| --- | --- | --- | --- |
| `Salt`（Deprecated） | `string` | 短密钥必填 | PBKDF2 盐值，仅在密钥 <32 字节的派生路径使用。无默认值，不配置时短密钥路径 `Encrypt` / `Decrypt` 返回错误「使用 <32 字节密钥时必须配置 Salt：请调用 SetConfig 设置 Salt，或改用 ≥32 字节密钥」。**Salt 是公开的 KDF 参数（防预计算表用），不承担保密** |
| `Iterations`（Deprecated） | `int` | 可选 | PBKDF2 迭代次数，默认 `600000`，需要更强/更弱算力时覆盖 |

## 4. 契约

**密文格式（对外契约，前端 JS 按此对接）**：

```
"v1:" + base64( nonce[12] + tag[16] + ciphertext )
```

- `nonce`：12 字节随机数，每次加密新生成（同一明文每次加密结果不同）；
- `tag`：16 字节 GCM 认证标签；
- `ciphertext`：密文，长度与明文字节相同；
- 前缀 `v1:` 自 v1 起携带（base64 标准字符集不含冒号，前缀无歧义）。

**前端解密视角四步**：剥离 `v1:` 前缀 → base64 标准解码 → 按 `[0:12]` 取 nonce、`[12:28]` 取 tag、`[28:]` 取 ciphertext → AES-256-GCM 解密（密钥按下面的派生规则得到）。

**密钥派生规则（两端必须一致）**：

| 密钥长度 | 规则 | Salt |
| --- | --- | --- |
| ≥32 字节（主路径） | 直接取前 32 字节作为 AES-256 密钥（不派生） | 不需要 |
| <32 字节（**已废弃**，v1.0 移除） | `PBKDF2-SHA256(key, salt, iterations)` 派生 32 字节密钥 | 必须 SetConfig 配置，否则报错 |

**密钥与盐的职责边界（机制事实）**：密钥是秘密，由调用方从环境等渠道注入（进入代码库即视为泄露）；Salt 是公开的 KDF 参数，职责是防预计算表，不承担保密。≥32 字节路径不经过 Salt，单次加解密为微秒级；<32 字节路径每次派生完整跑 PBKDF2（600000 迭代，百毫秒级）。「公开常量当密钥 + 把 Salt 当秘密注入」曾作为误用形态被识别（密钥保密性完全落在 Salt 上，等于换名的密钥），见第 6 节血统注记。

**`base64:` 前缀约定**：本包不识别密钥上的 `base64:` 前缀——对齐上游 Laravel（前缀剥离是调用方职责，其 Encrypter 同样不感知前缀）。密钥采用 base64 形态的项目（如 Laravel `APP_KEY` 习惯）由调用方自行解码后传入解码值（解码后 ≥32 字节即走主路径）。

**黄金测试向量（跨语言对账锚）**：三条固定 key/明文/密文三元组，密文由本包 `Encrypt` 生成后固化（内嵌随机 nonce，对账方向为**解密**——用给定 key 解给定密文须还原给定明文，并按四步切分核验结构）。任何语言实现对账通过即与 Go 端契约一致：

| # | key | 明文 | 密文 |
| --- | --- | --- | --- |
| 1 | `0123456789abcdef0123456789abcdef`（32 字节） | `shw-cryptox golden vector #1` | `v1:0/zbcGlse7IH3QXdVK54aigtvtXrlHhhWTEvC0eCQXNUYP72DLsVZq4MgofVFSBN6//N0FFdblU=` |
| 2 | `golden-vector-key-2-base64-shape-aaaaaaaaaaaa==`（47 字符，取前 32） | `黄金向量二：中文明文与多字节字符` | `v1:5QXx8+t07wh/5FO4kIYrAXEj1T5EL+shHak6z4Of1MWRSQlKZk12cWREzAyoc09V8T+mnEWgdhVxrGHeHePTwP9HA+LoR4mGSH4hog==` |
| 3 | `golden-vector-key-3-bare-format-40-bytes-xxxx` | `bare format vector` | `otIHpeQaiKtqEvOLfHRcV3aP7xPwFF8nS3J25JdOeHiF15pxHc3nNTj+2UtBdQ==`（v0.0.3 裸格式，无前缀） |

向量同时在 `cryptox/golden_test.go` 作为回归锁——排列、派生或前缀的任何改动都会使该测试变红；契约升版（`v2:` 等）重铸向量用生成器（见该文件内说明）。

**版本兼容**：`Encrypt` 只输出带 `v1:` 前缀的格式；`Decrypt` 同时接受带前缀与**无前缀**（v0.0.3 裸格式，字节排列与 v1 相同）的输入；`IsEncrypted` 识别两种格式（剥离前缀后 base64 解码成功且 ≥29 字节——nonce[12] + tag[16] + 至少 1 字节密文）。

**错误行为**：密文太短（base64 解码后不足 `nonceSize+16` 字节）返回错误「密文太短」；密钥不匹配时 GCM 认证失败返回解密错误，**不会**返回错乱明文。

## 5. FAQ

**Q：忘了调 SetConfig 会怎样？**

密钥 ≥32 字节：完全不受影响（主路径不依赖 Salt，也不需要 SetConfig）。密钥 <32 字节（已废弃路径）：`Encrypt` / `Decrypt` 都返回错误「使用 <32 字节密钥时必须配置 Salt：请调用 SetConfig 设置 Salt，或改用 ≥32 字节密钥」——明确报错优于静默用弱默认盐产出密文（历史上不存在弱默认 salt，这是 v0.1.0 起的强制要求）。

**Q：为什么 ≥32 字节密钥不派生、直接截断？**

32 字节恰好是 AES-256 密钥长度，截断即用、无算力开销；短密钥熵不足才需要 PBKDF2 拉伸（600000 次迭代是对离线爆破的算力门槛）。该形态与上游 Laravel Encrypter 的严格密钥长度纪律同构（本包血统上游，见第 6 节）——随机强钥不需要任何「拉伸」工序，KDF 只为弱材料服务。

**Q：密钥该怎么选、Salt 需要保密吗？**

机制事实：≥32 字节随机值（如 `openssl rand -base64 32` 的输出，44 字符）走主路径，零初始化、微秒级单次开销；Salt 只存在于已废弃的短密钥路径，其职责是防预计算表而非保密（盐泄露不降低主路径安全性——主路径根本不使用盐）。密钥进入代码库即视为泄露；「源码常量当密钥 + 环境注入 Salt」的形态保密性完全落在 Salt 上，是被识别过的误用。

**Q：其他语言（前端 JS 等）怎么确认实现正确？**

用第 4 节黄金测试向量对账：给定 key 解给定密文，还原出给定明文、四步切分结构核验通过，即契约一致。向量是多语言契约的可执行锚，比 prose 描述可靠。

**Q：同一明文加密两次结果一样吗？**

不一样。每次加密生成新 nonce，密文必然不同。判断一个值是否为本包密文用 `IsEncrypted`（格式判定），不要拿「加密结果相等」当判据。

**Q：数据库里混存着明文旧数据和密文新数据，读出来怎么处理？**

先 `IsEncrypted(stored)` 判定：是密文再 `Decrypt`，否则按明文直接用（第 2 节 `maybeDecrypt` 示例）。新写入一律 `Encrypt`。

**Q：前端 JS 怎么对接？**

照第 4 节契约实现：剥 `v1:` 前缀、base64 解码、按偏移切 nonce/tag/ciphertext、AES-256-GCM 解密。密钥与 Salt（若走短密钥路径）由项目按自身安全策略下发，不属于本包范畴。

**Q：能解 v0.0.3 时代加密的存量密文吗？**

能。v0.0.3 是无前缀裸格式，字节排列与 v1 相同（nonce + tag + ciphertext），`Decrypt` / `IsEncrypted` 均兼容；只需保证密钥与 Salt 与加密时一致。

**Q：换密钥后旧密文还能解吗？**

不能——GCM 认证失败。密钥轮换需项目自行规划（新旧密钥并行期的数据归属、重加密迁移），本包不内置轮换机制。

## 6. 维护者注记

**格式演进纪律**：字节排列、前缀、派生规则是对外契约（前端 JS 与存量密文双重锁定），任何改动都是破坏性变更，走 shw-spec change 流程并升版本前缀（`v2:` 等），不允许在 v1 排列内做「悄悄调整」。

**血统与短钥废弃（2026-09-01，issue #2）**：本包 AES 形态的传承链是 Laravel Encrypter（严格 `==16/32` 字节钥匙纪律、无 KDF、`base64(JSON{iv,value,mac,tag})` 封装）→ hyperf-lib 时代一次凭理解的复写跑偏（JSON 信封丢失、凭空引入 PBKDF2+盐、二进制拼接封装）→ gf-lib Go 复刻忠实继承。本地可见的每一跳演化都在给「盐」这个上游不存在的东西抛光（硬编码盐 @1 万轮 → 可配置 @60 万轮 → Go 侧强制 Salt + 友好报错），无一跳回问上游。「容忍短钥」的便利性正是 xlzb 以盐代钥倒置用法的门——故短密钥路径文档级废弃（运行时行为不变，计划 v1.0 移除），新接入一律 ≥32 字节随机密钥。同日定案的连带裁定：hyperf-lib 为零生产消费者孤儿仓库（AES 类六仓库唯一调用方是其自带测试），「不偏离 hyperf-lib」对本包盐设计不再构成约束；封装排列 `nonce‖tag‖ct` 维持不动（排列任意、定长 28B 头有解析优点，无收益不折腾）；`base64:` key 前缀对齐 Laravel = 调用方剥离，库内不做。

**包名是 cryptox 而非 crypto**：避免与标准库 `crypto/` 命名冲突——项目侧同时 import 两者时无需别名。这是 v0.1.0 的 BREAKING 改名（原 `crypto` 包名）。

**字节重排的 append 别名教训**：Go GCM `Seal` 返回 `nonce + ciphertext + tag`（tag 在末尾），而契约排列是 `nonce + tag + ciphertext`，需要重排。重排必须写入**独立缓冲区**——`sealed` 的子切片 cap 共享底层数组，直接 `append` 会把 tag 就地写进 ciphertext 区域，曾导致 round-trip 解密必然失败。解密侧 `Open` 需要 `ciphertext + tag` 拼接，同理拷贝到独立缓冲区。

**测试覆盖**（`cryptox/aes_test.go` + `cryptox/golden_test.go`，纯逻辑无外部依赖）：round-trip（含中文明文、超 16 字节多段 append、40 字节密钥截断路径）、错误密钥 GCM 认证失败、`v1:` 前缀两种密钥路径均携带、无前缀裸格式兼容解密（按 v0.0.3 逻辑独立构造密文，非剥前缀造假）、未配 Salt 的错误路径（Encrypt/Decrypt 双向）、长密钥不依赖 Salt、`IsEncrypted` 双格式识别与非法 base64 拒判；黄金向量回归锁（三条 ≥32 字节密钥向量：32 字节精确、47 字符截断、v0.0.3 裸格式；生产 Encrypt 生成固化，排列/派生/前缀任何改动即红）+ 一次性向量生成器（`GEN_GOLDEN=1` 启用，契约升版重铸用）。

# migrate — 数据库迁移执行引擎

> 本文档是 docs/ 目录的第一份组件文档，为后续组件文档定调。面向两类读者：**项目侧接入者**读第 1–6 节（怎么用）；**库维护者**加读第 7 节（为什么这样设计）。事实来源是 `migrate/` 源码，本文的代码示例、签名、Brief 与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

migrate 封装 goose（`github.com/pressly/goose/v3`），提供迁移引擎和命令。调用方维护迁移 SQL 与 embed 声明。

三条边界，全部由代码结构保证：

- **导出 API 零 goose 依赖**：goose 类型只出现在 `migrate/migrate.go` 内部，项目源码不需要 import goose；
- **首版仅 PostgreSQL**：`Dialect` 枚举当前只有 `DialectPostgres`；
- **仅增量 up 与 dev 清库重建**：不提供 down / status / version 等子命令（上游两项目只用这两种能力，收编时有意收窄，明确不存在）。

在 goose 之上，本组件对迁移文件实施裸 goose 不存在的机器强制——完整性对账（R1/R2）、粒度（R4）、文件名格式（R7），见第 3 节。这是从「文档风格约定」升格的机制条款：由代码在构造期与执行期强制，无开关、无豁免路径。

goose v3.27.3 是本库首个重基础设施依赖（go.mod 因此显著变胖），这是统一迁移引擎的既定取舍；`github.com/pganalyze/pg_query_go/v6`（v6.2.5，PostgreSQL 服务器语法解析器的 Go 绑定，Cgo 包）是第二个——粒度机器判定引入的既定取舍，构建前提见第 2 节。

## 2. 快速接入（四段式）

前提：

1. 项目已完成 gf-lib 接入（见仓库 README 第 2 节），且 gf 数据库配置就绪（`g.DB()` 可用，本例为 PostgreSQL）。项目侧额外需要 gf 的 PostgreSQL 驱动（项目已引入则跳过）：

   ```bash
   go get github.com/gogf/gf/contrib/drivers/pgsql/v2
   ```

2. **构建环境需要 CGO_ENABLED=1 与 C 工具链**（本组件是 Cgo 包，原因见下）。

**Cgo 构建前提**：粒度判定经 pg_query_go 解析迁移 SQL（见第 3 节），该依赖包含随 Go 包编译的 PostgreSQL 解析器 C 源码——import 本组件后，项目构建需要 `CGO_ENABLED=1` 与构建机上的 C 工具链：

- 本机原生构建：机器装有 C 编译器即可（macOS 的 clang、主流 Linux 发行版的 gcc 均自带）；官方 `golang` Docker 镜像自带 C 工具链，Linux/glibc 环境无需额外安装；
- 交叉编译：Go 工具链在交叉编译时默认禁用 CGO，且预编译 C 库须与目标平台匹配——跨平台发布在**目标平台的构建容器内**完成；
- CGO 被禁时的构建失败形态是**符号缺失**，实测形如：

  ```text
  # github.com/shyun-cc/shw-gf-lib/migrate
  migrate/granularity.go:91:23: undefined: pgquery.Parse
  ```

  这个报错有误导性——看似组件 API 不存在，实为 pg_query_go 的 C 部分未参与编译。撞上它先查 `CGO_ENABLED` 与 C 工具链，报错本身与 API 是否存在无关。

项目侧目录结构（迁移 SQL 属项目资产，文件名受 R7 机器校验，见第 3 节）：

```
项目根/
├── main.go
└── migrations/
    ├── 00001_create_users.sql
    └── 00002_add_user_index.sql
```

main.go 四段接线（完整可复制）：

```go
package main

import (
	"embed"
	"io/fs"

	// gf 的 PostgreSQL 驱动（迁移经 g.DB() 取连接；项目已引入则此行已存在）
	_ "github.com/gogf/gf/contrib/drivers/pgsql/v2"

	"github.com/gogf/gf/v2/os/gcmd"
	"github.com/gogf/gf/v2/os/gctx"

	"github.com/shyun-cc/shw-gf-lib/migrate"
)

// ① embed 声明：把项目 migrations/ 目录下的 SQL 编进二进制
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	// ② 从 embed 根取出 migrations 子目录（embed 指令不支持目录本身，须经 fs.Sub）
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err)
	}

	// ③ 构造迁移执行器：构造期即校验 FS 与方言，配置错误不会拖到执行期。
	//    gf 数据库配置就绪时 DB 工厂可省（缺省走 g.DB().Master()）。
	m, err := migrate.New(migrate.Config{
		FS:      migrations,
		Dialect: migrate.DialectPostgres,
	})
	if err != nil {
		panic(err)
	}

	root := gcmd.Command{
		Name:  "app",
		Brief: "业务项目命令行入口",
	}

	// ④ 挂进项目自己的 gcmd 命令树（项目可继续挂载自己的其他命令）
	if err := root.AddCommand(migrate.NewCommand(m)) err != nil {
		panic(err)
	}

	root.Run(gctx.New())
}
```

运行：

```bash
# 生产构建：默认构建即生产形态（见第 5 节），无 --fresh
go build -o app .
./app migrate

# 开发构建：--fresh 仅在此构建存在
go run -tags dev . migrate --fresh
```

迁移 SQL 文件体沿用 goose 注解分段：

```sql
-- +goose Up
CREATE TABLE users (...);

-- +goose Down
DROP TABLE users;
```

文件名格式 `{5 位零填充数字}_{小写描述}.sql`（如 `00001_create_users.sql`）——`New` 构造期对 FS 内全部 `.sql` 文件校验（R7），文件内容在执行期过完整性对账（R1/R2）与粒度校验（R4），三者都是机器强制条款，详见第 3 节。本组件只执行 `-- +goose Up` 段（`Down` 段按 goose 格式惯例保留，当前不会被触发；粒度判定同样只看 Up 段）。

## 3. 迁移文件强制规约（R1/R2/R4/R7）

四条铁律，全部由代码强制、无开关无豁免：

| 规约 | 机制 | 校验时机 | 拒绝文案前缀 |
| --- | --- | --- | --- |
| R1 | 已应用文件内容不可修改（checksum 对账） | `Up` 执行前 | `已应用的迁移文件被修改: ` |
| R2 | 已应用文件不可删除 / 重命名 | `Up` 执行前 | `已应用的迁移文件缺失: ` |
| R4 | 一个目标对象一个迁移文件（AST 粒度判定） | `Up` 执行前，仅待应用文件 | `迁移文件粒度不合规: ` |
| R7 | 文件名 `{5 位零填充数字}_{小写描述}.sql` | `New` 构造期 | `迁移文件名格式不合法: ` |

`Up` 的双门时序：完整性对账门先于粒度校验门，两门都先审后写——R1/R2/R4 命中时旁表与业务库都未被触碰，拒绝发生在任何写库动作之前；两门都通过才交给 goose 应用，应用成功后逐文件记录旁表。

**R7 文件名格式（New 构造期）**：正则口径 `^\d{5}_[a-z0-9_]+\.sql$`——恰好 5 位数字前缀（`0001_x.sql` 位数不足、`000001_x.sql` 超位均不合法），描述段仅限小写字母 / 数字 / 下划线（连字符不合法）。仅 `.sql` 后缀参与校验（README 等说明文件不受限）；goose 约定迁移文件平铺在 FS 根层，全量遍历兜底——嵌套路径（含路径分隔符）同样拒绝。文件名是迁移排序与版本比对的唯一依据，格式失真在构造期直接拒绝，不拖到执行期 goose 报错。

**gf_migrate_state 旁表**：R1/R2 对账的记录基准，`Up` 首次执行时自动创建（幂等 DDL）：

```sql
CREATE TABLE IF NOT EXISTS gf_migrate_state (
    version_id  bigint PRIMARY KEY,
    filename    text NOT NULL,
    checksum    char(64) NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now()
)
```

- **checksum 口径**：文件全文（含 goose 注解行）的 sha256 hex，64 字符小写十六进制——锚定文件字节内容，与 Up 段截取无关；
- **记录时机**：每个迁移应用成功后写入（UPSERT，版本号冲突时刷新文件名与 checksum，重放安全）；
- **对账主线**：goose 版本表（`goose_db_version`）的已应用版本集 × 旁表记录 × FS 文件三方交叉比对。版本表归 goose 管（含其建表时插入的 `version_id = 0` 基线行，对账排除——它不对应任何迁移文件）；旁表与版本表无物理外键、逻辑上一版本一行；
- **崩溃自愈**：goose 已应用但旁表未写（写旁表失败、进程崩溃）→ 下次 `Up` 自动按 FS 当前内容补录，迁移 SQL 不会重复执行；
- **孤儿清理**：旁表有记录但版本表已无该版本（版本表被外部干预等异态残留）→ Warning 日志列出孤儿版本并自动删除，不报错、不拦迁移。

**R1/R2 完整性对账（Up 执行前）**：以版本表已应用版本为主线逐版本核对——

- R1 修改：同版本同文件名但 checksum 变了 → 拒绝「已应用的迁移文件被修改: <文件名>（禁止修改已应用文件，请新增迁移文件）」；
- R2 删除：版本表已应用某版本但 FS 无该版本文件 → 拒绝「已应用的迁移文件缺失: <名字>（禁止删除或重命名已应用文件）」，名字优先取旁表记录的最后已知文件名，旁表无记录时退回版本号；
- R2 重命名：同版本号但文件名变了（含仅改描述段）→ 同文案，报旁表记录的旧文件名。

**R4 粒度（Up 执行前，仅待应用文件）**：待应用文件（版本不在版本表已应用集内）的 Up 段被解析为 PostgreSQL AST，逐语句提取宿主对象（被 CREATE / ALTER / DROP / ... 的那张表、那个序列 / 类型 / 视图 / 函数 / 扩展 / schema），要求整个 Up 段涉及的宿主对象集合**恰好 1 个**——空集合与多集合均拒绝。已应用文件只做 checksum 对账、不进解析器（不追溯存量文件粒度，原因见下方基线化）。

单对象多语句合法。例：`00007_add_users_status.sql`，两条语句宿主都是 users：

```sql
-- +goose Up
ALTER TABLE users ADD COLUMN status smallint NOT NULL DEFAULT 0;
CREATE INDEX idx_users_status ON users(status);
```

一文件两对象拒绝（无论跨语句还是单语句多对象）：

```sql
-- +goose Up
CREATE TABLE users (id uuid PRIMARY KEY);
CREATE TABLE orders (id uuid PRIMARY KEY);
```

```sql
-- +goose Up
DROP TABLE a, b;
```

前者拒绝文案「迁移文件粒度不合规: <文件名>（涉及多个对象: users, orders；须一个目标对象一个文件）」，后者「…（涉及多个对象: a, b；须一个目标对象一个文件）」。

SET / BEGIN 等无宿主语句被忽略，但纯无宿主文件拒绝（空集合）：

```sql
-- +goose Up
SET search_path TO public;
BEGIN;
COMMIT;
```

→「迁移文件粒度不合规: <文件名>（未包含任何针对数据库对象的语句；须一个目标对象一个文件）」。

**宿主映射与 fail-closed**：语句三路分派，映射清单刻意保守——

| 分派 | 语句（PostgreSQL 形态） | 宿主对象 |
| --- | --- | --- |
| 宿主映射 | CREATE TABLE / ALTER TABLE / ALTER INDEX | 表自身 |
| 宿主映射 | DROP TABLE / DROP SEQUENCE | 被删对象（单条语句删多个时全部计入） |
| 宿主映射 | TRUNCATE | 被清空的表（多个全部计入） |
| 宿主映射 | CREATE INDEX | 索引所在表 |
| 宿主映射 | CREATE TRIGGER | 触发器所在表 |
| 宿主映射 | INSERT / UPDATE / DELETE | 目标表 |
| 宿主映射 | CREATE SEQUENCE / ALTER SEQUENCE | 序列自身 |
| 宿主映射 | CREATE TYPE ... AS / ALTER TYPE ... ADD VALUE | 类型自身 |
| 宿主映射 | CREATE FUNCTION | 函数名（限定名取末段，public.fn → fn） |
| 宿主映射 | CREATE EXTENSION | 扩展名 |
| 宿主映射 | CREATE VIEW / CREATE MATERIALIZED VIEW / CREATE TABLE AS | 视图 / 新表自身 |
| 宿主映射 | COMMENT ON | 列注释（COLUMN）宿主 = 列所属的表（与索引同口径）；其余对象取对象名 |
| 宿主映射 | CREATE SCHEMA | schema 名（`CREATE SCHEMA AUTHORIZATION joe` 无名形态无法判定宿主，拒绝） |
| 宿主映射 | GRANT / REVOKE | 受权对象（多个全部计入） |
| 无宿主白名单 | SET 会话变量、BEGIN / COMMIT / SAVEPOINT 等事务语句、纯 SELECT | 忽略，不进集合（`SELECT ... INTO` 建表形态除外——按无法判定拒绝） |
| 白名单之外 | 一切未收录语句类型（DO、CALL、CREATE ROLE、DROP VIEW 等） | fail-closed 硬拒绝 |

fail-closed 是判定的灵魂：语句类型不在映射清单内、或宿主对象名提取失败时一律硬错误，绝不静默放行——宁可误拒合规文件（由维护方扩映射）也不漏放不合规文件。白名单外语句的报错带 pg_query 节点类型，例如 `DO $$ BEGIN NULL; END $$;` 拒绝文案为「迁移文件包含无法判定宿主对象的语句: <文件名>（语句类型: DoStmt；不支持该语句类型时向 gf-lib 维护方反馈）」。

对象名口径：按 SQL 原样大小写敏感（同一文件内 `users` 与 `Users` 是两个对象）；限定名取末段（不含 schema 前缀）；`-- +goose Down` 段不参与任何判定。

**自动基线化（存量项目升级）**：升级到含强制规约的版本后第一次 `Up`，若旁表为空且 goose 版本表已有应用记录（存量项目的典型形态）→ 自动把全部已应用文件按 FS 当前内容补录进旁表，输出日志「已锁定迁移基线，此后已应用文件不可变更」，随后照常执行增量。要点：

- 基线化**不追溯存量文件粒度**：粒度门只看待应用文件，存量文件（含批次式多表一文件）不进解析器——追溯会把存量项目一次性打红，且其内容已被完整性门锁定；
- 基线化判定下**缺文件仍拒**（R2 优先于补录——锁定一个已知缺文件的状态没有意义，基线化的无感过渡以文件齐全为前提）；
- 基线化锁定的是 **FS 当前内容**：历史上被原地改过的文件，锁定的是改后现状（版本表无从考据原始内容），分叉不会消失；
- 全新空库（版本表、旁表皆空）不触发基线化，全部文件按待应用走粒度校验；
- 中途失败补录与基线化共用同一规则（版本表有、旁表无 → 以 FS 补录），无需单独分支。

**--fresh 与规约的交互**：`--fresh` 清空 public schema（`DROP SCHEMA ... CASCADE`）时，goose 版本表与 gf_migrate_state 旁表一并被清——此后 `Up` 没有任何「已应用」记录，**全部迁移文件按待应用走粒度校验**（文件名格式仍在 `New` 构造期校验）。因此：

- 批次式存量文件（一文件多表）不拆分，`--fresh` 会在粒度门被拒绝；
- 拆分成单对象合规文件后 `--fresh` 通过，旁表按拆分后文件重录；
- 清「批次债」的唯一出口 = 把存量文件拆分成合规文件后 `--fresh` 重建——增量路径下已应用文件不可改不可删（R1/R2），没有第二条路。

**错误文案全表**（对外可观察契约，与源码逐字一致；`<>` 为运行期填充）：

| 触发 | 文案 |
| --- | --- |
| `New`：文件名格式失真（R7） | `迁移文件名格式不合法: <文件名>（须为 {5 位数字}_{描述}.sql，如 00001_create_users.sql）` |
| `Up` 对账：已应用文件被修改（R1） | `已应用的迁移文件被修改: <文件名>（禁止修改已应用文件，请新增迁移文件）` |
| `Up` 对账：已应用文件缺失 / 改名（R2） | `已应用的迁移文件缺失: <文件名>（禁止删除或重命名已应用文件）` |
| `Up` 粒度：多对象（R4） | `迁移文件粒度不合规: <文件名>（涉及多个对象: <对象列表>；须一个目标对象一个文件）` |
| `Up` 粒度：无任何宿主对象（R4） | `迁移文件粒度不合规: <文件名>（未包含任何针对数据库对象的语句；须一个目标对象一个文件）` |
| `Up` 粒度：语句类型无法判定宿主 | `迁移文件包含无法判定宿主对象的语句: <文件名>（语句类型: <节点类型>；不支持该语句类型时向 gf-lib 维护方反馈）` |
| `Up` 粒度：SQL 语法错误 | `迁移文件解析失败: <文件名>: <pg_query 原始错误>` |
| `Up` 对账阶段 DB 操作失败 | `校验迁移完整性失败: <原因>` |
| `Up` 读盘失败（embed 缺文件等罕见形态） | `读取迁移文件失败: <文件名>: <原因>` |

除「迁移文件名格式不合法」「已应用的迁移文件被修改」「已应用的迁移文件缺失」三条完整文案外，其余均以前缀 + `%w` 包装返回，错误链可解出底层原因。

## 4. Config 参数语义

`migrate.New(cfg migrate.Config)` 的三个字段（构造期完成全量校验，不把配置错误拖到执行期；FS 内全部 `.sql` 文件同时在构造期过 R7 文件名校验，见第 3 节）：

| 字段 | 类型 | 必填 | 语义与错误行为 |
| --- | --- | --- | --- |
| `FS` | `fs.FS` | 必填 | 项目 embed 的迁移文件（一般是 `embed.FS` 经 `fs.Sub` 取出的迁移目录）。`nil` 时 `New` 立即返回「迁移文件系统未初始化，请联系开发者」 |
| `Dialect` | `migrate.Dialect` | 必填 | 目标数据库方言，目前仅 `DialectPostgres`。零值（及方言映射表外的一切值）时 `New` 立即返回「不支持的数据库方言」 |
| `DB` | `func() (*sql.DB, error)` | 可选 | 数据库连接工厂。缺省 `nil` 时走 `g.DB().Master()`——迁移是写操作，不经从库；测试或特殊部署环境可注入自定义连接来源 |

## 5. 构建分身契约（默认即生产）

**默认构建（不加任何标签）就是生产形态**：

- `NewCommand` 编译为纯增量版本：Brief「执行数据库迁移（增量 up）」，不识别任何参数；
- `Fresh` 方法物理不存在——清库代码位于 `//go:build dev` 的文件（`fresh_dev.go`），编译期就不进二进制。

**`-tags dev` 构建才有 `--fresh`**：

- `NewCommand` 编译为开发版本：Brief「执行数据库迁移（增量 up）；[--fresh] 清空重建（仅开发构建）」，解析 `--fresh` 参数；
- `--fresh` 的语义（`Fresh` 方法）：`DROP SCHEMA IF EXISTS public CASCADE` → `CREATE SCHEMA public` → 调 `Up` 从头应用全部迁移。清 schema 连带清掉版本表与 gf_migrate_state 旁表，全部文件按待应用走粒度校验（与规约的完整交互见第 3 节）。

**为什么方向是「默认生产 / `-tags dev` 开发」而不是反过来**——忘加标签时的失败模式对比：

- 本契约下忘加 `dev`：开发时发现 `--fresh` 不存在（无害，补上 tag 即可）；
- 反过来（生产需要显式 tag）忘加 `prod`：**生产二进制带清库代码**（致命）。

「什么都不做」就是安全态，这是 fail-safe 设计。

**从自定义构建标签迁移**：若旧项目用 `-tags prod` 区分生产构建，请按以下方式调整构建脚本。切换到本组件时构建脚本必须反转——生产 / CI 构建删除 `-tags prod`（什么都不加），开发命令改 `-tags dev`。若忘改，症状是「开发机 `--fresh` 消失」（无害方向，不会出现生产带清库代码的反向事故）。

**项目侧挂载代码只有一份**：同名 `NewCommand` 按构建标签二选一编译，四段式接线代码不出现任何构建标签——这是收编的核心价值之一（上游形态里项目侧要维护两份带标签的命令文件）。

> 提示：`-tags dev` 对整个模块树生效——构建时任何依赖包里 `//go:build dev` 的文件都会被激活。罕见但存在，项目侧如遇构建异常可先排查此语义。

## 6. FAQ

**Q：迁移建表之后怎么填充初始数据？**

如果应用提供幂等的 seed 命令，部署时按顺序执行：先 `./app migrate` 建表、后 `./app seed` 铺底数据，两者都幂等，部署脚本可无条件顺序执行。

**Q：`--fresh` 为什么默认不存在？**

见第 5 节构建分身契约。补充：默认构建即使被误传 `--fresh`，该参数也只被当作未声明选项忽略，命令执行普通增量 up——不存在任何代码路径能触达 `DROP SCHEMA`（CLI 层与库层双重隔离）。

**Q：为什么 `Up` 可以安全地重复执行（幂等）？**

`Up` 是增量迁移：goose 在版本表（`goose_db_version`）记录已应用的迁移，已执行的文件自动跳过。每次 `Up` 先过完整性对账（已应用文件被改 / 删会在此拦截，见第 3 节）。全部已应用时输出日志「数据库已是最新版本，无待执行迁移」，不报错。

**Q：存量项目升级后会撞上强制规约吗？**

首次 `Up` 自动基线化（见第 3 节）：文件齐全时无感过渡，存量文件不追溯粒度；此后新写的文件受全量强制（R1/R2/R4/R7）。

**Q：已应用的批次式文件（一文件多表）怎么清理？**

增量路径没有出口：R1 锁死内容、R2 锁死删改，且无任何开关。唯一出口是把存量文件拆分成单对象合规文件后 `--fresh` 重建（见第 3 节）。

**Q：构建报 `undefined: pgquery.Parse` 是组件 API 没了吗？**

不是。这是 CGO 被禁用时 pg_query_go 的 C 部分未参与编译的报错形态（见第 2 节 Cgo 构建前提），报错本身与 API 是否存在无关。

**Q：错误文案的前缀约定是什么？**

全部错误以中文前缀 + `%w` 包装返回，错误链可解出底层原因（`errors.Unwrap` / `errors.Is`）；完整文案逐字表见第 3 节：

- `New`：「迁移文件系统未初始化，请联系开发者」「不支持的数据库方言」「迁移文件名格式不合法: 」；
- `Up` 完整性对账门：「已应用的迁移文件被修改: 」「已应用的迁移文件缺失: 」「校验迁移完整性失败: 」「读取迁移文件失败: 」；
- `Up` 粒度门：「迁移文件粒度不合规: 」「迁移文件包含无法判定宿主对象的语句: 」「迁移文件解析失败: 」；
- `Up` 引擎：「获取数据库连接失败: 」「初始化迁移 provider 失败: 」「执行迁移失败: 」；
- `Fresh`（仅 dev 构建）：「获取数据库连接失败: 」「删除 schema 失败: 」「创建 schema 失败: 」「不支持的数据库方言（fresh）」。

**Q：什么时候需要注入 `Config.DB`？**

gf 数据库配置就绪（`g.DB()` 可用）即可省略。需要注入的场景：单元测试隔离连接来源、特殊部署环境不走 gf 默认数据源。

**Q：支持 MySQL / 其他数据库吗？**

首版仅 PostgreSQL。方言是预留的扩展点（见第 7 节维护者注记），项目侧需求按 README 第 4 节协作规则提交给 gf-lib 维护方评估。

**Q：有 down / status / version 子命令吗？**

没有，明确不存在。上游两项目只用「增量 up + dev 清库重建」两种能力，收编时有意收窄，不透传 goose 的其余能力。确有需要时向维护方提需求评估。

## 7. 维护者注记

**goose 调用只在一个文件**：goose 的 import 与类型只出现在 `migrate/migrate.go`（及包内测试）。goose 升级的影响面收敛到单文件；「项目源码不 import goose」由编译器结构保证（导出签名零 goose 类型）。pg_query_go 同理：import 只出现在 `migrate/granularity.go`（及包内测试），升级影响面同样单文件。

**粒度白名单的扩展规矩**：宿主映射清单刻意保守起步（如 `DropStmt` 仅认 OBJECT_TABLE / OBJECT_SEQUENCE，DROP VIEW 等按 fail-closed 拒绝）。扩映射必须同步补对应宿主提取与测试，缺一不可。项目侧收到「迁移文件包含无法判定宿主对象的语句」报错后向维护方反馈、扩表放行——这是设计内路径而非事故；反向疏漏（白名单外被静默放行）才是事故。

**解析器语法漂移**：pg_query_go 是 PG 服务器 gram.y 的编译产物，版本落后于 PG 服务器新语法时，新语法文件被误拒（fail-closed 方向）——表现是带文件名与节点类型定位的硬错误，而非静默疏漏；升级 pg_query_go 即修复。解析只对待应用新文件执行，存量项目升级后不受影响。

**方言扩展点**：新增方言三步，缺一不可——

1. `Dialect` 枚举加值；
2. `gooseDialect` 映射补条目——漏补则 `New` 构造期拒绝（「不支持的数据库方言」）；
3. `Fresh` 的 `switch` 补实现——漏补则 `Fresh` 的 `default` 分支在**触碰数据库前**拒绝（「不支持的数据库方言（fresh）」），绝不静默跳过。

第 2、3 步的双保险是纵深防御：兜住「扩了枚举、进了 `New` 映射（`Up` 可用）、但 `Fresh` 漏实现」的未来失误。

**测试分层**：

| 层 | 位置 | 覆盖 | 构建标签 |
| --- | --- | --- | --- |
| 单测 | `migrate/*_test.go` | Config 校验（nil FS / 零方言 / R7 文件名表驱动：位数不足、连字符、无下划线、非 .sql 不参与）、DB 工厂注入与失败前缀、错误 `%w` 链、命令分派（fake runner：默认版不认 `--fresh`、dev 版分派 Fresh/Up）、Fresh 未知方言 default 分支、reconcile 对账决策表（正常 / 基线化 / R1 改动 / R2 缺失 / R2 改名 / 孤儿 / 中途失败补录）、parseGooseFile 截取与注解剥离与全文 checksum、粒度表驱动全量（单对象多语句合规 / 一文件两表拒 / 单语句多对象拒 / 空集合拒 / 白名单外拒 / 语法错误拒 / Down 段不参与，错误文案逐字断言） | 默认与 `-tags dev` 各跑一遍，不触真库 |
| 集成 | `tests/migrate_test.go` | 真 PostgreSQL：Up 应用计数、二跑零文件幂等、坏 SQL 迁移中途失败且错误链可解、TestMigrateIntegrity 系（存量基线化补录 + 增量并存、基线化判定下缺文件仍拒、改文件 R1 拒、改名 R2 拒、空库首跑不基线化、旁表三元组断言） | `-tags integration`，`PG_DSN` 环境变量注入连接，缺失自动 Skip |
| 集成 | `tests/migrate_fresh_test.go` | 真 PostgreSQL：Fresh 清空 public schema 后全量重放迁移（预置杂表消失 + 业务表/版本表重建）、粒度 × Up（合规新文件通过并记录旁表 / 多对象文件拒且库中无表被建）、粒度 × Fresh（批次存量文件被粒度门拒、拆分后 Fresh 通过且旁表重录） | `-tags "integration dev"`，同 `PG_DSN` 注入；会清 public，须指向一次性测试库 |

**双构建验证是提交前置**：两套命令分身若只在一套构建下编译过会漂移。收尾验证强制 `go build ./... && go vet ./... && go test ./...` 默认与 `-tags dev` 各跑一遍——编译期即可暴露漂移，无需 PG 环境。

**契约改动的规矩**：构建分身方向是对外安全契约；Brief 与错误文案是对外可观察行为（项目侧脚本可能依赖其输出）。此类改动走 shw-spec change 流程，并同步更新本文档与 README 组件一览表的 migrate 行。

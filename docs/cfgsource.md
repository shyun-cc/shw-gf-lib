# cfgsource — 「文件打底 + Driver 覆盖」的动态配置源适配器

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `cfgsource/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

cfgsource 把任意动态配置源（远程配置中心、DB、HTTP 接口等）以 `gcfg.Adapter` 形式接入 GoFrame 配置体系：业务侧实现单方法 `Driver` 接口，组件把 Driver 的全量配置与本地配置文件深合并为单一内存快照，经 `g.Cfg().SetAdapter` 挂载后，消费方读配置的代码一行不改（`g.Cfg().Get` 照常直达）。

三条核心行为，全部由代码结构保证：

- **合并快照**：内部以文件适配器（`gcfg.NewAdapterFile`）打底、Driver 全量配置为覆盖层，Driver 同名键覆盖文件键、未及的文件键保留；全部读取路径只查内存快照零 IO，快照因文件打底永不依赖 Driver 可用性；
- **轮询刷新**：默认每 5s 全量拉取一轮（间隔可调、不支持禁用），所有触发源（New 急加载、ticker 轮询、Refresh）经 single-flight 合并；快照内容有变才原子换绑并通知 watcher（diff 过滤），未变零通知；
- **fail-open**：启动时 Driver 急加载失败不阻断，`New` 返回 (adapter, err) 双值且 adapter 可用（快照退化为纯文件内容）；运行中刷新失败保旧快照，仅记错误日志。

不做的事：不解析类型——Driver 值一律原始字符串，类型转换由消费方经 gvar 完成；不感知存储介质——PG 表、Redis、配置中心、HTTP 接口都只是 `Driver` 的一种实现；无推送源——动态性靠轮询收敛，不提供变更推送通道。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节）。

完整接线（可整块复制；两处存储读取以注释占位，由项目替换为真实实现）：

```go
package main

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gctx"

	"github.com/shyun-cc/shw-gf-lib/cfgsource"
)

// dbDriver 业务侧 Driver 实现：本例全量配置存一张 PG 表，存储介质自定（组件不感知）
type dbDriver struct{}

// Load 全量返回扁平点分键 → 原始字符串值（存储读取以注释占位）
func (d *dbDriver) Load(ctx context.Context) (map[string]string, error) {
	// 实际实现（占位）：SELECT config_key, config_value FROM app_config，全量返回：
	// map[string]string{
	// 	"oss.bucket":   "my-bucket",
	// 	"oss.endpoint": "oss-cn-hangzhou.aliyuncs.com",
	// 	"sms.sign":     "某签名",
	// }
	return map[string]string{}, nil
}

// saveConfig 保存方联动：先落库，再调 Refresh 本进程即时生效
// （其他进程——多 Deployment 实例——最迟一个轮询周期收敛）
func saveConfig(ctx context.Context, adapter *cfgsource.Adapter, key, value string) error {
	// 实际实现（占位）：INSERT/UPDATE app_config ...
	_ = key
	_ = value
	return adapter.Refresh(ctx)
}

func main() {
	ctx := gctx.New()

	// ① 构造适配器：先建文件快照再拉 Driver（急加载），随后启动后台轮询（默认 5s）
	adapter, err := cfgsource.New(ctx, cfgsource.Config{
		Driver: &dbDriver{},
		// RefreshInterval: 10 * time.Second, // 可选；缺省 5s，≤0 回落默认，不支持禁用
	})
	if err != nil {
		// fail-open：急加载失败不阻断启动，adapter 仍可用（纯文件内容），记日志后继续挂载
		g.Log().Errorf(ctx, "cfgsource 初始加载失败: %v", err)
	}

	// ② 劫持默认配置实例：此后 g.Cfg().Get 全部直达合并快照，业务读法不变；
	//    database 等框架配置从文件打底内容天然可得，框架启动不受影响
	g.Cfg().SetAdapter(adapter)

	// ③ watcher：快照内容有变才通知。Go 常驻进程不像 PHP-FPM 每请求重建，
	//    已构建的长生命周期对象不会自行感知新配置，重建动作在此完成
	//    （此为 adapter.AddWatcher 直调路径，gcfg.Loader 结构体绑定路径见第 4 节）
	adapter.AddWatcher("oss-client", func(ctx context.Context) {
		// 用最新配置重建 OSS 客户端等长生命周期对象（业务占位）
	})

	// ④ 消费方读取：与读普通配置完全一致；Driver 值是原始字符串，类型转换经 gvar
	bucket, _ := g.Cfg().Get(ctx, "oss.bucket")
	fmt.Println(bucket.String()) // my-bucket

	// ⑤ 保存联动：落库 → Refresh；本轮刷新失败时组件保旧快照，错误原样返回
	if err := saveConfig(ctx, adapter, "sms.sign", "新签名"); err != nil {
		g.Log().Errorf(ctx, "配置保存联动失败: %v", err)
	}
}
```

常驻服务进程启动时执行 ①②③，④是此后任意消费点的读法；⑤属于配置保存链路（后台 / 管理端改配置的地方），与本进程不同属时，保存进程对本进程的影响就是第 4 节刷新语义里的轮询收敛。

## 3. Config 参数语义

`cfgsource.New(ctx, cfg cfgsource.Config) (*Adapter, error)` 的两个字段：

| 字段 | 类型 | 必填 | 语义与错误行为 |
| --- | --- | --- | --- |
| `Driver` | `cfgsource.Driver` | 必填 | overlay 配置源，接口契约见第 4 节。`nil` 时 `New` 立即返回「cfgsource: Driver 不能为空」 |
| `RefreshInterval` | `time.Duration` | 可选 | 后台轮询间隔，缺省 5s；≤0 回落默认值。不支持禁用轮询——组件无推送源，禁用即配置永不收敛 |

`New` 的返回值语义（fail-open 双值设计）：

| 场景 | 返回 | 行为 |
| --- | --- | --- |
| 急加载成功（文件 + Driver 均就绪） | `(adapter, nil)` | 快照为完整合并内容 |
| Driver 急加载失败 | `(adapter, err)` | adapter 仍可用，快照退化为纯文件内容；调用方自行决定记日志后继续挂载 |
| 文件 base 读取失败 | `(adapter, nil)` | 仅告警「cfgsource 初始加载文件配置失败，将以空文件配置构建: 」，以空 base 继续构建 |
| 文件适配器创建失败 | `(nil, err)` | 「cfgsource: 创建文件适配器失败: 」包装底层原因，未产生 adapter |

常驻服务一般无需处理关闭；测试或临时挂载场景用毕调 `Adapter.Close` 停止后台轮询（幂等，可重复调用）。

## 4. 契约

**合并模型**：适配器内部以 `gcfg.NewAdapterFile()` 文件配置打底，Driver 全量配置为覆盖层深合并——Driver 深层键覆盖 base 同名子键，Driver 未及的 base 键原样保留。全部读取路径只查内存快照零 IO：

- `Adapter.Get(ctx, pattern)`：按点分 pattern 读内存快照（如 `"oss.bucket"`、`"list.0.name"`），miss 返回 `(nil, nil)`，默认值由 gcfg 层 def 参数承接（`g.Cfg().Get(ctx, "oss.bucket", "默认值")`）；
- `Adapter.Data(ctx)`：返回当前快照的递归深拷贝，调用方改写不影响内部状态；
- `Adapter.Available(ctx)`：快照存在且内容非空——纯文件快照即 `true`，构造全败的空快照为 `false`；运行中刷新失败保旧快照，不翻转该值。

`g.Cfg().SetAdapter(adapter)` 劫持默认实例后消费方 API 不变——`g.Cfg().Get` 直达快照，框架启动要读的 database 等文件配置从打底内容天然可得。无启动死锁的机制依据：`New` 的执行序是「先 base 后 overlay」的急加载，`New` 返回时快照至少含文件内容，`SetAdapter` 发生在其后——即便 Driver 的 `Load` 自身读 `g.Cfg()`（配置存 PG 表时取 database 配置连库是典型形态），急加载阶段读的是原文件适配器，挂载后读的是含文件打底的快照，读取路径都不会回溯到 Driver。

**Driver 契约**：`Load(ctx context.Context) (map[string]string, error)` 全量返回一份配置，语义六条：

1. key 为扁平点分键（如 `"oss.bucket"`），无点分纯名同样合法；value 为原始字符串，组件不做任何类型解析（类型转换消费方经 gvar 完成，见第 5 节 FAQ）；
2. 返回 `(空 map, nil)` 为合法的全量空配置，组件清空 overlay（文件打底内容仍在）；
3. 返回 `(nil, err)` 表示本轮刷新失败，组件保旧快照并记错误日志（运行中文案「cfgsource 刷新配置失败: 」；急加载时错误经 `New` 返回交调用方处置）；
4. 组件每次 Load 后复制持有返回 map，Driver 复用返回 map 不影响组件状态；
5. `Load` 的调用经 single-flight 串行化（同一时刻至多一次在飞），实现应无状态、可重复调用：每轮刷新都会完整调用一次 Load；
6. 存储介质（PG 表 / Redis / 配置中心 / HTTP 接口……）自定，组件不感知。

**刷新语义**：三个触发源汇入同一入口——`New` 急加载、ticker 轮询（默认 5s，`RefreshInterval` 可配，≤0 回落默认，不支持禁用）、保存方落库后调用的 `Adapter.Refresh(ctx)`（本进程即时生效；其他进程——多 Deployment 实例——最迟一个轮询周期收敛）。并发触发经手写 single-flight 合并为一次执行：同一时刻至多一次 Driver Load，等待者共享执行者的结果（含同一 error）。每轮刷新以合并树的 json 序列化结果做 diff：内容有变才原子换绑快照并通知 watcher，内容未变静默返回。`Refresh` 失败本轮不换绑、保旧快照，错误原样返回调用方；ticker 轮询失败仅记日志，重试交给下一轮。

**watcher**：通知经 diff 过滤——仅快照内容有变的换绑触发，未变零通知。两条注册路径二选一：

```go
// 路径一：adapter.AddWatcher 直调（回调 fn 即 gcfg.WatcherFunc，形态 func(context.Context)）
adapter.AddWatcher("oss-client", func(ctx context.Context) { /* 重建长生命周期对象 */ })
// 配套管理：adapter.RemoveWatcher("oss-client") / GetWatcherNames() / IsWatching("oss-client")

// 路径二：gcfg.Loader 结构体绑定——配置变更时自动把 "oss" 子树重扫进结构体并回调 OnChange
var conf OssConf
gcfg.NewLoader(g.Cfg(), "oss", &conf).
	OnChange(func(updated OssConf) error {
		// updated 是重扫后的新结构体，重建动作放这里
		return nil
	}).
	MustLoadAndWatch(ctx, "oss-watcher")
```

常驻进程语义：Go 常驻进程不像 PHP-FPM 每请求重建对象，已构建的长生命周期对象（客户端、连接池）不会自行感知新配置——组件的机制边界是「变更时通知一次」，重建动作由回调承担。

**键格式**：Driver 的扁平点分键进入合并前先嵌套化——`"oss.bucket"` → `oss` 子树（`{"oss": {"bucket": "..."}}`），无点分纯名直接落顶层。同一轮配置里 `"a.b"`（浅键叶值）与 `"a.b.c"`（深键子树）同路径冲突时，保留深键丢弃浅键并记 warning：`cfgsource 键冲突: "<路径>" 与子树 "<路径>" 同名，保留深键丢弃浅键`（同一对冲突每轮仅告警一次）。键按字节序排序后插入树，结果与 map 迭代顺序无关。

## 5. FAQ

**Q：改了配置，为什么其他进程没马上生效？**

组件无推送源，动态性靠轮询收敛：其他进程（多 Deployment 实例）最迟一个轮询周期（默认 5s，`RefreshInterval` 可调）内拿到新值。落库的进程可在保存链路直接调 `Adapter.Refresh(ctx)` 让本进程即时生效（见第 2 节 ⑤）。

**Q：Driver 挂了（Load 返回 error）会怎样？**

分层兜底。运行中刷新失败：本轮不换绑、保旧快照，仅记错误日志「cfgsource 刷新配置失败: 」，服务以旧配置照常运行，下一轮轮询自动重试。启动急加载失败：fail-open，`New` 返回 `(adapter, err)` 双值且 adapter 可用（快照退化为纯文件内容）。由于快照以文件打底，database 等框架配置与文件内配置在任何故障形态下都读得到。

**Q：配置能存 PG 表以外的介质吗（Redis / 配置中心 / HTTP 接口）？**

能。组件只面向 `Driver.Load` 抽象，不感知存储介质——PG 表只是常见形态。实现一个读 Redis、拉配置中心或调 HTTP 的 Driver 即可接入，第 2 节接线代码不变。

**Q：Driver 的值都是字符串，数字、布尔怎么配？**

组件不做类型解析，value 一律原始字符串；消费方经 gvar 转换——`g.Cfg().Get(ctx, "oss.timeout")` 返回 `*gvar.Var`，按需调 `Int()` / `Bool()` / `Duration()` 等方法取值。miss 时拿到的是 nil 的 Var（gvar 方法对 nil 接收者安全，转换结果为零值），默认值经 gcfg 层 def 参数承接（`g.Cfg().Get(ctx, "oss.timeout", 30)`）。

**Q：RefreshInterval 设 0 能禁用轮询吗？**

不能。≤0 一律回落默认 5s，且不支持禁用——组件无推送源，禁用轮询即配置永不收敛。

**Q：Driver 里同时返回 `"a.b"` 和 `"a.b.c"` 会怎样？**

键冲突：保留深键（`"a.b.c"` 子树）丢弃浅键（`"a.b"` 叶值），并记 warning 带冲突路径定位；同一对冲突每轮仅告警一次，不影响其余键的正常合并。

**Q：watcher 触发后组件会替我重建对象吗？**

不会。组件的机制边界是「快照变更时通知一次」，重建由回调承担——常驻进程里客户端、连接池等长生命周期对象不会自行感知新配置（Go 不像 PHP-FPM 每请求重建）。需要结构体化新值时走 `gcfg.Loader` 路径的 `OnChange`（见第 4 节）。

**Q：Close 之后还能读配置吗？**

能。`Close` 幂等，仅停止后台轮询；Close 后 `Get` / `Data` 仍可读最后一份快照。与在飞刷新竞态时，换绑前检查生命周期已取消并放弃换绑，快照保持 Close 前状态。

## 6. 维护者注记

**零库内依赖的基础包定位**：cfgsource 只依赖 GoFrame 标准件（gcfg / gvar / gjson / g），不依赖库内任何组件——处于依赖方向图最底层，未来组件依赖它不构成环。当前库内无消费方，首批消费方预期是各组件把「可运营调参」的开关从启动期固定值升级为动态值。

**快照不可变与 diff 基准**：快照发布后只读、永不改写，换绑即整体替换新实例，并发读无锁（gjson 以非 safe 模式构建，仅走只读路径）。diff 基准是合并树 `json.Marshal` 的字节结果——键经排序插入、map 键有序，序列化确定性成立；文件解码值与 Driver 字符串均为 JSON 可序列化形态，序列化失败按刷新失败处置（本轮不换绑）。

**single-flight 为手写单 key 实现**：`refreshCall` 以 WaitGroup 建立等待者与执行者的 happens-before，等待者共享执行者结果含同一 error；在飞令牌置 nil 后才放行下一轮。改并发逻辑前先读 `cfgsource.go` 内注释，`Close` 与在飞刷新的竞态处理（换绑前检查生命周期 context）也在同一处。

**对外文案即契约**：错误文案「cfgsource: Driver 不能为空」「cfgsource: 创建文件适配器失败: 」「cfgsource 刷新配置失败: 」、告警文案「cfgsource 初始加载文件配置失败，将以空文件配置构建: 」「cfgsource 键冲突: …」与 5s 轮询默认值是对外可观察行为，项目侧脚本与运维排查可能依赖；改动走 change 流程，并同步本文档与 README 组件一览表的 cfgsource 行。

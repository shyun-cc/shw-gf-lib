# task 统一任务平台（v0.14）

需求来源 #114（城城联 #652，标准 f50327a5）。本文随 v0.13.0 各切片增量维护，在 v0.12 持久化 Store/Executor（见 [task-persistent.md](task-persistent.md)）之上叠加平台能力；空 namespace 旧 Worker、`task/amqp` AlwaysAck 桥接保持原语义。

## 1. 类型目录与动态配置（#115）

`StoreConfig.TypeControl=true` 开启类型目录控制。`task_type` 每行是 namespace 下一个稳定业务键：

| 字段 | 层 | 说明 |
| --- | --- | --- |
| HandlerVersion / Policy（Priority、Timeout、Lease、Retry、Recovery） | 快照层 | 创建运行时快照进 Definition，修改只影响之后的运行 |
| Class | 快照层 | 执行类别（RabbitMQ 路由/队列划分，见后续切片） |
| SubmitEnabled / ConsumeEnabled | 实时控制层 | 业务提交开关与消费开关；关闭消费保留积压，不丢弃 |
| GlobalConcurrency | 实时控制层 | 该类型跨全部实例的同时运行上限（1..10000），不是“每实例” |
| RateLimit / RateWindow | 实时控制层 | 窗口内最多开始的尝试数；0 不限，窗口 1s..24h |

- `RegisterTypes(ctx, specs, audit)` 只插入缺失类型，新类型提交与消费均默认关闭，需管理端显式开通；重启/重新登记不覆盖已有配置、开关与版本。
- `ConfigureType` / `SetTypeSwitches`：expected version CAS + request key 幂等 + 同事务 Audit；审计失败整体回滚，重复键不再审计，同键不同内容返回 `ErrConflict`。
- `ConfigRevision`：namespace 配置修订号，类型配置与计划启停/配置的每次提交都递增。实例通过 `HeartbeatApplied(id, revision)` 上报已应用修订号，`Instance.AppliedRevision` 可读。

TypeControl 下：

- 提交（`Submit`）须类型存在且提交开启，否则 `ErrTypeUnavailable` / `ErrSubmitDisabled`；运行策略取自类型目录，调用方只提供 Type、Payload、ConcurrencyKey 与请求键。同键重复提交只比较这些调用方字段。
- 计划派发（`Tick`）与立即触发（`Trigger`）用当前类型策略生成快照，不检查提交开关（由计划启停控制）；缺类型定义的计划不派发、不推进游标。
- 领取（`Claim`）在原有 namespace、available_at、实例能力/容量、计划启用、互斥组条件之外，再要求类型存在、消费开启、类型 running 数 < GlobalConcurrency、窗口内开始尝试数 < RateLimit。名额检查与 attempt 创建同事务，调小上限不撤销在途，只停止补位；调大立即可领。待重试、异常任务不占执行名额。

## 2. 结构化结果

`Completion.Outcome`：

| Outcome | 运行状态 |
| --- | --- |
| success / partial / skipped | succeeded（Outcome 保留区分，partial 不冒充全部成功） |
| retryable_failure（兼容 failed）/ interrupted | 幂等策略且未达上限 → failed 等待退避；达上限 → abnormal；非幂等策略 → abnormal |
| permanent_failure | abnormal，不重试 |
| unknown | abnormal，保留未知结果，不盲目重试；无需确认 |

`Completion.Stats` 为有界结构化计数（≤64 项，键 ≤64 字节），随 Completion JSON 写入。

## 3. Outbox 与 RabbitMQ 闭环（#116）

`StoreConfig.Outbox=true` 时，创建运行（Submit/SubmitTx/Tick/Trigger/重放）与失败到期转 pending（`Recover`）都在同一短事务内写 `task_outbox` 新一代通知，并递增 `task.dispatch_generation`；旧代次待发通知作废。`SubmitTx(ctx, tx, …)` 让业务数据、任务与通知在调用方事务内一起提交或回滚（同一数据库；跨库用业务侧 Outbox）。

消息体 `task.DispatchMessage`（协议版本 1）只含 `v/ns/task_id/gen`，不复制载荷或秘密；MessageID 为 `task_id:gen`。

**投递**：`Relay.RunOnce` → `ClaimOutbox`（发布租约）只领取任务当前可执行（pending、available_at 到期、计划启用、类型消费开启）且代次有效的通知；TypeControl 下每类型本轮最多发布其剩余全局名额条数。`task/amqp.Transport.Publish` 以持久化 + publisher confirms + mandatory 发布，broker 退回（无路由）返回 `task.ErrUnroutable`；断连/未知/退回一律 `MarkOutboxFailed` 退避（1s 起倍增，封顶 60s）保留重试，只有确认成功才 `MarkOutboxSent`。确认后写回前崩溃会在发布租约过期后重复投递，由消费端代次与原子领取去重。

**消费**：`Transport.Consume` 以 ManualAck 订阅类别队列，`Store.Accept(instance, body, trace)` 给出处置：

| 结论 | 条件 | 确认 |
| --- | --- | --- |
| claimed | 原子领取成功，attempt 与租约已落库（租约失效必由 Recover 转重试新一代通知或异常终态） | ACK 后执行 |
| stale | 旧代次、任务已运行或已完成 | ACK |
| deferred | 暂停/名额/限速/互斥/未到期：同事务把当前代次通知重新置为待发布（1s 起倍增封顶 30s，不早于 available_at） | ACK |
| poison | 协议错误、跨 namespace、本作用域不存在或伪造代次 | Nack(requeue=false) 进死信并告警 |
| error | 数据库不可用 | 不确认；调用方本地退避持有，停机时 Nack(requeue=true) |

ACK 发生在领取事务提交之后、执行之前，长任务不受 RabbitMQ 消费确认时限约束。数据库重试（`available_at` + 下一代通知）是唯一业务重试权威，不使用 broker requeue 重试。

**修复**：`RepairDispatch(limit, staleAfter)` 为当前可执行、但当前代次没有待发通知或已发送超过 staleAfter 仍未被领取的任务补投新一代通知，不越过暂停与 available_at。

**拓扑**：`task/amqp.PlatformTopology(prefix, classes, quorum)`：direct exchange `<prefix>.task`、每类别 durable 队列 `<prefix>.task.<class>`（死信指向 fanout `<prefix>.task.dlx` → `<prefix>.task.dead`），quorum=true 声明 Quorum Queue。副本数与故障域由基础设施确认；单节点开发环境只用于功能验证。

rabbitmq 组件配套新增：`WithMandatory` + `ErrUnroutable`（需 WithConfirm 与 WithMessageID）、`ManualAck` 策略、`Consumer.Stop`（停止订阅，未确认消息重新入队）。旧 `task/amqp.NewHandler/NewConsumer` AlwaysAck 桥接语义不变。

## 4. 统一 Runtime（#117）

`task.NewRuntime(RuntimeConfig)` + `Run(ctx)` 是项目唯一需要启动的 Worker 入口（如 `app worker`），可多实例横向扩展。项目只提供：

- `Handlers map[string]PlatformHandler`：`Version`、`Run`、可选 `Validate`（参数 schema，执行前失败按 permanent_failure）；旧 `Reconcile` 字段仅保留源兼容，不再被调用。只映射已编译注册表，不反射执行任意方法/SQL/Shell。
- `Allowed`：部署许可白名单；实例能力 = Handlers ∩ Allowed（不再按业务名 ENABLE_WORKERS 编成）。
- `Types`：启动时 `RegisterTypes` 补齐缺失定义（默认关闭）。
- 资源硬上限 `Concurrency`（本实例执行池），以及 Store、Transport 连接。WebUI 配置的全局并发不能突破各实例硬上限之和。

Run 内循环：配置刷新（`Refresh` ≤5s，`Runtime.Refresh()` 可由通知加速；刷新失败保留最后已验证配置并在 `Status().ConfigStale` 标明；启动时读不到配置直接返回错误，不回退为全部开启）兼作实例心跳并上报已应用修订号；`Tick` 调度；`Relay` 投递；`Recover` + `RepairDispatch` 恢复；`Transport.Consume`（每执行类别一个订阅，prefetch=Concurrency）或无 Transport 时 DB pull。所有实例同时运行这些循环是安全的（namespace 事务串行 + 唯一键），也可用 `Disable*` 按角色拆分。

消费处置：先占本地执行名额（满时有界持有消息，不热循环 requeue）→ `Store.Accept` → claimed 则 ACK 后异步执行；stale/deferred ACK；poison Nack 进死信；数据库错误不 ACK、本地退避持有，停机时 Nack(requeue)。

**attempt trace**（#128）：每次领取由 `RuntimeConfig.TraceID` 生成 trace 标识（未配置时每次 attempt 生成新的 OpenTelemetry traceId），写入 `attempt.trace_id`（`AttemptView.TraceID`），并贯穿该 attempt 的 Handler/Splitter ctx、续租、写回与日志：`task.AttemptTraceID(ctx)` 总能读到；为 32 位十六进制 OTel traceId 时同时注入 SpanContext，`gtrace.GetTraceID(ctx)`/glog/`tracex.FromCtx` 与管理页 attempt trace 一致。每次 attempt 独立，不复用实例级 context。可选 `AttemptContext(ctx, lease)` 在此之后追加项目字段。v0.12 `Executor` 同样把 `TraceID` 回调的结果注入 Handler ctx。

执行：独立续租（Lease/3，最长 10s），`ErrCancelRequested` 时协作取消；租约丢失后取消 Handler 且放弃写回；业务 deadline 到期立即触发恢复；结果经 `Redact` 脱敏后写回。Handler 不响应取消时名额保持占用直至其返回（不强杀）。

**协作取消**：`Store.CancelRun(id, expected, key, audit)`——pending/failed 立即 cancelled 并释放互斥组；running 置 `cancel_requested`，执行器续租感知后取消，非成功结果落为 cancelled；已成功的外部副作用不承诺回滚。v0.12 `Executor` 同样遵守取消请求。

**停机顺序**：停止调度/投递/新领取并取消订阅（`rabbitmq.Consumer.Stop`，未确认消息由 broker 重新入队）→ 等待在途 `ShutdownGrace`（默认 30s）→ 协作取消并等待 `CancelGrace`（默认 10s），Handler 返回后写回 interrupted（幂等策略转退避重试）→ 未返回者租约过期由 Recover 处置 → `StopInstance`。SIGKILL 时无写回，租约过期后同样由 Recover 处置。

## 5. 父任务拆分 N 个子任务（#118）

处理器声明 `Splitter func(ctx, parent *Run, cursor []byte, limit int) (SplitBatch, error)`（与 Run 二选一）。Runtime 执行父任务时：读取持久化游标（`SplitCursor`）→ 最多 `SplitBudget` 批（默认 10），每批 ≤`SplitBatchSize`（默认 100，≤1000）→ `AppendSplit` 在一个事务内写入子任务、各自 Outbox 通知与新游标 → `FinishSplit`。

- **稳定检查点**：`ChildSpec.Key` 为父任务内稳定业务键，`unique(parent_id,child_key)`（重放行除外）保证重启/重试拆分不重复生成；游标是业务定义的 JSON（≤16KiB），禁止用易漂移 offset 充当。数据范围是否冻结由业务在游标语义中决定。
- **续派**：单次尝试预算用尽而未拆完时，父任务转 pending 并立即续派新一代通知，游标保留；续派次数（`continuations`）不计入失败重试次数。拆分出错/崩溃按父任务恢复策略重试，游标不回退（建议父类型使用 idempotent_retry）。
- **等待子任务**：`split_completed` 后父任务进入 `waiting_children`，不占执行名额；子任务进入 succeeded/abnormal/旧 dead/cancelled 时在同事务内检查，只有拆分完成且每个逻辑子任务（每个 child_key 的最新运行）都已处置才汇总。异常子任务已终结，不等待人工处置。
- **汇总**：全部成功（success/skipped）→ success；部分失败/取消/partial → succeeded + partial；全部失败/异常/取消 → abnormal + permanent_failure。`summary` 为 Completion JSON，`Stats` 含 total/succeeded/partial/failed/abnormal/cancelled。
- **重放**：幂等异常子任务可显式 `Retry`，原状态和错误保留，新运行继承 parent_id 与 child_key，汇总只计逻辑子任务一次；父任务已汇总后子任务重放结算会重新汇总（`summary_version` 递增）。
- **进度**：`SplitProgress(parentID)`：拆分中只给 `Discovered`（已发现 N 项），`Total` 为 nil，不伪造百分比；完成后 `Total` 为准确总数，并给出各状态逻辑子任务计数。`Children(parentID, page)` 列出子运行（含重放）。
- **取消**：`CancelTree(id, expected, includeChildren, key, audit)`；includeChildren=false 只取消父任务（子任务照常执行），true 时同时取消未开始/待重试子任务、运行中子任务协作取消。暂停父类型消费只停止后续拆分续派，不影响已生成的子任务。

同一计划的活跃轮次判断包含 `waiting_children`，父任务等待子任务期间不会派发下一轮。

## 6. 顺序控制（#119）

`Definition.OrderingKey`（设备号、订单号等，≤256 字节）与可选 `OrderScope`（默认类型名）定义严格有序。序号在创建运行的同一事务内由 `task_order` 计数器分配（回滚一并回滚，不产生悬空号）；`Run.OrderSeq` 可读。子任务可用 `ChildSpec.OrderingKey`。

- **门控**：领取与投递都检查同 scope+key 是否存在序号更小且状态不是 succeeded/cancelled/abnormal/dead 的运行；存在即阻塞。因此待重试、旧 needs_review、等待子任务会阻塞，异常与旧 dead 终态自动放行；不同键互不影响、可并发。优先级只在可领取集合内排序，不越过同键顺序。
- **空洞**：门控判断“是否存在更早阻塞项”而非序号连续，清理删除或回滚造成的空洞不会导致永久等待。
- **重放**：`Retry` 新运行分配新序号，不插回已推进的位置，原异常保留。
- **显式跳过**：`SkipOrder(id, expected, key, audit)` 仅允许未在运行的 pending/failed/dead 有序运行，置为 cancelled + outcome=skipped，推进后续运行；CAS + 幂等 + 同事务审计，审计应记录操作人、原因和对后续的影响。运行中的需先 `CancelRun`。
- **可见性**：`OrderState(scope, key)` 返回最近分配序号、队首阻塞运行与其后等待数。
- RabbitMQ 分片/单活跃消费者只是运输优化，顺序由数据库门控保证；投递器不投递被阻塞的运行，消费端遇到被阻塞的运行持久化延期。

## 7. 管理视图与 DTO（#120）

库只提供数据与命令，HTTP 路由、权限、操作人、页面由项目或独立公共 UI 模块实现（不在 gf-lib）。所有命令均为 expected version CAS + request key 幂等 + 同事务 `Audit`：

| 场景 | 读 | 命令 |
| --- | --- | --- |
| 类型（目标/实际并发、积压、最早就绪时点） | `TypeStats`、`Types`、`GetType` | `ConfigureType`、`SetTypeSwitches` |
| 计划（周期、下次时点、漏跑） | `Schedules`、`GetSchedule` | `SetEnabled`、`Configure`、`Trigger` |
| 实例（心跳、已应用修订号、容量） | `Instances`、`GetInstance`、`ConfigRevision`、`Runtime.Status` | — |
| 运行/尝试（状态、Outcome、Stats、trace、错误） | `Runs`、`GetRun`、`Attempts`、`GetAttempt`、`Counts` | `Submit`、`Retry`、`Resolve`、`CancelRun` |
| 父子树与拆分进度 | `Children`、`SplitProgress` | `CancelTree` |
| 顺序阻塞 | `OrderState` | `SkipOrder` |
| 投递积压/失败 | `DispatchStats` | —（`RepairDispatch` 由 Runtime 自动执行） |

视图不含租约 token 与连接凭据；`Payload`/`Summary`/`Result` 是项目写入的 JSON，结果在落库前经 `RuntimeConfig.Redact` 脱敏，管理接口仍应按角色裁剪展示。Go 时间字段为 `time.Time`；需要无 Z 的北京墙上时间 HTTP DTO 时由项目格式化（TimeWall + Asia/Shanghai 存储模式见 task-persistent.md）。处理器下拉只应列出已编译注册的 `Handlers`，参数用 `Runtime.ValidatePayload` 校验。

装配示例见 `task/example_platform_test.go`。

## 8. 迁移与兼容

1. **新库**：`Schema(TimeInstant)` 或 `Schema(TimeWall)`（含 DDL + UpgradeDDL + PlatformDDL）复制为项目正式迁移文件，按目标对象一文件拆分落地。
2. **v0.12 库升级**：停止旧执行器/扫描器 → 备份 → 执行 `PlatformDDL`（墙上时间库用 `PlatformSchema(TimeWall)`，均可重复执行，只追加表/列/索引，不改旧列语义）→ 以 `RegisterTypes` 登记已有处理器（默认关闭）→ 依据已确认的旧白名单与现有计划启停状态形成一次性初始化决议，用 `ConfigureType`/`SetTypeSwitches` 显式开通并审计（不在每次重启自动开启）→ 以 `TypeControl=true`（使用 RabbitMQ 时再加 `Outbox=true`）启动 Runtime。已有 pending 运行的 `dispatch_generation=0`，由 `RepairDispatch` 自动补投首代通知。
3. **兼容**：`TypeControl=false`、`Outbox=false` 保留 v0.12 装配方式，Store 恢复同样采用本版异常终态；空 namespace 旧 `Worker`/`Dispatcher`/`RetryScanner` 与 `task/amqp.NewHandler/NewConsumer` AlwaysAck 桥接语义不变，不会领取非空 namespace 的运行。`Completion.Outcome="failed"` 继续有效（等同 retryable_failure）。幂等请求键与命令账本保留策略不变（`Cleanup` 不删除带请求键的运行与命令）。
4. **回退**：停止全部新 Runtime → 处置 running/waiting_children/needs_review 运行 → 切回 v0.12 二进制（以 `TypeControl=false` 运行 Store/Executor，新增表与列保留不删）。父子拆分、顺序与 Outbox 状态只有新版本理解，回退前须确认无依赖它们的在途运行；业务副作用不能靠数据库回滚撤销。

## 9. 容量边界与验证范围

- 同 namespace 写事务以 advisory lock 串行（见 task-persistent.md），全局并发、限速、顺序门控与名额计数都在锁内判断，跨实例不越界；代价是 namespace 内写吞吐有限（每次领取/结算/投递批次是一个短事务），面向项目级后台任务。大流量场景按业务拆分 namespace 或另行压测确认，不宣称高吞吐。
- 领取与投递查询含按类型/顺序键的相关子查询，依赖 `idx_task_type_status`、`idx_task_order`、`idx_outbox_due` 等索引；积压规模与 DB 压测结论应由项目在真实数据量下记录。
- RabbitMQ：生产高可用优先 Quorum Queue（`PlatformTopology(..., quorum=true)`），副本数、节点故障域、vhost/权限与队列预算由基础设施确认；单节点开发环境与 CI 只证明功能正确，不是容灾证据。消息在领取提交后立即 ACK，消费确认时限不受长任务影响。
- 库 CI（真 PostgreSQL 18 + RabbitMQ 3）覆盖：类型目录 CAS/幂等/审计回滚、跨实例全局并发与调整、限速、task/outbox 原子回滚、confirm 后写回前重复投递去重、无路由、旧代次、暂停不热循环、毒消息死信、丢失补投、Runtime 池上限/修订下发/协作取消/有界停机/缺配置拒绝启动、拆分中断恢复/部分先完成/重放计数/取消范围/续派、顺序阻塞/异常放行/重试新序号/跳过/空洞。未覆盖且需项目或基础设施验证：真实 SIGKILL 进程级故障、DB/MQ 长时间断连、HA 切换、积压与 DB 规模压测、项目处理器外部副作用幂等。

## 10. v0.14 异常终态（#131）

公开状态 `StatusAbnormal="abnormal"`，展示“异常”，保留客观结果、错误、执行尝试、trace 与业务引用供定期复盘。结算同事务释放互斥；顺序门控放行异常前项；父子汇总将异常子项计入 `Stats["abnormal"]`，全部失败/取消/异常时父任务异常，部分失败时 Outcome=partial。`SplitView.Abnormal` / `TypeView.Abnormal` 是异常计数；旧 `NeedsReview` 字段恒为零。默认 Cleanup 保留异常。

`RecoveryRetry` 有界重试 failed/interrupted，unknown 不盲目重试；其他策略及重试耗尽异常终结。新非重试策略使用 `RecoveryStop`；旧 RecoveryReview/RecoveryReconcile 只兼容快照，等同异常结束，不再调用核对器。`Resolve` 废弃并始终返回 ErrConflict。上一轮异常终结后下一轮自动执行，不依赖 SkipOrder。

历史 needs_review 读映射、分批转换、停旧升级、回退限制与消费方 PC/API 清单见 [持久化文档](task-persistent.md)第 8 节。无结构迁移，不允许业务端直接 SQL 改库状态；库不替消费方执行生产数据处置。

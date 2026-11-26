# task 持久化调度与管理接入

Issue #95。新增 API 位于 `task` 包，复用 `task`/`task_attempt`，不引入 MQ、HTTP、RBAC 或项目业务任务。

## 1. 模型与责任

- `Store`：项目注入 gdb.DB、非空 Namespace、Now、TimeMode、Location。所有同命名空间写操作使用 PG 事务 advisory lock，统一锁顺序；Handler 不在事务中运行。此实现为项目级低频任务，命名空间内写事务串行，审计回调必须短且不得访问外部服务。
- `Schedule`：稳定 Name、DefinitionVersion、显式 Anchor/Timezone、interval 或五段 cron、Enabled、Version、NextRunAt。`RegisterSchedule` 只补缺失默认值，不覆盖暂停/游标/已配置策略；较旧 DefinitionVersion 被拒绝，新定义需要明确 Configure。
- `Definition`：Type、HandlerVersion、Payload、Priority、Timeout、Lease、Retry、Recovery、ConcurrencyKey，完整序列化进每个 Run 的 definition，不随计划变化。
- `Run`：一轮逻辑任务，自动重试继续使用同一 Task ID，手动重放创建新 Task 并保留 ReplayOf。管理读模型不包含租约 Token；Lease 仅供执行器内部使用。
- `Instance`：每次进程启动登记新 ID；Capabilities 为部署许可后的 type→handler version 白名单。空白名单零领取，Capacity 限制活跃尝试数。领取时心跳必须在最近 30 秒内；状态展示阈值由项目决定。

## 2. 迁移与时间

1. 停止旧执行器及旧重试扫描，备份任务表。检查并处理同一 `(task_id,attempt_no)` 重复数据，新唯一索引不会替项目自动删除历史。
2. 已有默认 TIMESTAMPTZ 表执行 `UpgradeDDL`；新库使用 `Schema(TimeInstant)` 或 `Schema(TimeWall)` 生成 SQL，复制为项目正式迁移文件（不要运行时自动建表）。旧 `DDL` 仍只创建基础双表，新增 namespace 列与尝试序号唯一索引；`CREATE TABLE IF NOT EXISTS` 不会升级已有表。
3. `TimeWall` 必须明确 Location，仅支持 `UTC` 或 `Asia/Shanghai`，不支持有夏令时歧义的墙上时间模式。读写时按字段年月日时分秒映射到指定 Location，不依赖宿主本地时区。Instant 模式发送 UTC 带偏移字符串，Wall 模式发送明确地区的无偏移字符串。写事务核对 task.available_at 的列类型，不符即报错。
4. 改已有表时间类型时用 `ConvertTimeColumnsSQL(to, zone)` 生成显式 `AT TIME ZONE` 转换模板。只生成、不会自动执行；需要项目固定数据库会话时区并通过自身迁移控制。反向调用生成时间转换回退 SQL。反向转换不是历史数据恢复，仍需保留备份。
5. 先运行旧 `RetryScanner` 清理存量孤儿：无有效 attempt 的 legacy running 标记 needs_review，人工核对后按项目迁移处理，不自动重放不确定副作用。新 namespace 的 Store 不接管历史空 namespace 行。
6. 启动新版 Store/Executor。不可滚动混跑旧二进制：旧版 PgRepository 不认识 namespace，可能误领新任务。回退必须先停全部新调度/执行器，处理 running/failed/needs_review，归档新命名空间运行，再切旧版；不得直接丢列丢历史。业务副作用通过项目核对，无法由数据库回滚撤销。

库的 Go 时间字段仍是 time.Time；项目若需 HTTP 无 Z，应在自己的 DTO 格式化，库不决定响应格式。

## 3. 周期与漏跑

`Schedule.Next(after)` 对 interval 使用固定 Anchor；cron 使用 robfig/cron/v3 的五段 parser，加显式 Timezone，不接受六段或描述符。不存在下一执行时间时返回错误。

`Store.Tick(ctx,budget,audit)` 的 budget 为 1..100。计划锁定、Task 插入、next_run_at 推进同事务，唯一键 `(schedule_id,scheduled_at)` 防重复；事务失败整体回滚。同计划 pending/running/failed/waiting_children 时不再派发；旧 needs_review 在 Recover 事务转换前仍阻塞，转换后放行。

- coalesce：选择最新到期时点，只生成一轮；MissedCount 累加合并掉的时点数。
- skip：不生成轮次，推进到未来并累计跳过数。
- catch_up：`Schedule.Due` 可有界计算 MaxCatchUp 个时点；实际 Tick 为确保同计划单活跃运行，每计划每轮至多派发一个，剩余最早游标保留。这是比 MaxCatchUp 更严格的上限。
- cron coalesce/skip 每次最多评估 100000 个时点，超限报错且不推进，须管理端显式重设 Anchor；interval 直接算术跳转。
- MisfireFrom/MisfireUntil 记录最近一次合并/跳过评估区间 `[from,until)`，coalesce 区间包含保留下来的最后一轮，精确合并/跳过数量看 MissedCount。

Trigger 立即执行要求计划启用、无活跃轮次，不改变周期游标。Configure 使用 expected version，只影响未来轮次，以显式传入的新 Anchor 重新计算 next；默认字段不是启动时的覆盖指令。

## 4. 领取与租约

`Claim(ctx,instanceID,id,traceID)`：id 空为 pull，非空为 ByID，两者完全共用事务路径。条件包括 namespace、pending、available_at、计划启用、部署能力、HandlerVersion、实例状态/容量及互斥组。创建 attempt、递增次数、关联 active_attempt_id 和占互斥组在同事务内。

Lease 最小 1 秒，Timeout 是独立业务时限（零表示不限），续租不延长 Timeout。`Renew`、`Progress`、`Complete` 都校验 namespace、运行状态、活跃 attempt、token、租约有效性；过期返回 `ErrLeaseLost`，不复活。Progress 0..100、Checkpoint 最多 16KiB JSON，可用 Audit 与业务检查点同事务。

`Executor.RunOnce` 按 Type/Version 找 Handler，独立 ticker 续租并刷新实例心跳；周期为 Lease/3 且最多 10 秒。续租错误立即协作取消，旧写入仍被拒绝；deadline 使用取消 context。`Run(ctx,poll)` 是单并发循环，poll 为 (0,10s]，退出登记 stopped；需要并发时项目启动多个 RunOnce 调用并自行管理停机与实例生命周期。Handlers/Capabilities 注册完成后不得并发修改。

不能强杀不响应 context 的 Handler，也不承诺外部副作用 exactly-once。归属令牌只保护库数据；外部 API 需要项目实现幂等键与外部结果自动收敛；人工仅定期复盘。

## 5. 结果、重试与恢复

Completion.Outcome 接受 success/partial/skipped/retryable_failure/failed/permanent_failure/unknown/interrupted。partial 的 Task 状态为 succeeded、Outcome=partial，不冒充全部成功，也不自动重放已成功部分。Summary/Result 必须有效 JSON，分别最多 16KiB/64KiB；ErrorCode ≤128 字节、ErrorSummary ≤2048、BusinessRef ≤1024。Completion 完整 JSON 写入 task.summary 与 attempt.result，Task.Result 保留业务 Result。[]byte 字段在该 JSON 中遵循 Go 默认 Base64 形式，读取后应解码 Completion。项目先脱敏，库无法识别任意 JSON 中的秘密。

Recovery 必须显式声明。新代码使用 `RecoveryRetry="idempotent_retry"` 或 `RecoveryStop="stop_on_failure"`；旧 `RecoveryReview="needs_review"`、`RecoveryReconcile="reconcile_first"` 仅兼容配置/运行快照，行为等同 RecoveryStop，不触发核对。仅幂等策略对 failed/retryable_failure/interrupted 自动退避；unknown 不盲目重试，直接以 `StatusAbnormal="abnormal"`（异常）结束。不可重试的失败、permanent_failure 与重试耗尽同样异常终结，真实 Outcome 保留。重试最大次数包含首次，Base/Factor/Max/Jitter 显式注入，Jitter∈[0,1]，实际退避位于未抖动值的 `[1-Jitter,1]` 倍内。

失败时下一重试时点只持久化一次；Recover 到期将 failed 转 pending，不叠加退避。回收/续租/完成共享归属校验，重复扫描不覆盖成功。并发组在 retry wait 中保留，成功/异常/取消在结算事务内释放；异常是供复盘的终态，不等待人工确认。旧租约/代次不可写回；Runtime 的 Handler 不响应取消时，本地执行槽保留至 Handler 返回。库不能强杀进程或撤销外部请求，外部幂等和自动收敛由项目工程实现。

旧 Worker 对 PG 自动使用 AtomicRepository，pull/ByID 原子领取，CreateAttempt 兼容返回已建的同序号同 worker 尝试。旧自定义 Repository 接口源兼容，但必须自行实现 AtomicRepository 才有新原子保证。旧 Scanner 对 PgRepository 使用原子恢复；到期 pending 的 queue 类型会持续重发唤醒直到领取，以覆盖消息丢失/过早投递，消费者仍 AlwaysAck，重复唤醒通过原子领取去重。

## 6. 管理 API 与事务审计

- Schedules/GetSchedule、Runs/GetRun、Attempts/GetAttempt、Instances/GetInstance：namespace 限定，Page.Limit 默认50、最大200，Offset 为管理展示用途。
- SetEnabled/Configure：expected version + request key，冲突返回 ErrConflict。暂停不取消正在执行的尝试。
- Submit：独立手动任务，request key 必填，相同 key + 相同 Definition 返回已有运行，不重复审计。
- Trigger：计划立即运行，幂等键不重复入队/审计。
- Retry：仅幂等策略的 abnormal/旧 dead 终态可创建关联新运行，原运行、错误和尝试不变；failed 自动退避阶段不可手动重试，同计划已有活跃轮次时拒绝。
- Resolve：已废弃，仅保留签名供源兼容，始终返回 ErrConflict。项目删除核对结果/确认成功/确认取消/确认重放 API 和界面。
- Audit(ctx,gdb.TX)：管理/派发/进度/完成可注入同事务审计，失败整体回滚；幂等重复命中不再调用。
- Counts：按状态提供计数，项目导出指标；建议另外记录 next_run_at 逾期、心跳年龄、lease_lost、abnormal。
- Cleanup：只删 cutoff 前无幂等请求键、无父子/重放/命令/互斥引用的终态；保留活动和异常运行（abnormal 默认不清理）。手动幂等记录和命令账本有意不删，避免历史 key 重用导致再次执行；其归档由项目按幂等保留期决定。

## 7. 装配与验证

项目依次：迁移 → NewStore → RegisterSchedule → RegisterInstance → 独立启动 Tick/Recover 短轮询与 Executor。将执行角色注册进 worker.RunEnabled；业务 Capabilities 从同一部署白名单过滤生成，禁止空 ENABLE_WORKERS 时自行填全量能力。Tick 的唤醒可以每5秒，但业务周期只由持久化 NextRunAt 决定，部署重启不重算。

可编译装配形态见 `task/example_persistent_test.go`。时间/漏跑/退避单测在 task/schedule_test.go；真实 PG 并发、事务故障、幂等审计、租约竞态、版本隔离、互斥、wall 模式和 Handler deadline 测试在 tests/task_persistent_pg_test.go，发布 PR 重型层执行。测试成功不替代项目20项 Handler 的外部幂等审计。

## 8. v0.14 升级与历史状态兼容（#131）

本版无新增列/表/索引，**结构迁移无需执行**。数据兼容单独由引擎完成：停止全部旧 Store/Runtime/Executor/Recover 实例并备份 → 用正式 tag 升级 → 对每个非空 Namespace 调用 `Recover(ctx, limit)`（limit 1..1000，按批循环或保持恢复轮询）→ 核对原状态数量和互斥释放 → 启动新调度/消费。Runtime 默认恢复循环也会自动完成转换；不得关闭恢复后期待历史锁消失。原始错误、outcome、summary（含 BusinessRef）、attempt 和 trace 均保留，仅更新状态、版本、空缺的结束时间并清空 active_attempt_id、释放本运行占用，触发父任务汇总；重复执行幂等。非空 namespace 的 Store 不接管空 namespace 旧 Worker 历史。

`GetRun`/`Runs` 返回旧 needs_review 时统一映射为 abnormal；`Runs` 用 abnormal 或旧 needs_review 筛选均匹配两种存储值，`Counts` 合并计数为 abnormal，不返回 needs_review 键。只读映射不释放占用，释放由 Recover 事务完成。`StatusNeedsReview` 常量保留旧字面值供识别存量，不应用于判断新读模型。异常不允许 CancelRun/SkipOrder 改写；Retry 只对显式幂等终态生效，不是调度恢复前置。

消费方 PC/API 清单：状态枚举及文案统一 abnormal/异常；旧入参 needs_review 可兼容映射；使用 TypeView.Abnormal / SplitView.Abnormal，旧 NeedsReview 字段保留但恒为零；移除 Resolve 与核对回调装配；新策略选择 idempotent_retry 或 stop_on_failure；保留详情的错误、outcome、attempt、trace、业务引用及部分成功统计。工程负责异常排查修复，人工定期复盘即可。部署前用新 tag 构建消费方，不混跑旧新版本。回退先停新执行器并排空/归档在途；旧版本不认识 abnormal，不能仅降依赖版本后直接启动，需项目制定回退适配或恢复备份（外部副作用不可回滚）。

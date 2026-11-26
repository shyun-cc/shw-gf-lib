# task — 表驱动异步任务引擎

> 面向项目侧接入者（第 1–4 节）与库维护者（加读第 5 节）。事实来源是 `task/` 源码，本文的代码示例、签名、SQL 与退避公式均与其逐字一致；若发现不一致，以源码为准并修正本文档。

新增持久化计划、执行租约和管理 API 见 [task-persistent.md](task-persistent.md)；v0.13 统一任务平台（类型目录与动态配置、全局并发/限速、Outbox + RabbitMQ、统一 Runtime、父子拆分、顺序控制、管理视图）见 [task-platform.md](task-platform.md)。升级必须执行 UpgradeDDL；空 namespace 为旧链路，非空 namespace 由 Store 管理。

## 1. 组件定位

表驱动异步任务引擎（对应 PHP Foundation/Task），**Table-First**：`task` 表是任务的唯一真相源，队列消息只带 task_id——队列挂了不丢任务，RetryScanner 兜底拉起。核心机制：

- **抢占**：PG `FOR UPDATE SKIP LOCKED`——并发 worker 抢同一批任务时互斥且不阻塞；
- **执行租约**：`task_attempt` 行记录每次执行，回写时校验 attempt 仍 running——超时回收后慢执行者的回写被拒，不会覆盖新轮状态；
- **重试独占**：重试只由 RetryScanner 触发（先回收 stuck、再转死 exhausted、最后按退避重试 failed）——执行器崩溃不会导致重试失效；
- **指数退避**：base=5s factor=2 cap=3600s，指数取 attempts-1（首次重试退避恰为 5s）。

即时消费（队列模式）官方 RabbitMQ 开箱即用：`task/amqp` 桥接包（见第 2 节⑥）+ `TypeRegistry` 注册 type（依赖 [rabbitmq](rabbitmq.md) 组件）。其他队列（Kafka 等）是项目决策：自实现单方法 `QueueProducer` 接口注入，pull 模式用 `NullProducer`。

## 2. 快速接入

### ① 建表

包内 `DDL` 常量是权威建表语句（task + task_attempt 双表及索引，PG）：

```go
import "github.com/shyun-cc/shw-gf-lib/task"

// 迁移 SQL 里执行 task.DDL（项目经 migrate 组件的迁移文件引入，或部署脚本执行）
_, err := db.ExecContext(ctx, task.DDL)
```

两表主键/FK 均为 uuid 列且 **DDL 不带 DEFAULT**：id 由组件内 `uuid.NewV7()` 生成（应用层生成，无 LastInsertId），插入全经组件——手工 SQL 直插因无 DEFAULT 失败是预期的防绕过特性（PG<18 无 uuidv7() 函数）。

### ② 提交任务

```go
repo := task.NewPgRepository()                                    // PG 仓储（g.DB() 取连接）
d := task.NewDispatcher(repo, nil)                                // nil → NullProducer（pull 模式）

t, err := d.Submit(ctx, task.CreationOptions{
	Type:    "send-welcome-mail",
	Payload: []byte(`{"user_id":1024}`),
})
// t.ID 即任务号（uuidv7 文本，组件生成）；ViaQueue=false（默认）pull 模式不发消息
```

### ③ 拉取执行（pull 模式，装配 worker 周期跑）

```go
w := &task.Worker{
	Name: "mail-worker-1", // workerID，写进 attempt 行
	Repo: repo,
	Handler: func(ctx context.Context, t *task.Task) ([]byte, error) {
		return sendMail(ctx, t.Payload) // 项目业务；返回值落 task.result
	},
}

mail := worker.NewProcWorker("task-mail", 10*time.Second, func(ctx context.Context) error {
	_, err := w.RunOnce(ctx) // claim → 建租约 → 执行 → 回写
	return err
})
```

### ④ 重试扫描（独立装配，与执行器分开跑）

```go
rs := task.NewRetryScanner(repo)

retryScan := worker.NewProcWorker("task-retry-scan", 30*time.Second, func(ctx context.Context) error {
	_, _, _, err := rs.Scan(ctx) // (回收数, 重试数, 死亡数, err)
	return err
})

// 与执行器同列表经 RunEnabled 注册，环境变量 ENABLE_WORKERS（逗号分隔白名单）
// 控制各进程实际启动哪些——扫描进程白名单只配 task-retry-scan 即与执行器分开跑
err := worker.RunEnabled(ctx, mail, retryScan)
```

### ⑤ 父子拆分（大任务切片）

```go
payloads := splitJob(bigJob) // [][]byte，每片一个子任务 payload
parent, children, err := d.SplitAndSubmit(ctx, opts, payloads)
// 父任务不逐次指定 ViaQueue（强制 false）；type 已注册 queue 模式时 Submit 判定仍会发消息
// 子任务继承父的 type/priority/max_attempts/timeout/biz_ext
// 父状态查询时聚合派生（AggregateChildStatus），不回写父行
```

### ⑥ 即时消费（queue 模式，RabbitMQ 开箱即用）

桥接包 `task/amqp`（导入路径 `github.com/shyun-cc/shw-gf-lib/task/amqp`）把 task 引擎与 [rabbitmq](rabbitmq.md) 组件装配成完整链路：注册 type → AmqpProducer → NewConsumer → WorkerManager。

```go
// 1. 注册 type 为 queue 模式：Submit 一律发消息、RetryScanner 重试后重投
registry := task.NewTypeRegistry()
registry.Register("send-welcome-mail")

// 2. 连接 + 声明拓扑（durable 组合；细节见 docs/rabbitmq.md）
client := rabbitmq.NewClient(rabbitmq.Config{Host: "mq.internal", User: "app", Password: "***"})
if err := client.Connect(ctx); err != nil {
	return err
}
if err := client.Declare(ctx, rabbitmq.TaskTopology("task.exchange", "direct", "task.queue", "task")); err != nil {
	return err
}

// 3. AmqpProducer 实现 task.QueueProducer：消息即 {"task_id":"<uuid>"} JSON
producer := amqp.NewProducer(client.NewProducer(), "task.exchange", "task")

repo := task.NewPgRepository()
d := task.NewDispatcher(repo, producer)
d.QueueTypes = registry // 发送判定 ViaQueue OR IsQueue(type)

// 4. 消费骨架：固定 AlwaysAck（表是真相源，重试独占走 RetryScanner，永不 requeue）
w := &task.Worker{Name: "mail-consumer-1", Repo: repo, Handler: mailHandler}
m.Register(amqp.NewConsumer(client, "task.queue", w))

// 5. 重试扫描同样接上注册表与 Producer（与 Dispatcher 共用同一实例）
rs := task.NewRetryScanner(repo)
rs.QueueTypes, rs.Producer = registry, producer
```

消息格式契约（task/amqp 包 doc 公示）：`{"task_id": "018f3c2e-..."}`，ContentType application/json，消息 persistent。task 表是唯一真相源、消息只是即时唤醒信号——消费端无论执行成败一律 ACK，失败补偿由 RetryScanner 表驱动重试；非法载荷（非 JSON / 缺 task_id / 非 uuid 形态）记日志后 ACK 丢弃，消费循环不中断；重复投递经 `ExecuteByID` 对非 pending 任务幂等忽略。

## 3. 参数语义

`CreationOptions` 逐字段：

| 字段 | 类型 | 默认 | 语义 |
| --- | --- | --- | --- |
| `AvailableAt` | time.Time | 零值取当前时间 | 首次可领取时点 |
| `TaskNo` | string | 空 | 业务侧任务号（幂等键由项目自行约束） |
| `Type` | string | 必填 | 任务类型，执行器按 type 分派 Handler |
| `Payload` | []byte | — | 业务载荷（JSONB 落表） |
| `Priority` | int | 0 | 大者先被 claim（ORDER BY priority DESC） |
| `MaxAttempts` | int | 3 | ≤0 时取 3；达限转 dead |
| `Timeout` | int | 0 | 秒；0 不限时。>0 时超时由 RetryScanner 回收 |
| `BizExt` | []byte | 空 | 业务扩展字段（JSONB），引擎不解读 |
| `ViaQueue` | bool | false | 提交期一次性标志：true 时本次经 QueueProducer 发 task_id 消息；type 注册进 `TypeRegistry` 后无须此标志一律发（且重试会重投） |

## 4. 契约

**任务状态机**：`pending → running → succeeded / failed →（退避后）pending … → dead`；拆分父任务终态为派生态 `completed_with_errors`（有子失败）或 `succeeded`（全成功），由查询时聚合得出。

**claim 语义**：PG 的 ClaimExecution 事务内领取并创建 attempt，pull/ByID 共用。Claim 使用相同路径，后续 CreateAttempt 返回同序号同 worker 的已建尝试。无可领取任务返回 nil。

**租约回写校验**：PG 先锁 task 并校验 running 及当前 attempts 序号，再更新对应 running attempt 和 task。Worker 对失去归属返回 ErrLeaseLost。旧自定义 Repository 必须实现可选 AtomicRepository 才有相同原子保证。

**退避公式**：`backoff = 5s · 2^(attempts-1)`，封顶 3600s——attempts=1 → 5s、2 → 10s、3 → 20s。

**队列投递判定**：Submit 发消息的判定是 `ViaQueue OR IsQueue(type)`——type 注册进 `Dispatcher.QueueTypes`（`TypeRegistry`）即 queue 模式，无须逐次传 ViaQueue。ViaQueue 是提交期一次性标志：未注册的 type 本次发消息但重试不重投，源码记 Warning `task: type=%s 提交带 ViaQueue 但未注册 queue 模式（TypeRegistry.Register），重试不重投队列，需 pull worker 或人工处置`。

**队列投递失败不回滚**：投递失败时任务已落表，Submit 返回携带 `task: 队列投递失败（任务已落表不回滚；注册为 queue 模式的 type 将由重试重投拉起，否则需 pull worker 或人工处置）` 前缀的错误与已创建的 Task——调用方无须重提，残留处置见第 5 节 FAQ。

**重试重投**：RetryScanner 把 failed 到期任务按退避转回 pending 后，注册为 queue 模式的 type（`RetryScanner.QueueTypes`，与 `Dispatcher.QueueTypes` 共用同一实例）经 `RetryScanner.Producer` 重投消息拉起即时消费；重投失败不回滚状态、不重试 Publish，仅记日志 `task: 重试重投失败（任务已转 pending 不回滚，需 pull worker 或人工处置）task=%s: %v`。

## 5. FAQ

**Q：pull 模式和即时消费模式怎么选？**

pull（默认）：worker 周期 RunOnce 拉取，零队列依赖，秒级延迟足够大多数场景。即时消费（queue 模式）：RabbitMQ 官方开箱即用——第 2 节⑥的装配（注册 type → task/amqp 生产消费 → WorkerManager），延迟敏感时用；消费收 task_id 消息（uuid 文本）后调 `ExecuteByID(ctx, taskID string)`（非 pending 幂等忽略）。其他队列（Kafka 等）自实现单方法 `QueueProducer`，消费端同样调 `ExecuteByID`。无论哪种即时消费，RetryScanner 仍须跑（队列消息可能丢、投递可能失败）。

**Q：投递失败 / 重投失败的任务会怎样？**

PgRepository 的原子 Scanner 会扫描到期 pending，并对注册 queue 类型重发唤醒直到领取，因此过早或丢失消息可在后续扫描恢复；重复唤醒通过 ClaimExecution 去重。未注册类型不会补投，仍需 pull 或人工处理。自定义旧 Repository 路径保留原有仅 failed 重投行为。

**Q：执行器进程崩了，运行中的任务怎么办？**

旧 PG Scanner 将超时 attempt 和任务原子推进，任务转带未来 available_at 的 pending 或 dead；没有有效 attempt 的历史孤儿标记 needs_review。旧 timeout=0 不自动回收有效 attempt。新增 Store/Executor 使用独立租约，业务 timeout=0 仍可回收失联执行者，见持久化文档。

**Q：重试会重试已成功的任务吗？**

不会。重试链只处理 failed 状态行；succeeded 是终态。

**Q：Handler panic 会怎样？**

panic 会让 executeTask 向上抛（RunOnce 的调用方——ProcWorker 装配下单轮 panic 被 recover 记日志后 re-panic，由 WorkerManager 兜底隔离，该 worker 停止、进程与其余 worker 不受影响）。attempt 停在 running，后续由 RetryScanner 按 stuck 回收。Handler 内部自行 recover 可以更快落 failed。

**Q：为什么父任务状态不回写？**

拆分任务的父状态是「全部子任务终态的聚合」，子任务随时在推进，回写会产生写放大与竞态。查询时经 `AggregateChildStatus` 派生（含 Progress = succeeded/total），永远即时准确。

## 6. 维护者注记

**Repository 接口化**：PG 实现在 `PgRepository`（gdb + 原生 SQL 承载 SKIP LOCKED 与租约校验），项目可换库/加读写分离时自实现 `Repository` 接口。PgRepository 通过 AtomicRepository 使用原子扫描。旧自定义 Repository 若不实现该接口仍走历史编排（含 recoverToFailed 的 g.DB 路径），不具备新原子保证；新实现应显式提供 AtomicRepository。

**测试分层**：包内 `task/task_test.go` 六用例（退避公式、状态判定等纯逻辑）；`registry_test.go` 四用例（注册判定、Mode 值、零值注册表可用、并发 Register）；`dispatcher_test.go` 五用例（发送判定矩阵、QueueTypes nil、ViaQueue 未注册仍发送、投递失败文案断言、注册 type 拆分提交父任务照发）；`scanner_test.go` 四用例（注册 type 重投、未注册不投、重投失败不回滚、pull 类型重试计数不变）——均经 fake Repository/Producer 注入；`task/amqp` 子包 consumer 五用例（执行成功/失败仍恒 nil/毒消息/重复投递幂等/AlwaysAck 装配）+ producer 三用例（消息契约、发布失败、断开透传），经 fake Delivery/Publisher 注入；`tests/task_pg_test.go` 真 PG 集成七用例（PG_DSN 注入、缺失 Skip）——并发 claim 互斥、租约往返、失败退避、超时回收后慢执行者回写被拒、重试转死、父子聚合、Save 插入/更新全字段往返（防 `Data()` 覆盖语义与字段回读遗漏回归）。

**依赖方向**：task 包本身无内部依赖（Repository 接口化）；`task/amqp` 子包 → task + rabbitmq（单向桥接，两个被桥接包互不感知）。

**改动规矩**：状态机常量、DDL、退避默认值（base/factor/cap）是对外行为契约；attempt 回写校验语义是并发正确性核心，改动须走 change 流程并过并发集成用例。

### v0.14 异常终态

Store/Runtime/Executor 公开异常状态为 `StatusAbnormal="abnormal"`，不等待人工确认。不可安全重试/重试耗尽/未知结果异常结束并释放占用，后续定时运行自动推进，错误、尝试与 trace 保留。历史 needs_review 由 Recover 分批转换（无结构迁移），查询先映射为异常。消费方 PC/API、停旧升级与回退限制见 [task-persistent.md](task-persistent.md)第 8 节。空 namespace 旧 Worker 孤儿规则仍见上文，不由非空 namespace 的 Store 接管。

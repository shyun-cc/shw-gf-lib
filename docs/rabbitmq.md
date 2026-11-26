# rabbitmq — RabbitMQ 基础组件（自管重连 + 拓扑声明 + 生产消费）

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `rabbitmq/` 源码，本文的代码示例、签名、默认值与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

RabbitMQ 通用基础组件：连接生命周期管理 + 拓扑声明 + 生产/消费，零业务语义。两个边界声明：

- **是 RabbitMQ 专属实现，不是 MQ 抽象层**：QueueProducer 一类业务接口在各自组件侧定义（如 [task](task.md) 的即时消费模式），本包不试图统一 AMQP/Kafka 的编程模型。
- **连接/channel 生命周期全自管**：唯一外部依赖 `github.com/rabbitmq/amqp091-go` 是 RabbitMQ 官方 Go 客户端，**不内置自动重连**——断线退避重连、拓扑重声明、订阅恢复全部由本包的 guardian 状态机承担，这是本包存在的核心价值。

task 场景的官方装配（注册 type → AmqpProducer → NewConsumer → WorkerManager）见 [docs/task.md](task.md) 第 2 节⑥，桥接代码在 `task/amqp` 子包。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节），有一套可用的 RabbitMQ 凭证。

```go
package job

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/worker"
)

// StartMQ 连接、声明拓扑、装配生产消费（示意：main 或服务启动期）
func StartMQ(ctx context.Context, m *worker.WorkerManager) (*rabbitmq.Client, *rabbitmq.Producer, error) {
	client := rabbitmq.NewClient(rabbitmq.Config{
		Host: "mq.internal", Port: 5672,
		User: "app", Password: "***", Vhost: "/",
	})
	// 首连：同步拨号，失败如实返回 error（不做内部重试，让配置问题尽早暴露）
	if err := client.Connect(ctx); err != nil {
		return nil, nil, err
	}
	// 停机：Connect 传入的 ctx 取消即触发优雅停机（等同 Close）；
	// 不依赖 ctx 生命周期时，在服务的退出路径显式调用 client.Close()

	// 声明拓扑（幂等：同名同参重复无害）并登记注册表——断线重连后自动原样重放
	err := client.Declare(ctx, rabbitmq.TaskTopology(
		"task.exchange", "direct", "task.queue", "send-welcome-mail",
	))
	if err != nil {
		return nil, nil, err
	}

	producer := client.NewProducer() // channel 由 Client 托管（断线自动重建）

	consumer := client.NewConsumer("task.queue",
		func(ctx context.Context, d rabbitmq.Delivery) error {
			// 项目业务：处理 d.Body()；返回非 nil error 视为失败，交由 NackPolicy 处置
			return handle(ctx, d.Body())
		},
		rabbitmq.WithNackPolicy(rabbitmq.NackRequeue), // 默认策略，可省略
	)

	// Consumer 实现 worker.Worker：注册进 WorkerManager 统一 ctx 生命周期
	m.Register(consumer)
	return client, producer, nil
}
```

使用形态固定为：`NewClient → Connect → Declare/NewProducer/NewConsumer → Close`；断线后的恢复全自动，无需调用方参与。

## 3. 参数语义

### 3.1 Config（连接与重连）

`rabbitmq.NewClient(cfg Config) *Client`。全部字段有默认值，零值字段以默认值补齐（见 `DefaultConfig`）：

| 字段 | 类型 | 默认 | 语义 |
| --- | --- | --- | --- |
| `Host` | string | `127.0.0.1` | 主机 |
| `Port` | int | `5672` | 端口 |
| `User` | string | `guest` | 用户名 |
| `Password` | string | `guest` | 密码 |
| `Vhost` | string | `/` | 虚拟主机 |
| `Heartbeat` | time.Duration | `10s` | AMQP 心跳间隔（<1s 时由 broker 按自身间隔协商） |
| `ReconnectBackoffBase` | time.Duration | `1s` | 断线重连退避基数（第 n 次重试等待 base·2^(n-1)） |
| `ReconnectBackoffCap` | time.Duration | `30s` | 退避上限（配置值小于 base 时按 base 生效） |

guest/guest@127.0.0.1 只是 AMQP 客户端库的常规出厂默认，生产环境必须用真实参数覆盖。

### 3.2 拓扑（Topology）

`Topology` 值类型三个切片，`client.Declare(ctx, topo)` 按 exchanges → queues → binds 顺序逐项声明：

| 类型 | 字段 |
| --- | --- |
| `ExchangeSpec` | `Name` / `Kind`（fanout / direct / topic / headers）/ `Durable` / `AutoDelete` / `Internal` / `Args` |
| `QueueSpec` | `Name` / `Durable` / `AutoDelete` / `Exclusive` / `Args`（如 x-message-ttl / x-dead-letter-exchange） |
| `BindSpec` | `Queue` / `Key`（routing key）/ `Exchange` / `Args` |

durable 等取值由调用方决定，库不预设。task 场景有便捷构造 `rabbitmq.TaskTopology(exchange, kind, queue, routingKey string) Topology`——durable exchange + durable queue + 绑定一步到位（与任务表「表是真相源、消息只是唤醒信号」的可靠性要求对齐，见 task/amqp 包文档）。

### 3.3 Producer

`client.NewProducer(opts ...ProducerOption) *Producer`，构造选项仅一个：

| 选项 | 默认 | 语义 |
| --- | --- | --- |
| `WithConfirm()` | 关 | 开启 publisher confirm：发布后等待 broker 确认（Channel.Confirm + NotifyPublish），broker nack、channel 中途关闭、ctx 取消均如实返回 error。confirm 模式下 Publish 全程串行 |

`producer.Publish(ctx, exchange, routingKey string, body []byte, opts ...PublishOption) error` 的可选项：

| 选项 | 默认 | 语义 |
| --- | --- | --- |
| `WithContentType(ct string)` | `application/octet-stream` | MIME 内容类型 |
| `WithTransient()` | —（默认持久化） | 标记消息非持久化（默认 delivery_mode=2 持久化） |
| `WithMessageID(id string)` | 空 | 消息标识 |
| `WithHeaders(h amqp.Table)` | 无 | AMQP 消息头 |
| `WithMandatory()` | 关 | mandatory 发布：无队列可路由时返回 `ErrUnroutable`（判定依赖 confirm 前到达的 basic.return）。仅限 `WithConfirm` 生产者且必须同时 `WithMessageID`（v0.13） |

### 3.4 Consumer

`client.NewConsumer(queue string, handler HandlerFunc, opts ...ConsumerOption) *Consumer`。`HandlerFunc func(ctx context.Context, d Delivery) error`，`Delivery` 是消息薄适配接口（`Body() []byte` / `Ack() error` / `Nack(requeue bool) error`）。构造选项：

| 选项 | 默认 | 语义 |
| --- | --- | --- |
| `WithNackPolicy(p NackPolicy)` | `NackRequeue` | 失败处置策略（见下） |
| `WithPrefetch(n int)` | `10` | QoS prefetch（0 表示不限）；串行消费下决定 broker 预推的未确认消息量 |
| `WithConsumerName(name string)` | `rabbitmq-consumer:<queue>` | worker 名与 AMQP consumer tag |

`NackPolicy` 四值（组件不预设策略，由调用方注入）：

| 取值 | 失败时 | 成功时 | 适用 |
| --- | --- | --- | --- |
| `AlwaysAck` | Ack | Ack | task 场景语义：表是真相源，失败走任务表重试而非消息重投 |
| `NackRequeue` | Nack + requeue | Ack | 通用重试场景（默认） |
| `NackDrop` | Nack 不 requeue | Ack | 毒消息直丢，或由死信 exchange 接管 |
| `ManualAck` | 不处置 | 不处置 | handler 自行调用 `Ack/Nack`（v0.13，task 统一平台“领取落库后 ACK”使用） |

`consumer.Stop()`（v0.13）：从 Client 注册表移除（重连不再重订阅）并关闭独占 channel，broker 将未确认消息重新入队；`Run` 在在途 handler 完成后返回。用于有界停机的“停止新拉取”。

## 4. 契约

**重连语义**：Client 内 guardian goroutine 监听断连（connection.NotifyClose），按 `base·2^(n-1)` 封顶 cap（默认 1s·2^n 封顶 30s，可配）退避重连；成功后自动重声明已登记拓扑、为已注册 Producer 重建 channel、恢复 Consumer 订阅。channel 级自愈：连接仍在、channel 单独被 broker 关闭（如 queue 被删触发 404 关 channel）时立即单独重建该组件的 channel/订阅。`Connect` 首连同步拨号、失败如实返回 error 不内部重试（启动期失败让调用方尽早感知配置问题）；幂等，guardian 已运行时再调用直接返回成功。

**拓扑声明幂等**：AMQP 声明同名同参重复无害；同名异参 broker 返回 406 precondition failed，`Declare` / 重连重放如实透传该错误（配置漂移早暴露，不静默吞并）。`Declare` 须在 `Connect` 之后调用，未连接时返回 `ErrNotConnected`。

**生产语义**：消息默认 persistent（delivery_mode=2）；confirm 参数化、默认关（开启后 nack / channel 中途关闭 / ctx 取消如实报错）。**连接断开期间 Publish 立即返回 `ErrNotConnected`（`rabbitmq: 连接未建立或已断开`），绝不静默缓存重发**——调用方自行决定失败处理（重试/落库/告警）。发布失败的错误前缀为 `rabbitmq: 发布到 %s (key=%s) 失败`。

**消费语义**：消费循环**串行一次一条**（handler 完成 → 按策略 Ack/Nack → 取下一条）；handler panic 被 recover 隔离（记日志后转为 error，按 NackPolicy 处置该消息），消费循环绝不断掉。Ack/Nack 自身失败（连接已断）仅记日志——消息会因未确认而在重连后由 broker 重新投递（at-least-once）。并发扩吞吐 = 项目侧创建多个 Consumer 实例（RabbitMQ 共享队列原生负载均衡），单 Consumer 内不并发。

**优雅停机**：`Close()`（幂等）或 `Connect` 传入的 ctx 取消即触发——停止取新消息、等待在途 handler 完成后 `Run` 返回 nil；连接断开时 `Run` 不退出（等 guardian 重连恢复订阅后继续消费）。

## 5. FAQ

**Q：为什么断开期间 Publish 直接报错，而不把消息缓存起来等重连后补发？**

因为静默缓存会把失败藏起来：消息滞留在进程内存里，延迟不可见、进程崩溃即丢，调用方却以为发布成功。本包选择把失败如实、立即地交还调用方（ErrNotConnected），由调用方按业务语义决定重试、落库还是告警。task 场景的答案更高一层：任务已落表（表是真相源），投递失败只退化为延迟，不丢任务。

**Q：怎么提高消费吞吐？**

单 Consumer 是串行一次一条（顺序性与 QoS 语义的最简实现），扩吞吐的方式是创建多个 Consumer 实例（同一 queue 名）——RabbitMQ 共享队列原生负载均衡，多实例自动分摊消息。必要时配合 `WithPrefetch` 调整 broker 预推量。

**Q：为什么不做 Kafka / 通用 MQ 抽象层？**

AMQP 与 Kafka 的编程模型（exchange/binding vs topic/partition、push vs pull、确认语义）差异大到「统一接口」只能取两者交集的残集。本包只把 RabbitMQ 一件事做对（含官方客户端缺失的自动重连）；需要 Kafka 的项目在各自组件侧定义接口自实现（如 task 的 `QueueProducer` 就是单方法窄接口，实现成本极低）。

**Q：消息会丢吗？**

本包提供的是可靠性工具而非保证：消息默认 persistent（delivery_mode=2）+ 拓扑 durable（由调用方声明时勾选）+ 可选 confirm，三者齐备时 broker 侧丢失窗口极小；消费侧是 at-least-once（Ack 失败未确认的消息会被 broker 重投），handler 须容忍重复消息。发布到断开重连的窗口期消息一定失败（如实报错），不丢的前提是调用方处理了该错误。

**Q：Confirm 模式该开吗？**

按可靠性需求决定。默认关：RabbitMQ 持久化消息的落盘确认本就异步，confirm 增加一次同步等待、且 confirm 模式下 Publish 全程串行（避免确认归属错乱），吞吐有损。开了 confirm 则 broker nack / channel 中途关闭 / ctx 取消都会如实报错——「消息到底进没进 broker」的答案是确定的。

## 6. 维护者注记

**测试形态**：包内单测（全经 fake conn/channel 注入，不依赖真 broker）——`conn_test.go` 12 用例（重连状态机：断连→退避节奏→重声明/重订阅调用序列、Connect 幂等、停机路径）；`consumer_test.go` 10 用例（NackPolicy 分派矩阵、panic 隔离、ctx 取消停机在途消息、消息流关闭续跑）；`producer_test.go` 8 用例（发布参数、断开立即报错、confirm 等待/nack/取消）；`topology_test.go` 4 用例（声明参数序列与幂等重放）；`rabbitmq_test.go` 3 用例（Config 默认值补齐、URL 拼接含特殊字符转义、退避公式）。另有真 broker 集成 `tests/rabbitmq_test.go` 两用例（`AMQP_DSN` 注入、缺失 Skip）：TaskTopology 声明与同参重声明幂等、发布→消费往返；断开期以 Close 模拟（Close 后 Publish 立即 ErrNotConnected、断开期第三方发布的 durable 消息不丢、新客户端重声明同拓扑后续消费成功）——连接级 guardian 断线自动重连无法在真 broker 上注入触发，由主库 fake 单测覆盖。

**amqp091-go 的窄接口隔离**：全包与 amqp091-go 的交互收窄为 `amqpChannel` / `amqpConn` 两个非导出接口（conn.go），`*amqp.Channel` / `*amqp.Connection` 天然满足（后者经 `realConn` 适配 Channel 返回类型）——这既是单测 fake 注入点，也把「官方客户端不重连、需自管」的复杂度圈在 conn.go 一处。

**恢复失败的分层语义**（establish）：拓扑声明失败即整体失败（连接不可用不该被掩盖）；单个 producer/consumer 恢复失败仅记日志跳过、留待下次重连重试（不应因单个订阅问题丢弃整条可用连接）。channel 分配失败的 producer 会 detach，其 Publish 立即报 ErrNotConnected 直到下次重连恢复。

**依赖方向**：rabbitmq → worker（Consumer 实现 worker.Worker，可注册进 WorkerManager）。task 场景的桥接在 `task/amqp` 子包（→ task + rabbitmq），两个被桥接包互不感知。

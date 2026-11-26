// Package rabbitmq RabbitMQ 通用基础组件：连接生命周期管理 + 拓扑声明 + 生产/消费。
//
// 组件定位：RabbitMQ 专属实现，零业务语义——本包是面向 RabbitMQ 的基础设施封装，
// 不是 AMQP/Kafka 抽象层（QueueProducer 等业务接口在各自组件侧定义，如 task）。
// 唯一外部依赖 github.com/rabbitmq/amqp091-go 是 RabbitMQ 官方 Go 客户端，
// 它不内置自动重连——连接/channel 生命周期（断线退避重连、拓扑重声明、订阅恢复）
// 全部由本包自管，这是本包存在的核心价值。
//
// 核心行为契约：
//   - Client 内 guardian goroutine 监听断连，按 1s·2^n 封顶 30s（可配）退避重连，
//     成功后自动重声明拓扑、重建 channel、恢复订阅。
//   - Producer 与每个 Consumer 各独占一条 channel（confirm 与 QoS 均为 channel 级开关）。
//   - 连接断开期间 Publish 立即返回 error，绝不静默缓存重发。
//   - 消费循环串行一次一条；handler panic 被 recover 隔离，按 NackPolicy 处置消息。
//   - 一切项目参数（主机/凭证/心跳/退避）经 Config 注入，本库不硬编码任何环境决策。
package rabbitmq

import (
	"fmt"
	"net/url"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Config 连接与重连参数（构造注入，库不做项目决策）。
// 全部字段均有合理默认值（见 DefaultConfig），零值 Config 等价于 DefaultConfig。
type Config struct {
	Host     string // 主机，默认 127.0.0.1
	Port     int    // 端口，默认 5672
	User     string // 用户名，默认 guest
	Password string // 密码，默认 guest
	Vhost    string // 虚拟主机，默认 "/"

	// Heartbeat AMQP 心跳间隔，默认 10s（<1s 时由 broker 按自身间隔协商）
	Heartbeat time.Duration

	// ReconnectBackoffBase 断线重连退避基数，默认 1s（第 n 次重试等待 base·2^(n-1)）
	ReconnectBackoffBase time.Duration

	// ReconnectBackoffCap 退避上限，默认 30s
	ReconnectBackoffCap time.Duration
}

// 默认参数（Config 零值兜底，不构成对项目环境的任何假设）
const (
	defaultHost               = "127.0.0.1"
	defaultPort               = 5672
	defaultUser               = "guest"
	defaultPassword           = "guest"
	defaultVhost              = "/"
	defaultHeartbeat          = 10 * time.Second
	defaultBackoffBase        = 1 * time.Second
	defaultBackoffCap         = 30 * time.Second
	consumerDefaultPrefetch   = 10
	producerDefaultContentTyp = "application/octet-stream"
)

// DefaultConfig 返回带全部默认值的 Config（guest/guest@127.0.0.1 仅是 AMQP 客户端
// 库的常规出厂默认，生产环境必须用真实参数覆盖）。
func DefaultConfig() Config {
	return Config{
		Host:                 defaultHost,
		Port:                 defaultPort,
		User:                 defaultUser,
		Password:             defaultPassword,
		Vhost:                defaultVhost,
		Heartbeat:            defaultHeartbeat,
		ReconnectBackoffBase: defaultBackoffBase,
		ReconnectBackoffCap:  defaultBackoffCap,
	}
}

// withDefaults 以默认值补齐零值字段，返回规范化后的副本
func (c Config) withDefaults() Config {
	if c.Host == "" {
		c.Host = defaultHost
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	if c.User == "" {
		c.User = defaultUser
	}
	if c.Password == "" {
		c.Password = defaultPassword
	}
	if c.Vhost == "" {
		c.Vhost = defaultVhost
	}
	if c.Heartbeat <= 0 {
		c.Heartbeat = defaultHeartbeat
	}
	if c.ReconnectBackoffBase <= 0 {
		c.ReconnectBackoffBase = defaultBackoffBase
	}
	if c.ReconnectBackoffCap <= 0 {
		c.ReconnectBackoffCap = defaultBackoffCap
	}
	if c.ReconnectBackoffCap < c.ReconnectBackoffBase {
		c.ReconnectBackoffCap = c.ReconnectBackoffBase
	}
	return c
}

// URL 拼接 amqp:// 连接串（用户名/密码经 url.UserPassword 转义——
// PathEscape 不转义 @，密码含 @ 时会破坏 URL 结构；vhost 做路径转义，"/" → "%2F"）
func (c Config) URL() string {
	return fmt.Sprintf("amqp://%s@%s:%d/%s",
		url.UserPassword(c.User, c.Password).String(), c.Host, c.Port,
		url.PathEscape(c.Vhost))
}

// amqpConfig 转换为 amqp091-go 的连接配置（PLAIN 认证 + 心跳 + vhost）
func (c Config) amqpConfig() amqp.Config {
	return amqp.Config{
		Vhost:     c.Vhost,
		Heartbeat: c.Heartbeat,
		SASL: []amqp.Authentication{
			&amqp.PlainAuth{Username: c.User, Password: c.Password},
		},
	}
}

// backoff 计算第 attempt 次重连的退避时长：base·2^(attempt-1) 封顶 cap。
// 逐次翻倍并在触达 cap 时提前返回，避免大 attempt 位移溢出。
func (c Config) backoff(attempt int) time.Duration {
	d := c.ReconnectBackoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= c.ReconnectBackoffCap {
			return c.ReconnectBackoffCap
		}
	}
	if d > c.ReconnectBackoffCap {
		d = c.ReconnectBackoffCap
	}
	return d
}

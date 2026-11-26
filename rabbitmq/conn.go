package rabbitmq

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrNotConnected 连接未建立或已断开时的统一错误。
// Publish 在断开期间立即返回本错误，绝不静默缓存重发（调用方自行决定重试策略）。
var ErrNotConnected = errors.New("rabbitmq: 连接未建立或已断开")

// amqpChannel channel 级窄接口：全包与 amqp091-go channel 的唯一交互面。
// *amqp.Channel 天然实现本接口；单测注入 fakeChannel。
type amqpChannel interface {
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error
	Qos(prefetchCount, prefetchSize int, global bool) error
	Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
	PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
	Confirm(noWait bool) error
	NotifyPublish(confirm chan amqp.Confirmation) chan amqp.Confirmation
	NotifyReturn(c chan amqp.Return) chan amqp.Return
	NotifyClose(c chan *amqp.Error) chan *amqp.Error
	Close() error
}

// amqpConn 连接级窄接口：全包与 amqp091-go connection 的唯一交互面。
// *amqp.Connection 的 Channel() 返回具体类型，无法直接满足本接口，
// 由 realConn 适配（见下）；单测注入 fakeConn。
type amqpConn interface {
	Channel() (amqpChannel, error)
	NotifyClose(c chan *amqp.Error) chan *amqp.Error
	Close() error
}

// dialer 拨号函数类型：生产实现为 amqpDial（amqp.DialConfig 适配），
// 单测注入 fake 以覆盖重连状态机，不依赖真 broker。
type dialer func(url string, cfg amqp.Config) (amqpConn, error)

// amqpDial 生产拨号实现：amqp.DialConfig + realConn 适配
func amqpDial(url string, cfg amqp.Config) (amqpConn, error) {
	conn, err := amqp.DialConfig(url, cfg)
	if err != nil {
		return nil, err
	}
	return realConn{c: conn}, nil
}

// realConn *amqp.Connection → amqpConn 适配器（仅做 Channel 返回类型的接口化）
type realConn struct{ c *amqp.Connection }

func (r realConn) Channel() (amqpChannel, error) { return r.c.Channel() }
func (r realConn) NotifyClose(c chan *amqp.Error) chan *amqp.Error {
	return r.c.NotifyClose(c)
}
func (r realConn) Close() error { return r.c.Close() }

// Client RabbitMQ 连接客户端：持有到 broker 的唯一连接，负责
// 连接生命周期（guardian 断线重连）、拓扑注册表重放、Producer/Consumer
// 的 channel 分配（各独占一条 channel）。
//
// 使用形态：NewClient → Connect（首连失败如实返回 error）→ Declare/NewProducer/NewConsumer
// → Close 优雅停机；断线后的恢复全自动，无需调用方参与。
type Client struct {
	cfg  Config
	dial dialer

	mu        sync.Mutex
	conn      amqpConn
	connected bool
	readyCh   chan struct{} // 「当前连接已就绪」广播：就绪时 close，断开时换新

	topologies []Topology  // 已声明拓扑注册表（重连后逐项重放，声明幂等见 D3）
	producers  []*Producer // channel 持有者注册表（重连后逐个重建 channel）
	consumers  []*Consumer // 订阅注册表（重连后逐个重订阅）

	lifecycleCtx context.Context // Connect 传入的 ctx：取消即优雅停机
	stopCh       chan struct{}   // Close 停机信号
	stopOnce     sync.Once       // 防 stopCh 重复 close
	guardianDone chan struct{}   // guardian 退出信号（Connect 成功后创建）
}

// NewClient 创建客户端。cfg 零值字段以默认值补齐（见 DefaultConfig）。
func NewClient(cfg Config) *Client {
	return &Client{
		cfg:     cfg.withDefaults(),
		dial:    amqpDial,
		readyCh: make(chan struct{}),
		stopCh:  make(chan struct{}),
	}
}

// Connect 建立首个连接并启动 guardian：同步拨号（失败如实返回 error，
// 不做内部重试——启动期失败让调用方尽早感知配置问题），成功后依次
// 重放已注册拓扑、为已注册 Producer/Consumer 分配 channel，随后由
// guardian 监听断连并自动恢复。ctx 取消等同 Close（优雅停机）。
// 幂等：guardian 已在运行时再调用直接返回成功并丢弃新拨的连接。
func (c *Client) Connect(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := c.dial(c.cfg.URL(), c.cfg.amqpConfig())
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.guardianDone != nil {
		c.mu.Unlock()
		conn.Close()
		return nil
	}
	c.lifecycleCtx = ctx
	c.guardianDone = make(chan struct{})
	c.mu.Unlock()
	if err := c.establish(conn); err != nil {
		c.mu.Lock()
		c.guardianDone = nil
		c.lifecycleCtx = nil
		c.mu.Unlock()
		conn.Close()
		return err
	}
	c.mu.Lock()
	done := c.guardianDone
	c.mu.Unlock()

	go func() {
		defer close(done)
		c.guardian(conn)
	}()
	return nil
}

// Close 优雅停机：通知 guardian 退出（关闭连接）、等待其结束。
// 幂等，可重复调用；未 Connect 过则直接返回。
func (c *Client) Close() {
	c.stopOnce.Do(func() { close(c.stopCh) })
	c.mu.Lock()
	done := c.guardianDone
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

// ready 返回当前连接就绪信号（就绪时被 close；断开后换新 chan）
func (c *Client) ready() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readyCh
}

// guardian 断线重连状态机（goroutine，Connect 成功后启动）：
//
//	serve（监听 NotifyClose）──断连──▶ markDisconnected ──▶ reconnect
//	   │                                                  （退避 base·2^n 封顶 cap）
//	   └─ 停机信号 ──▶ 关连接、退出
//
// reconnect 成功即回到 establish（重放拓扑 → 重建 producer channel →
// 重订阅 consumer），随后继续 serve 监听新连接。
func (c *Client) guardian(conn amqpConn) {
	for {
		if !c.serve(conn) {
			return
		}
		c.markDisconnected()
		next, ok := c.reconnect()
		if !ok {
			return
		}
		conn = next
	}
}

// serve 监听连接关闭事件。返回 true 表示连接断开（调用方应重连），
// false 表示客户端停机（调用方应退出）。
func (c *Client) serve(conn amqpConn) bool {
	closeCh := conn.NotifyClose(make(chan *amqp.Error, 1))
	select {
	case err := <-closeCh:
		g.Log().Warningf(context.Background(), "rabbitmq 连接断开: %v，开始自动重连", err)
		return true
	case <-c.stopCh:
		c.shutdownConn(conn, "客户端 Close")
		return false
	case <-c.lifecycleDone():
		c.shutdownConn(conn, "生命周期 ctx 取消")
		return false
	}
}

// lifecycleDone lifecycle ctx 的 Done 信号（ctx 未设置时返回 nil chan 永不触发）
func (c *Client) lifecycleDone() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lifecycleCtx == nil {
		return nil
	}
	return c.lifecycleCtx.Done()
}

// shutdownConn 停机路径：标记断开并关闭底层连接（释放资源）
func (c *Client) shutdownConn(conn amqpConn, reason string) {
	c.markDisconnected()
	if err := conn.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
		g.Log().Debugf(context.Background(), "rabbitmq 停机关闭连接（%s）: %v", reason, err)
	}
}

// reconnect 退避重连循环：base·2^(n-1) 封顶 cap，成功 establish 后返回新连接；
// 停机信号触发时返回 false。
func (c *Client) reconnect() (amqpConn, bool) {
	for attempt := 1; ; attempt++ {
		select {
		case <-c.stopCh:
			return nil, false
		case <-c.lifecycleDone():
			return nil, false
		case <-time.After(c.cfg.backoff(attempt)):
		}
		conn, err := c.dial(c.cfg.URL(), c.cfg.amqpConfig())
		if err != nil {
			g.Log().Warningf(context.Background(),
				"rabbitmq 第 %d 次重连失败（%v 后重试）: %v", attempt, c.cfg.backoff(attempt+1), err)
			continue
		}
		if err := c.establish(conn); err != nil {
			g.Log().Errorf(context.Background(),
				"rabbitmq 重连成功但恢复拓扑/订阅失败（%v 后重试）: %v", c.cfg.backoff(attempt+1), err)
			conn.Close()
			continue
		}
		g.Log().Infof(context.Background(),
			"rabbitmq 第 %d 次重连成功：拓扑已重声明、channel 已重建、订阅已恢复", attempt)
		return conn, true
	}
}

// watchChannel channel 级自愈（D1：监听 conn.NotifyClose 与各 channel NotifyClose）：
// channel 单独被 broker 关闭（连接仍在，如 queue 被删触发 404 关 channel）时，
// 立即重建该组件的 channel/订阅；连接级断开由 guardian 统一处理——此时
// c.conn 已换/清，本函数直接退出不重复重建。restore 必须在 c.mu 持有下可安全调用。
func (c *Client) watchChannel(ch amqpChannel, conn amqpConn, owner string, restore func() error) {
	closeCh := ch.NotifyClose(make(chan *amqp.Error, 1))
	go func() {
		select {
		case err := <-closeCh:
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.conn != conn || !c.connected {
				return // 连接级断开/已换新连接：交给 guardian 的 markDisconnected
			}
			g.Log().Warningf(context.Background(),
				"rabbitmq %s 的 channel 被关闭（%v），连接仍在——单独重建", owner, err)
			if err := restore(); err != nil {
				g.Log().Errorf(context.Background(),
					"rabbitmq %s channel 重建失败（等待下次重连）: %v", owner, err)
			}
		case <-c.stopCh:
		case <-c.lifecycleDone():
		}
	}()
}

// attachProducerLocked 在 client.mu 持有下为 producer 分配 channel 并挂
// channel 级自愈（channel 被单独关闭时自递归重建）。分配失败仅 detach
// （producer 的 Publish 报 ErrNotConnected，等下次重连恢复）。
func (c *Client) attachProducerLocked(p *Producer, conn amqpConn) {
	ch, err := conn.Channel()
	if err != nil {
		g.Log().Errorf(context.Background(), "rabbitmq producer channel 分配失败: %v", err)
		p.detach()
		return
	}
	p.attach(ch)
	var watch func(cur amqpChannel)
	watch = func(cur amqpChannel) {
		c.watchChannel(cur, conn, "producer", func() error {
			nch, err := conn.Channel()
			if err != nil {
				return err
			}
			p.attach(nch)
			watch(nch) // 重建的 channel 继续挂自愈
			return nil
		})
	}
	watch(ch)
}

// subscribeConsumerLocked 在 client.mu 持有下为 consumer （重）订阅并挂
// channel 级自愈。订阅失败记日志返回（等下次重连）。
func (c *Client) subscribeConsumerLocked(cons *Consumer, conn amqpConn) {
	if cons.isStopped() {
		return
	}
	cons.detach()
	if err := cons.subscribe(conn); err != nil {
		g.Log().Errorf(context.Background(),
			"rabbitmq consumer %s 订阅失败（等待下次重连）: %v", cons.name, err)
		return
	}
	var watch func(cur amqpChannel)
	watch = func(cur amqpChannel) {
		c.watchChannel(cur, conn, cons.name, func() error {
			if cons.isStopped() {
				return nil
			}
			cons.detach()
			if err := cons.subscribe(conn); err != nil {
				return err
			}
			cons.mu.Lock()
			nch := cons.ch
			cons.mu.Unlock()
			watch(nch) // 重建的订阅继续挂自愈
			return nil
		})
	}
	cons.mu.Lock()
	consCh := cons.ch
	cons.mu.Unlock()
	watch(consCh)
}

// establish 连接就绪路径：重放拓扑注册表 → 标记就绪 → 重建 producer channel
// 与 consumer 订阅。任一拓扑声明失败即整体失败（同名异参 406 如实报错，
// 配置漂移早暴露）；单个 producer/consumer 恢复失败仅记日志跳过，
// 留待下次重连重试（连接本身已可用，不应因单个订阅问题丢弃整条连接）。
func (c *Client) establish(conn amqpConn) error {
	c.mu.Lock()
	topologies := make([]Topology, len(c.topologies))
	copy(topologies, c.topologies)
	producers := make([]*Producer, len(c.producers))
	copy(producers, c.producers)
	consumers := make([]*Consumer, len(c.consumers))
	copy(consumers, c.consumers)
	c.mu.Unlock()

	for _, topo := range topologies {
		ch, err := conn.Channel()
		if err != nil {
			return err
		}
		err = topo.declareOn(ch)
		ch.Close()
		if err != nil {
			return err
		}
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	close(c.readyCh)
	for _, p := range producers {
		c.attachProducerLocked(p, conn)
	}
	for _, cons := range consumers {
		c.subscribeConsumerLocked(cons, conn)
	}
	c.mu.Unlock()
	return nil
}

// markDisconnected 标记断开：清连接、换新就绪信号、通知全部
// producer/consumer 释放 channel（producer 的后续 Publish 立即报错）
func (c *Client) markDisconnected() {
	c.mu.Lock()
	c.conn = nil
	c.connected = false
	c.readyCh = make(chan struct{})
	for _, p := range c.producers {
		p.detach()
	}
	for _, cons := range c.consumers {
		cons.detach()
	}
	c.mu.Unlock()
}

// Declare 声明拓扑并登记进注册表（重连后自动重放）。
// AMQP 声明本身幂等——同名同参重复无害；同名异参 broker 返回 406，
// 本方法如实透传该错误（配置漂移早暴露，不做静默吞并）。
// 必须在 Connect 之后调用，未连接时返回 ErrNotConnected。
func (c *Client) Declare(ctx context.Context, topo Topology) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return ErrNotConnected
	}
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := topo.declareOn(ch); err != nil {
		return err
	}
	c.topologies = append(c.topologies, topo)
	return nil
}

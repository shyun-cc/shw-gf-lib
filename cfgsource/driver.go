package cfgsource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/os/gcfg"
)

// Driver 配置源驱动：向组件提供全量配置的扁平点分键集合。
//
// 契约：
//   - key 为扁平点分键（如 "oss.bucket"），无点分纯名同样合法；value 为原始
//     字符串，组件不做任何类型解析；
//   - 返回 (nil, err) 表示本轮刷新失败，组件保旧快照并记错误日志；
//   - 返回 (空 map, nil) 为合法的全量空配置，组件清空 overlay；
//   - 组件每次 Load 后复制持有返回 map，Driver 复用返回 map 不影响组件状态；
//   - Load 的调用经 single-flight 串行化（同一时刻至多一次在飞），实现应无状态、
//     可重复调用：每轮刷新都会完整调用一次 Load。
type Driver interface {
	// Load 拉取一份全量配置（扁平点分键 → 原始字符串值）。
	Load(ctx context.Context) (map[string]string, error)
}

// Config 组件构造参数。
type Config struct {
	// Driver overlay 配置源，必填；为 nil 时 New 返回错误。
	Driver Driver

	// RefreshInterval 后台轮询间隔，默认 5s；≤0 回落默认值。
	// 不支持禁用轮询——组件无推送源，禁用即配置永不收敛。
	RefreshInterval time.Duration
}

// defaultRefreshInterval RefreshInterval 未指定（≤0）时的回落默认值。
const defaultRefreshInterval = 5 * time.Second

// New 创建合并快照配置适配器并启动后台轮询。
//
// 执行序为「先 base 后 overlay」的急加载：New 返回时快照至少含文件内容，
// 保证 SetAdapter 后框架启动读 database 等文件配置必命中。fail-open 语义：
//   - Driver 急加载失败不阻断，返回 (adapter, err) 双值，adapter 仍可用
//     （快照退化为纯文件内容），调用方可自行决定是否记日志后继续；
//   - 文件 base 读取失败仅告警，以空 base 继续构建。
//
// 用毕须调用 Close 停止后台轮询。
func New(ctx context.Context, cfg Config) (*Adapter, error) {
	if cfg.Driver == nil {
		return nil, errors.New("cfgsource: Driver 不能为空")
	}
	baseAdapter, err := gcfg.NewAdapterFile()
	if err != nil {
		return nil, fmt.Errorf("cfgsource: 创建文件适配器失败: %w", err)
	}
	interval := cfg.RefreshInterval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	a := &Adapter{
		driver:          cfg.Driver,
		baseAdapter:     baseAdapter,
		refreshInterval: interval,
		snapshot:        gvar.New(nil, true),
		registry:        gcfg.NewWatcherRegistry(),
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	// 急加载与后续 ticker / Refresh 汇入同一构建入口（initial 模式容忍文件 base 失败）。
	err = a.doRefresh(ctx, true)
	a.startTicker()
	return a, err
}

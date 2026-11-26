// Package cfgsource 「文件打底 + Driver 覆盖」的配置合并适配器：把任意动态配置源
// （远程配置中心、DB、HTTP 接口等）以 gcfg.Adapter 形式接入 GoFrame 配置体系，
// 业务侧仅需实现单方法 Driver 接口。
//
// 核心行为：
//   - 合并快照：内部以 gcfg.NewAdapterFile 文件适配器为 base，Driver 全量配置为
//     overlay，深合并为单一内存快照（文件同名键被 Driver 键覆盖，其余各归其位）；
//     全部读取路径只查内存快照零 IO，快照因文件打底永不依赖 Driver 可用性。
//   - 定时刷新：默认每 5s 轮询一轮（RefreshInterval 可调，不支持禁用），所有触发
//     源（New 急加载、ticker 轮询、Refresh）经手写 single-flight 合并为一次执行；
//     内容有变才原子换绑并通知 watcher（diff 过滤），内容未变零通知。
//   - fail-open：急加载 Driver 失败不阻断启动，New 返回 (adapter, err) 双值且
//     adapter 可用（快照退化为纯文件内容）；运行中刷新失败保旧快照，仅记错误日志。
//
// 标准接入：
//
//	driver := NewXxxDriver(...) // 业务实现 cfgsource.Driver
//	adapter, err := cfgsource.New(ctx, cfgsource.Config{Driver: driver})
//	if err != nil {
//	    // 急加载失败的降级处理：记日志后仍可挂载 adapter
//	}
//	g.Cfg().SetAdapter(adapter)
package cfgsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/encoding/gjson"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
)

var (
	// Compile-time checking for interface implementation.
	_ gcfg.Adapter        = (*Adapter)(nil)
	_ gcfg.WatcherAdapter = (*Adapter)(nil)
)

// snapshot 单一不可变合并快照：发布后只读、永不改写，换绑即整体替换新实例，
// 因此并发读无需加锁（gjson 以非 safe 模式构建，仅走只读路径）。
type snapshot struct {
	json    *gjson.Json    // 嵌套树只读读取入口（Get）
	data    map[string]any // 合并嵌套树原 map（Data 深拷贝来源）
	raw     []byte         // 合并树 json.Marshal 结果（diff 基准；map 键有序故序列化确定性成立）
	isEmpty bool           // 顶层是否为空（Available 判定）
}

// refreshCall 一轮刷新的 single-flight 令牌：等待者经 WaitGroup 共享执行者结果
// （含同一 error），Done 先于 Wait 返回建立 happens-before，结果读取无数据竞争。
type refreshCall struct {
	wg  sync.WaitGroup
	err error
}

// Adapter cfgsource 适配器主体：文件打底 + Driver 覆盖的合并快照配置适配器，
// 实现 gcfg.Adapter 与 gcfg.WatcherAdapter，可直接经 g.Cfg().SetAdapter 挂载。
type Adapter struct {
	driver          Driver                // overlay 配置源（业务注入）
	baseAdapter     *gcfg.AdapterFile     // 文件打底适配器（gcfg.NewAdapterFile 产物）
	refreshInterval time.Duration         // 后台轮询间隔
	snapshot        *gvar.Var             // 当前快照（*snapshot；safe 模式原子换绑）
	registry        *gcfg.WatcherRegistry // watcher 注册表（通知经 diff 过滤）
	ctx             context.Context       // 内部生命周期 context（轮询退出 + 换绑竞态检查）
	cancel          context.CancelFunc
	closeOnce       sync.Once    // Close 幂等
	sfMu            sync.Mutex   // single-flight 互斥（保护 refreshing）
	refreshing      *refreshCall // 在飞刷新令牌；nil 表示当前无在飞刷新
}

// Available 返回快照存在且内容非空：纯文件快照即 true；构造全败的空快照为
// false；运行中刷新失败保旧快照，不翻转该值。
func (a *Adapter) Available(ctx context.Context, resource ...string) bool {
	s, _ := a.snapshot.Val().(*snapshot)
	return s != nil && !s.isEmpty
}

// Get 按点分 pattern 读取内存快照（如 "oss.bucket"、"list.0.name"），零 IO。
// miss 返回 (nil, nil)，默认值由 gcfg 层 def 参数承接。
func (a *Adapter) Get(ctx context.Context, pattern string) (value any, err error) {
	s, _ := a.snapshot.Val().(*snapshot)
	if s == nil {
		return nil, nil
	}
	return s.json.Get(pattern).Val(), nil
}

// Data 返回当前快照的递归深拷贝，调用方改写不影响内部状态。
func (a *Adapter) Data(ctx context.Context) (data map[string]any, err error) {
	s, _ := a.snapshot.Val().(*snapshot)
	if s == nil {
		return nil, nil
	}
	return cloneTree(s.data), nil
}

// AddWatcher 注册 watcher。通知时机经 diff 过滤：仅快照内容有变的换绑会触发，
// 内容未变的轮询零通知。
func (a *Adapter) AddWatcher(name string, fn gcfg.WatcherFunc) {
	a.registry.Add(name, fn)
}

// RemoveWatcher 移除指定名称的 watcher。
func (a *Adapter) RemoveWatcher(name string) {
	a.registry.Remove(name)
}

// GetWatcherNames 返回全部 watcher 名称。
func (a *Adapter) GetWatcherNames() []string {
	return a.registry.GetNames()
}

// IsWatching 返回指定名称的 watcher 是否已注册。
func (a *Adapter) IsWatching(name string) bool {
	return a.registry.IsWatching(name)
}

// Refresh 立即触发一轮刷新（与 ticker 轮询汇入同一 single-flight 入口）。
// 刷新失败本轮不换绑、保旧快照，错误原样返回调用方。
func (a *Adapter) Refresh(ctx context.Context) error {
	return a.doRefresh(ctx, false)
}

// Close 停止后台轮询，幂等可重复调用。Close 后 Get / Data 仍可读最后一份快照；
// 与在飞刷新竞态时，换绑前检查生命周期已取消并放弃换绑，快照保持 Close 前状态。
func (a *Adapter) Close() error {
	a.closeOnce.Do(func() {
		a.cancel()
	})
	return nil
}

// doRefresh 所有刷新触发源（New 急加载、ticker 轮询、Refresh）的唯一汇合入口。
// 手写单 key single-flight：并发触发合并为一轮执行，等待者共享执行者的结果
// （含同一 error），保证同一时刻至多一次 Driver Load。
func (a *Adapter) doRefresh(ctx context.Context, initial bool) error {
	a.sfMu.Lock()
	if call := a.refreshing; call != nil {
		a.sfMu.Unlock()
		call.wg.Wait()
		return call.err
	}
	call := &refreshCall{}
	call.wg.Add(1)
	a.refreshing = call
	a.sfMu.Unlock()

	err := a.buildAndSwap(ctx, initial)

	a.sfMu.Lock()
	a.refreshing = nil
	a.sfMu.Unlock()
	call.err = err
	call.wg.Done()
	return err
}

// buildAndSwap 一轮快照构建与换绑：
//  1. base ← 文件适配器 Data（文件缺失返回 (nil, nil)，空 base 合法；解析失败返回 (nil, err)）；
//  2. overlay ← Driver.Load（(nil, err) 本轮失败；(空 map, nil) 合法清空 overlay）；
//  3. overlay 扁平键 transform 为嵌套树，与 base 副本深合并（Driver 深层键覆盖
//     base 同名子键，未及的 base 键保留）；
//  4. 新快照与当前快照 json 序列化 diff——有变才原子换绑并通知 watcher。
//
// 失败分层：base 或 overlay 任一失败则本轮不换绑保旧快照并返回错误，错误日志
// 在本函数内完成（Refresh 把错误交还调用方、ticker 轮询仅记日志，均不重复记）。
// initial 急加载模式放宽：base 失败降级为空 base 继续；overlay 失败仍发布纯
// 文件快照（fail-open），错误返回 New 交由调用方处置。
func (a *Adapter) buildAndSwap(ctx context.Context, initial bool) error {
	base, err := a.baseAdapter.Data(ctx)
	if err != nil {
		if !initial {
			g.Log().Errorf(ctx, "cfgsource 刷新配置失败: %v", err)
			return err
		}
		g.Log().Warningf(ctx, "cfgsource 初始加载文件配置失败，将以空文件配置构建: %v", err)
		base = nil
	}
	if base == nil {
		base = map[string]any{}
	}

	overlay, err := a.driver.Load(ctx)
	if err != nil {
		g.Log().Errorf(ctx, "cfgsource 刷新配置失败: %v", err)
		if initial {
			// fail-open：急加载 Driver 失败不阻断，快照退化为纯文件内容。
			_ = a.publish(ctx, base)
		}
		return err
	}

	merged := cloneTree(base)
	mergeOverlay(merged, buildOverlayTree(ctx, overlay))
	return a.publish(ctx, merged)
}

// publish 构建不可变快照并按 diff 结果换绑：内容与当前快照一致则静默返回；
// 有变则原子换绑并通知全部 watcher。换绑前检查生命周期——内部 context 已取消
// （Close 竞态）则放弃换绑，不覆盖快照、不通知。
func (a *Adapter) publish(ctx context.Context, merged map[string]any) error {
	raw, err := json.Marshal(merged)
	if err != nil {
		// 嵌套树只含 JSON 可序列化值（文件解码值 + Driver 字符串），
		// 序列化失败按刷新失败处置：本轮不换绑。
		g.Log().Errorf(ctx, "cfgsource 刷新配置失败: %v", err)
		return err
	}
	if old, _ := a.snapshot.Val().(*snapshot); old != nil && bytes.Equal(old.raw, raw) {
		return nil
	}
	select {
	case <-a.ctx.Done():
		return nil
	default:
	}
	a.snapshot.Set(&snapshot{
		json:    gjson.New(merged),
		data:    merged,
		raw:     raw,
		isEmpty: len(merged) == 0,
	})
	a.registry.Notify(ctx)
	return nil
}

// startTicker 启动后台轮询 goroutine：按 refreshInterval 周期经 single-flight
// 入口触发刷新；轮询失败仅记日志（buildAndSwap 内完成）保旧快照；Close 取消
// 内部 context 后退出。
func (a *Adapter) startTicker() {
	go func() {
		ticker := time.NewTicker(a.refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				_ = a.doRefresh(a.ctx, false)
			}
		}
	}()
}

// cloneTree 递归深拷贝嵌套树：map / slice 逐层克隆，标量值不可变直接透传。
// 个别格式解码器可能产出 any 键 map，统一转为 string 键保证结果可 JSON 序列化。
func cloneTree(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = cloneValue(v)
	}
	return dst
}

// cloneValue 递归克隆单个值；仅 map / slice 需要克隆，其余类型不可变。
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneTree(t)
	case map[any]any:
		dst := make(map[string]any, len(t))
		for k, val := range t {
			dst[fmt.Sprint(k)] = cloneValue(val)
		}
		return dst
	case []any:
		dst := make([]any, len(t))
		for i, item := range t {
			dst[i] = cloneValue(item)
		}
		return dst
	default:
		return v
	}
}

// mergeOverlay 将 overlay 子树深合并进 dst（base 副本）：overlay 深层键覆盖
// base 同名子键，overlay 未及的 base 键原样保留。overlay 子树由 transform
// 新建、Driver 值为字符串标量，与 base 无共享引用，可直接挂接。
func mergeOverlay(dst map[string]any, overlay map[string]any) {
	for k, v := range overlay {
		if sub, ok := v.(map[string]any); ok {
			if dstSub, ok := dst[k].(map[string]any); ok {
				mergeOverlay(dstSub, sub)
				continue
			}
			dst[k] = sub
			continue
		}
		dst[k] = v
	}
}

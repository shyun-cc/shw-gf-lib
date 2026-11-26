// cfgsource 单元测试（change: cfgsource-foundation，tasks 2.1 + 2.2）。
//
// 覆盖 specs scenarios 全集：
//   - 基础面：overlay 覆盖 / 文件键保留 / 读取零 IO / (nil,err) 与 (空map,nil)
//     语义分离 / map 复制持有 / 嵌套化 / 裁深弃浅 / 拼装确定性 / fail-open
//     双返回 / Available / Get miss / Data 深拷贝 / SetAdapter 顺序契约 /
//     Driver 必填 / 间隔回落默认；
//   - 刷新链：轮询自动刷新 / 轮询失败保旧快照下轮重试 / Refresh 即时生效 /
//     single-flight 合并 / watcher 变化通知 / 不变零通知 / 回调 panic 隔离 /
//     gcfg.Loader Watch 路由 / Close 幂等。
//
// 关键测试手法：
//   - mockDriver：内嵌 map 底座 + 原子 Load 计数 + 可注入 error / sleep
//     （sleep 拉长 Load 执行窗口验证 single-flight）；shareMap 模式让 Load
//     直接返回内部 map 本体，验证的是「组件侧复制持有」而非 mock 自身复制；
//   - 文件 base：生产 New 内部自建 AdapterFile，默认搜索路径不含 testdata/，
//     故同包测试经私有字段 baseAdapter.SetPath("testdata") 指向 testdata 后
//     立即 Refresh 重建快照（SetPath 会清空文件缓存，重读必命中）；需要
//     「文件在急加载期即生效」的用例（fail-open / SetAdapter 顺序 / Available
//     空快照）按 New 的构建时序显式组装（assembleAdapter）；
//   - 时间敏感用例用 20–50ms 短间隔，等待一律经 channel 带超时接收或有界
//     轮询（time.Sleep 仅用于让轮询周期流逝，绝不做同步断言）。
package cfgsource

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/os/gcfg"
)

const (
	// testdataDir 文件 base 配置目录（cfgsource/testdata/config.yaml）。
	testdataDir = "testdata"

	// fileDatabaseLink testdata/config.yaml 中 database.default.link 的值，
	// 文件键保留 / fail-open / SetAdapter 顺序用例的共同断言依据。
	fileDatabaseLink = "pg://user:pass@127.0.0.1:5432/gflib_test"
)

// ---- mock driver ----

// mockDriver 测试用 Driver 实现：内嵌 map 底座 + Load 调用计数（原子）+
// 可注入 error / sleep。全部可变字段经 mu 保护，-race 下多 goroutine 安全。
type mockDriver struct {
	mu       sync.Mutex
	data     map[string]string
	err      error         // 非 nil 时 Load 返回该错误（模拟数据源不可用）
	sleep    time.Duration // Load 内 sleep，拉长执行窗口验证 single-flight
	shareMap bool          // true 时 Load 直接返回内部 map 本体（不复制）
	loads    atomic.Int64  // Load 调用计数
}

// newDriver 创建带初始配置的 mock driver。
func newDriver(initial map[string]string) *mockDriver {
	d := &mockDriver{data: make(map[string]string, len(initial))}
	for k, v := range initial {
		d.data[k] = v
	}
	return d
}

// Load 实现 Driver 接口：计数 + 可选 sleep + 错误注入 + 配置快照返回。
func (d *mockDriver) Load(ctx context.Context) (map[string]string, error) {
	d.loads.Add(1)
	if sleep := d.sleep; sleep > 0 {
		time.Sleep(sleep)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	if d.shareMap {
		return d.data, nil
	}
	out := make(map[string]string, len(d.data))
	for k, v := range d.data {
		out[k] = v
	}
	return out, nil
}

// setValue 修改单个键值（模拟远端配置被外部改写）。
func (d *mockDriver) setValue(key, val string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.data[key] = val
}

// setAll 整体替换配置（空 map 即合法的全量空配置）。
func (d *mockDriver) setAll(data map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.data = data
}

// setError 注入 / 清除 Load 错误。
func (d *mockDriver) setError(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
}

// loadCount 返回 Load 累计调用次数。
func (d *mockDriver) loadCount() int64 { return d.loads.Load() }

// ---- 构造辅助 ----

// newAdapter 走生产 New 构造 adapter，随后把私有字段 baseAdapter 指向
// testdata（New 内部自建的 AdapterFile 默认搜索路径不含 testdata/）并立即
// Refresh 一轮，快照即为「testdata/config.yaml ⊕ driver」合并结果。
// interval 传 0 走默认 5s（单测窗口内 ticker 不会触发，杜绝轮询干扰）。
func newAdapter(t *testing.T, d *mockDriver, interval time.Duration) *Adapter {
	t.Helper()
	ctx := context.Background()
	a, err := New(ctx, Config{Driver: d, RefreshInterval: interval})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.baseAdapter.SetPath(testdataDir); err != nil {
		t.Fatalf("SetPath(%s) 失败: %v", testdataDir, err)
	}
	if err := a.Refresh(ctx); err != nil {
		t.Fatalf("指向 testdata 后刷新失败: %v", err)
	}
	return a
}

// assembleAdapter 按 New 的构建时序显式组装 adapter（同包可触达私有字段）：
// 先备好 baseAdapter（withFileBase 决定是否指向 testdata）再执行急加载、
// 启动轮询，保证「文件 base 在 New 急加载期即生效」。用于 fail-open 双返回、
// SetAdapter 顺序契约与 Available 空快照用例；返回 New 语义的 (adapter, err) 双值。
func assembleAdapter(t *testing.T, d *mockDriver, withFileBase bool) (*Adapter, error) {
	t.Helper()
	baseAdapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("创建文件适配器失败: %v", err)
	}
	if withFileBase {
		if err := baseAdapter.SetPath(testdataDir); err != nil {
			t.Fatalf("SetPath(%s) 失败: %v", testdataDir, err)
		}
	}
	a := &Adapter{
		driver:          d,
		baseAdapter:     baseAdapter,
		refreshInterval: defaultRefreshInterval,
		snapshot:        gvar.New(nil, true),
		registry:        gcfg.NewWatcherRegistry(),
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	// 与 New 相同的「急加载 → 启动轮询」时序。
	adapterErr := a.doRefresh(context.Background(), true)
	a.startTicker()
	t.Cleanup(func() { _ = a.Close() })
	return a, adapterErr
}

// ---- 断言辅助 ----

// mustGetString Get 并断言结果为 string，返回该值。
func mustGetString(t *testing.T, a *Adapter, pattern string) string {
	t.Helper()
	v, err := a.Get(context.Background(), pattern)
	if err != nil {
		t.Fatalf("Get(%q) 报错: %v", pattern, err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("Get(%q) 期望 string，实际 %T（%v）", pattern, v, v)
	}
	return s
}

// notify 非阻塞投递一条通知（缓冲 4，多余丢弃）：watcher 回调可能因多轮
// 刷新被多次触发，阻塞发送或直接 close 都可能 panic。
func notify(ch chan string, msg string) {
	select {
	case ch <- msg:
	default:
	}
}

// waitNotify 带超时等待一条通知，超时即 Fatal。
func waitNotify(t *testing.T, ch chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		t.Fatalf("等待通知超时（%v）", timeout)
		return ""
	}
}

// waitForLoadCount 有界轮询等待 Load 计数达到 target（等待轮询周期流逝），
// 超时 Fatal。步进 2ms，对 20ms 级轮询间隔的时序扰动可忽略。
func waitForLoadCount(t *testing.T, d *mockDriver, target int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for d.loadCount() < target {
		if time.Now().After(deadline) {
			t.Fatalf("等待 Load 计数达到 %d 超时（当前 %d）", target, d.loadCount())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ---- 基础面（tasks 2.1）----

// TestNewDriverRequired Driver 必填：Config{} 构造返回 nil adapter 与明确错误。
func TestNewDriverRequired(t *testing.T) {
	t.Parallel()
	a, err := New(context.Background(), Config{})
	if a != nil {
		t.Fatalf("Driver 缺失时应返回 nil adapter，实际 %T", a)
	}
	if err == nil || err.Error() != "cfgsource: Driver 不能为空" {
		t.Fatalf("期望错误「cfgsource: Driver 不能为空」，实际 %v", err)
	}
}

// TestRefreshIntervalFallback 间隔回落：≤0 一律回落默认 5s 且构造成功
// （不支持禁用轮询语义）；正值原样生效（正值短间隔下的轮询行为见
// TestPollingAutoRefresh）。
func TestRefreshIntervalFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{"zero-falls-back-to-default", 0, defaultRefreshInterval},
		{"negative-falls-back-to-default", -time.Second, defaultRefreshInterval},
		{"positive-kept-as-is", 20 * time.Millisecond, 20 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := New(context.Background(), Config{
				Driver:          newDriver(map[string]string{"mode": "pro"}),
				RefreshInterval: c.interval,
			})
			if err != nil {
				t.Fatalf("New 失败: %v", err)
			}
			t.Cleanup(func() { _ = a.Close() })
			if a.refreshInterval != c.want {
				t.Fatalf("refreshInterval = %v, 期望 %v", a.refreshInterval, c.want)
			}
		})
	}
}

// TestOverlayOverridesBase overlay 覆盖 base：文件与 driver 同键（oss.bucket）
// 时，Get 出 driver 值。
func TestOverlayOverridesBase(t *testing.T) {
	t.Parallel()
	a := newAdapter(t, newDriver(map[string]string{"oss.bucket": "db-bucket"}), 0)
	if got := mustGetString(t, a, "oss.bucket"); got != "db-bucket" {
		t.Fatalf("Get(oss.bucket) = %q, 期望被 driver 覆盖为 db-bucket", got)
	}
}

// TestFileKeysPreserved 文件键保留：driver 不含 database 键时，文件键原样可读。
func TestFileKeysPreserved(t *testing.T) {
	t.Parallel()
	a := newAdapter(t, newDriver(map[string]string{"mode": "pro"}), 0)
	if got := mustGetString(t, a, "database.default.link"); got != fileDatabaseLink {
		t.Fatalf("Get(database.default.link) = %q, 期望文件值 %q", got, fileDatabaseLink)
	}
}

// TestReadZeroIO 读取零 IO：构造期恰好 2 次 Load（New 急加载 + 指向 testdata
// 后的重建），其后连续 Get / Data 共 200 次计数不增长——读路径零 Driver IO。
func TestReadZeroIO(t *testing.T) {
	t.Parallel()
	d := newDriver(map[string]string{"mode": "pro"})
	a := newAdapter(t, d, 0)
	if got := d.loadCount(); got != 2 {
		t.Fatalf("构造期 Load 计数 = %d, 期望 2（急加载 + SetPath 后重建）", got)
	}
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		if _, err := a.Get(ctx, "mode"); err != nil {
			t.Fatalf("Get 报错: %v", err)
		}
		if _, err := a.Data(ctx); err != nil {
			t.Fatalf("Data 报错: %v", err)
		}
	}
	if got := d.loadCount(); got != 2 {
		t.Fatalf("200 次读取后 Load 计数 = %d, 期望仍为 2（读取零 IO）", got)
	}
}

// TestLoadErrorKeepsOldSnapshot (nil, err) 语义：Load 报错按刷新失败处理——
// Refresh 返回错误，快照保持旧值（driver 键与文件键都在）。
func TestLoadErrorKeepsOldSnapshot(t *testing.T) {
	d := newDriver(map[string]string{"mode": "pro"})
	a := newAdapter(t, d, 0)
	d.setError(errors.New("db down"))
	if err := a.Refresh(context.Background()); err == nil {
		t.Fatal("Load 报错时 Refresh 应返回错误")
	}
	if got := mustGetString(t, a, "mode"); got != "pro" {
		t.Fatalf("刷新失败后 Get(mode) = %q, 期望旧值 pro", got)
	}
	if got := mustGetString(t, a, "database.default.link"); got != fileDatabaseLink {
		t.Fatalf("刷新失败后文件键应保留，Get = %q", got)
	}
}

// TestEmptyMapClearsOverlay (空map, nil) 语义：合法的全量空配置——overlay
// 清空、文件键仍在、无 error。与 (nil, err)「暂时读不到」严格区分。
func TestEmptyMapClearsOverlay(t *testing.T) {
	t.Parallel()
	d := newDriver(map[string]string{"mode": "pro", "oss.bucket": "db-bucket"})
	a := newAdapter(t, d, 0)
	d.setAll(map[string]string{})
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatalf("空 map 刷新不应报错: %v", err)
	}
	if v, err := a.Get(context.Background(), "mode"); v != nil || err != nil {
		t.Fatalf("overlay 应已清空，Get(mode) = (%v, %v)", v, err)
	}
	if got := mustGetString(t, a, "oss.bucket"); got != "file-bucket" {
		t.Fatalf("overlay 清空后 oss.bucket 应回落文件值 file-bucket，实际 %q", got)
	}
	if got := mustGetString(t, a, "database.default.link"); got != fileDatabaseLink {
		t.Fatalf("文件键应保留，Get = %q", got)
	}
}

// TestDriverMapCopiedOnLoad map 复制持有：shareMap 模式下组件拿到的是 driver
// 内部 map 本体，Load 返回后 driver 改自己的 map，快照保持 Load 时的值
// （组件侧经树构建复制，字符串标量不可变）。
func TestDriverMapCopiedOnLoad(t *testing.T) {
	t.Parallel()
	d := newDriver(map[string]string{"mode": "original", "oss.bucket": "b1"})
	d.shareMap = true
	a := newAdapter(t, d, 0)
	d.setValue("mode", "mutated")
	d.setValue("oss.bucket", "mutated-bucket")
	if got := mustGetString(t, a, "mode"); got != "original" {
		t.Fatalf("Get(mode) = %q, 期望 Load 返回时的值 original", got)
	}
	if got := mustGetString(t, a, "oss.bucket"); got != "b1" {
		t.Fatalf("Get(oss.bucket) = %q, 期望 Load 返回时的值 b1", got)
	}
}

// TestFlatKeyNesting 键变换嵌套化：扁平点分键落为嵌套树，单键与整组均可读，
// 无点分纯名落顶层。
func TestFlatKeyNesting(t *testing.T) {
	t.Parallel()
	a := newAdapter(t, newDriver(map[string]string{
		"oss.bucket":           "x",
		"session.ttl.platform": "604800",
		"mode":                 "pro",
	}), 0)
	if got := mustGetString(t, a, "oss.bucket"); got != "x" {
		t.Fatalf("Get(oss.bucket) = %q, 期望 x", got)
	}
	if got := mustGetString(t, a, "session.ttl.platform"); got != "604800" {
		t.Fatalf("Get(session.ttl.platform) = %q, 期望 604800", got)
	}
	if got := mustGetString(t, a, "mode"); got != "pro" {
		t.Fatalf("Get(mode) = %q, 期望 pro（无点分纯名落顶层）", got)
	}
	// Get("oss") 取整组子树。
	v, err := a.Get(context.Background(), "oss")
	if err != nil {
		t.Fatalf("Get(oss) 报错: %v", err)
	}
	grp, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("Get(oss) 期望整组 map[string]any，实际 %T（%v）", v, v)
	}
	if grp["bucket"] != "x" {
		t.Fatalf("Get(oss) 整组内容不符，实际 %v", grp)
	}
}

// TestConflictDeepWins 裁深弃浅："a.b" 与 "a.b.c" 冲突时保留深键——
// Get("a.b.c") 得值、Get("a.b") 是含 c 的子树；并直查 buildOverlayTree
// 树形确认浅键值被丢弃。
func TestConflictDeepWins(t *testing.T) {
	t.Parallel()
	a := newAdapter(t, newDriver(map[string]string{"a.b": "1", "a.b.c": "2"}), 0)
	if got := mustGetString(t, a, "a.b.c"); got != "2" {
		t.Fatalf("Get(a.b.c) = %q, 期望 2（深键保留）", got)
	}
	v, err := a.Get(context.Background(), "a.b")
	if err != nil {
		t.Fatalf("Get(a.b) 报错: %v", err)
	}
	sub, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("Get(a.b) 期望子树 map[string]any，实际 %T（%v）", v, v)
	}
	if sub["c"] != "2" {
		t.Fatalf("Get(a.b) 子树应含 c=2，实际 %v", sub)
	}

	// 树形直查：浅键 "1" 已被裁掉，a.b 原位转为子树。
	tree := buildOverlayTree(context.Background(), map[string]string{"a.b": "1", "a.b.c": "2"})
	ta, ok := tree["a"].(map[string]any)
	if !ok {
		t.Fatalf("树节点 a 期望子树，实际 %T", tree["a"])
	}
	tb, ok := ta["b"].(map[string]any)
	if !ok {
		t.Fatalf("a.b 期望子树（深键保留、浅键值被裁），实际 %T（%v）", ta["b"], ta["b"])
	}
	if len(tb) != 1 || tb["c"] != "2" {
		t.Fatalf("a.b 子树应仅含 c=2，实际 %v", tb)
	}
}

// TestBuildDeterminism 拼装确定性：同一份配置多次构建（多轮 Refresh + 独立
// 二次 New）结果一致——Go map 迭代顺序逐次随机，冲突裁剪与树形不随其漂移。
func TestBuildDeterminism(t *testing.T) {
	t.Parallel()
	data := map[string]string{
		"oss.bucket":           "b",
		"oss.endpoint":         "e",
		"session.ttl.platform": "1",
		"session.ttl.web":      "2",
		"mode":                 "pro",
		"feature.flag":         "on",
		"a.b":                  "shallow",
		"a.b.c":                "deep", // 冲突对同样必须确定性裁剪
	}
	ctx := context.Background()
	a1 := newAdapter(t, newDriver(data), 0)
	want, err := a1.Data(ctx)
	if err != nil {
		t.Fatalf("Data 报错: %v", err)
	}
	// 多轮刷新比对（每轮 transform 各自随机迭代 driver map）。
	for i := 0; i < 3; i++ {
		if err := a1.Refresh(ctx); err != nil {
			t.Fatalf("第 %d 轮 Refresh 失败: %v", i+1, err)
		}
		got, _ := a1.Data(ctx)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 轮构建结果漂移:\nwant %v\ngot  %v", i+1, want, got)
		}
	}
	// 独立二次构造比对。
	a2 := newAdapter(t, newDriver(data), 0)
	got2, err := a2.Data(ctx)
	if err != nil {
		t.Fatalf("Data 报错: %v", err)
	}
	if !reflect.DeepEqual(got2, want) {
		t.Fatalf("二次 New 构建结果与首轮不一致:\nwant %v\ngot  %v", want, got2)
	}
}

// TestNewFailOpenDualReturn 急加载失败双返回：driver 报错不阻断构造——
// New 返回非 nil adapter + 非 nil err，快照退化为纯文件内容、文件键可读。
func TestNewFailOpenDualReturn(t *testing.T) {
	d := newDriver(map[string]string{"mode": "pro"})
	d.setError(errors.New("db down"))
	a, err := assembleAdapter(t, d, true)
	if a == nil {
		t.Fatal("急加载失败也应返回可用 adapter（fail-open）")
	}
	if err == nil {
		t.Fatal("急加载失败应返回非 nil error")
	}
	if got := mustGetString(t, a, "database.default.link"); got != fileDatabaseLink {
		t.Fatalf("降级快照应含文件键，Get(database.default.link) = %q", got)
	}
	if !a.Available(context.Background()) {
		t.Fatal("纯文件快照应 Available")
	}
}

// TestAvailableSemantics Available 语义：有内容快照即 true（含纯文件快照）；
// 运行中刷新失败保旧快照不翻转；仅「构造失败且文件亦无内容」的空快照为 false。
func TestAvailableSemantics(t *testing.T) {
	ctx := context.Background()

	t.Run("normal-true", func(t *testing.T) {
		a := newAdapter(t, newDriver(map[string]string{"mode": "pro"}), 0)
		if !a.Available(ctx) {
			t.Fatal("合并快照应 Available")
		}
	})

	t.Run("refresh-failure-not-flipped", func(t *testing.T) {
		d := newDriver(map[string]string{"mode": "pro"})
		a := newAdapter(t, d, 0)
		d.setError(errors.New("db down"))
		if err := a.Refresh(ctx); err == nil {
			t.Fatal("期望刷新报错")
		}
		if !a.Available(ctx) {
			t.Fatal("刷新失败保旧快照，Available 不应翻转")
		}
	})

	t.Run("all-failed-empty-snapshot-false", func(t *testing.T) {
		// 无文件 base（不指向 testdata）+ driver 失败：快照为空。
		d := newDriver(map[string]string{"mode": "pro"})
		d.setError(errors.New("db down"))
		a, err := assembleAdapter(t, d, false)
		if err == nil {
			t.Fatal("全败构造应返回错误")
		}
		if a.Available(ctx) {
			t.Fatal("空快照不应 Available")
		}
		if v, gerr := a.Get(ctx, "mode"); v != nil || gerr != nil {
			t.Fatalf("空快照 Get 应 (nil, nil)，实际 (%v, %v)", v, gerr)
		}
	})
}

// TestGetMissNilNil Get miss 语义：快照无此 pattern 返回 (nil, nil)，
// 对齐 AdapterFile 行为，默认值由 gcfg 层 def 参数承接。
func TestGetMissNilNil(t *testing.T) {
	t.Parallel()
	a := newAdapter(t, newDriver(map[string]string{"mode": "pro"}), 0)
	v, err := a.Get(context.Background(), "not.exist")
	if v != nil || err != nil {
		t.Fatalf("miss 期望 (nil, nil)，实际 (%v, %v)", v, err)
	}
}

// TestDataDeepCopy Data 深拷贝：改写返回 map（含嵌套层与顶层）不影响内部快照。
func TestDataDeepCopy(t *testing.T) {
	t.Parallel()
	a := newAdapter(t, newDriver(map[string]string{"oss.bucket": "db-bucket", "mode": "pro"}), 0)
	ctx := context.Background()
	data, err := a.Data(ctx)
	if err != nil {
		t.Fatalf("Data 报错: %v", err)
	}
	oss := data["oss"].(map[string]any)
	oss["bucket"] = "hacked"
	oss["injected"] = "x"
	data["mode"] = "hacked"
	if got := mustGetString(t, a, "oss.bucket"); got != "db-bucket" {
		t.Fatalf("嵌套层改写不应影响快照，Get(oss.bucket) = %q", got)
	}
	if got := mustGetString(t, a, "mode"); got != "pro" {
		t.Fatalf("顶层改写不应影响快照，Get(mode) = %q", got)
	}
	fresh, _ := a.Data(ctx)
	if _, exists := fresh["oss"].(map[string]any)["injected"]; exists {
		t.Fatal("再次 Data 不应包含调用方注入键")
	}
}

// TestSetAdapterFrameworkKeysReadable SetAdapter 顺序契约：New 急加载「先 base
// 后 overlay」，返回即可读到框架配置键（database），无空快照窗口——driver 失败
// 亦然（fail-open）。以进程内独立 gcfg 实例模拟 g.Cfg().SetAdapter 的挂载形态。
func TestSetAdapterFrameworkKeysReadable(t *testing.T) {
	ctx := context.Background()
	d := newDriver(map[string]string{"mode": "pro"})
	d.setError(errors.New("db down"))
	a, err := assembleAdapter(t, d, true)
	if a == nil || err == nil {
		t.Fatalf("期望 (非 nil adapter, 非 nil err) 双返回，实际 (%v, %v)", a, err)
	}
	cfg := gcfg.NewWithAdapter(a)
	v, gerr := cfg.Get(ctx, "database.default.link")
	if gerr != nil {
		t.Fatalf("gcfg 层读取报错: %v", gerr)
	}
	if v == nil || v.String() != fileDatabaseLink {
		t.Fatalf("框架键应命中文件值 %q，实际 %v", fileDatabaseLink, v)
	}
	if !cfg.Available(ctx) {
		t.Fatal("挂载后 Available 应为 true")
	}
}

// ---- 刷新链（tasks 2.2）----

// TestPollingAutoRefresh 轮询自动刷新：50ms 间隔下改 driver 底层数据，
// 不调 Refresh，1.5s 内（数十个轮询周期）Get 出新值且 Load 计数增长。
func TestPollingAutoRefresh(t *testing.T) {
	ctx := context.Background()
	d := newDriver(map[string]string{"mode": "old"})
	a := newAdapter(t, d, 50*time.Millisecond)
	countBefore := d.loadCount()
	d.setValue("mode", "new")
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		if v, err := a.Get(ctx, "mode"); err == nil && v == "new" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("1.5s 内轮询未生效，Get(mode) 仍为旧值（Load 计数 %d）", d.loadCount())
		}
		time.Sleep(2 * time.Millisecond) // 有界轮询等待，断言依据是 Get 值本身
	}
	if d.loadCount() <= countBefore {
		t.Fatal("轮询应触发 Load 计数增长")
	}
}

// TestPollingFailureKeepsOldSnapshot 轮询失败保旧快照：driver 持续报错后
// Get 持续旧值且读取无 error 上抛；Load 仍按周期被重试（计数增长）；
// 错误由刷新入口经 Refresh 返回（ticker 路径仅记日志）。
func TestPollingFailureKeepsOldSnapshot(t *testing.T) {
	ctx := context.Background()
	d := newDriver(map[string]string{"mode": "pro"})
	a := newAdapter(t, d, 20*time.Millisecond)
	d.setError(errors.New("db down"))
	waitForLoadCount(t, d, 5, 2*time.Second) // 至少 3 个轮询周期重试
	if v, err := a.Get(ctx, "mode"); err != nil || v != "pro" {
		t.Fatalf("失败期间应持续读到旧值且无 error，实际 (%v, %v)", v, err)
	}
	if err := a.Refresh(ctx); err == nil {
		t.Fatal("刷新失败错误应经 Refresh 返回")
	}
	if v, err := a.Get(ctx, "mode"); err != nil || v != "pro" {
		t.Fatalf("显式刷新失败后仍应保旧快照，实际 (%v, %v)", v, err)
	}
}

// TestRefreshImmediateEffect Refresh 即时生效：落库模拟（改 driver 数据）后
// Refresh 返回即读到新值，不等轮询；driver 报错时 Refresh 返回非 nil error。
func TestRefreshImmediateEffect(t *testing.T) {
	ctx := context.Background()
	// 默认 5s 轮询窗口内不会自动刷新，变化只能来自显式 Refresh。
	d := newDriver(map[string]string{"mode": "v1"})
	a := newAdapter(t, d, 0)
	d.setValue("mode", "v2")
	if err := a.Refresh(ctx); err != nil {
		t.Fatalf("Refresh 失败: %v", err)
	}
	if got := mustGetString(t, a, "mode"); got != "v2" {
		t.Fatalf("Refresh 返回后应立即读到新值，实际 %q", got)
	}
	d.setError(errors.New("db down"))
	if err := a.Refresh(ctx); err == nil {
		t.Fatal("driver 报错时 Refresh 应返回非 nil error")
	}
	if got := mustGetString(t, a, "mode"); got != "v2" {
		t.Fatalf("失败刷新应保旧值，实际 %q", got)
	}
}

// TestSingleFlightMerge single-flight 合并：同一时刻 33 路并发 Refresh
// （32 goroutine + 主 goroutine）全部落在 Load 执行窗口（sleep 50ms）内，
// 经 single-flight 合并为恰好 1 次 Load，全部调用方共享同一结果。
// 轮询间隔取 1s，用例窗口内 ticker 不触发，计数断言不受轮询干扰。
func TestSingleFlightMerge(t *testing.T) {
	ctx := context.Background()
	d := newDriver(map[string]string{"mode": "pro"})
	d.sleep = 50 * time.Millisecond
	a := newAdapter(t, d, time.Second)
	before := d.loadCount()

	const goroutines = 32
	start := make(chan struct{})
	errs := make([]error, goroutines+1)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = a.Refresh(ctx)
		}(i)
	}
	close(start)
	errs[goroutines] = a.Refresh(ctx) // 主 goroutine 为第 33 个调用方
	wg.Wait()

	if got := d.loadCount() - before; got != 1 {
		t.Fatalf("33 路并发刷新应合并为恰好 1 次 Load，实际 %d 次", got)
	}
	for i, e := range errs {
		if e != nil {
			t.Fatalf("调用方 %d 应共享同一成功结果，实际报错: %v", i, e)
		}
	}
}

// TestWatcherNotifiedOnChange watcher 变化通知：内容变化的一轮刷新触发回调，
// 回调内 Get 取到新值；顺带验证 WatcherAdapter 注册表四方法路由。
func TestWatcherNotifiedOnChange(t *testing.T) {
	ctx := context.Background()
	d := newDriver(map[string]string{"mode": "v1"})
	a := newAdapter(t, d, 0)
	ch := make(chan string, 4)
	a.AddWatcher("w", func(c context.Context) {
		v, _ := a.Get(c, "mode")
		s, _ := v.(string)
		notify(ch, s)
	})
	if !a.IsWatching("w") {
		t.Fatal("AddWatcher 后 IsWatching 应为 true")
	}
	d.setValue("mode", "v2")
	if err := a.Refresh(ctx); err != nil {
		t.Fatalf("Refresh 失败: %v", err)
	}
	if got := waitNotify(t, ch, 2*time.Second); got != "v2" {
		t.Fatalf("回调内应取到新值 v2，实际 %q", got)
	}
	if names := a.GetWatcherNames(); len(names) != 1 || names[0] != "w" {
		t.Fatalf("GetWatcherNames = %v, 期望 [w]", names)
	}
	a.RemoveWatcher("w")
	if a.IsWatching("w") {
		t.Fatal("RemoveWatcher 后 IsWatching 应为 false")
	}
}

// TestWatcherSilentWhenUnchanged watcher 不变零通知：连续多个轮询周期内容
// 不变，回调零次（diff 过滤，防周期性空转通知）。
func TestWatcherSilentWhenUnchanged(t *testing.T) {
	d := newDriver(map[string]string{"mode": "pro"})
	a := newAdapter(t, d, 20*time.Millisecond)
	ch := make(chan string, 4)
	a.AddWatcher("w", func(c context.Context) {
		v, _ := a.Get(c, "mode")
		if s, ok := v.(string); ok {
			notify(ch, s)
		}
	})
	before := d.loadCount()
	waitForLoadCount(t, d, before+3, 2*time.Second) // 至少经历 3 个轮询周期
	select {
	case v := <-ch:
		t.Fatalf("内容未变不应通知，实际收到 %q", v)
	default:
	}
}

// TestWatcherPanicIsolated 回调 panic 不传染：panic 被 registry recover 隔离，
// 刷新流程正常返回、其他 watcher 正常收到通知、进程存活（断言执行到即证明）。
func TestWatcherPanicIsolated(t *testing.T) {
	ctx := context.Background()
	d := newDriver(map[string]string{"mode": "v1"})
	a := newAdapter(t, d, 0)
	panicCh := make(chan string, 4)
	okCh := make(chan string, 4)
	a.AddWatcher("panic-one", func(c context.Context) {
		notify(panicCh, "panic-one executed")
		panic("watcher 回调演练 panic")
	})
	a.AddWatcher("normal-one", func(c context.Context) {
		notify(okCh, "normal-one executed")
	})
	d.setValue("mode", "v2")
	if err := a.Refresh(ctx); err != nil {
		t.Fatalf("回调 panic 不应影响刷新流程，Refresh 报错: %v", err)
	}
	waitNotify(t, panicCh, 2*time.Second) // panicky 回调确实执行过
	waitNotify(t, okCh, 2*time.Second)    // 其他 watcher 不受影响
	if got := mustGetString(t, a, "mode"); got != "v2" {
		t.Fatalf("刷新应正常换绑，Get(mode) = %q", got)
	}
}

// ossWatchCfg gcfg.Loader 结构体重载目标。
type ossWatchCfg struct {
	Bucket string `json:"bucket"`
}

// TestLoaderWatchRoute gcfg.Loader 路由：Watch 经 WatcherAdapter 类型断言在
// adapter 注册表落地成功；配置变化时 loader 重载结构体生效。
func TestLoaderWatchRoute(t *testing.T) {
	ctx := context.Background()
	d := newDriver(map[string]string{"oss.bucket": "a"})
	a := newAdapter(t, d, 0)
	loader := gcfg.NewLoaderWithAdapter(a, "oss", &ossWatchCfg{})
	if err := loader.Watch(ctx, "oss-watch"); err != nil {
		t.Fatalf("对 WatcherAdapter 的 Watch 应成功: %v", err)
	}
	// Watch 经 adapter 注册表落地。
	found := false
	for _, name := range a.GetWatcherNames() {
		if name == "oss-watch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("loader.Watch 应注册进 adapter 注册表，实际 %v", a.GetWatcherNames())
	}
	// 初始加载：结构体填充 driver 值。
	if err := loader.Load(ctx); err != nil {
		t.Fatalf("初始 Load 失败: %v", err)
	}
	if got := loader.Get().Bucket; got != "a" {
		t.Fatalf("初始结构体 Bucket = %q, 期望 a", got)
	}
	// 配置变化 → watcher 通知 → loader 重载。
	reloaded := make(chan struct{})
	var once sync.Once
	loader.OnChange(func(updated ossWatchCfg) error {
		once.Do(func() { close(reloaded) })
		return nil
	})
	d.setValue("oss.bucket", "b")
	if err := a.Refresh(ctx); err != nil {
		t.Fatalf("Refresh 失败: %v", err)
	}
	select {
	case <-reloaded:
	case <-time.After(2 * time.Second):
		t.Fatal("配置变化后 loader 未重载")
	}
	if got := loader.Get().Bucket; got != "b" {
		t.Fatalf("重载后 Bucket = %q, 期望 b", got)
	}
}

// TestCloseIdempotent Close 幂等：连续两次无 panic 无错误；轮询停止（Load
// 计数不再增长，至多一次在飞刷新落地——生命周期检查只放弃换绑、不拦截计数）；
// Close 后 Get 仍可读最后快照。
func TestCloseIdempotent(t *testing.T) {
	ctx := context.Background()
	d := newDriver(map[string]string{"mode": "pro"})
	a := newAdapter(t, d, 20*time.Millisecond)
	waitForLoadCount(t, d, 4, 2*time.Second) // 轮询确实在工作
	if err := a.Close(); err != nil {
		t.Fatalf("首次 Close 报错: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("二次 Close 应幂等无错误: %v", err)
	}
	countAfterClose := d.loadCount()
	time.Sleep(120 * time.Millisecond) // 让 ≥5 个原轮询周期流逝
	if grew := d.loadCount() - countAfterClose; grew > 1 {
		t.Fatalf("Close 后 Load 计数不应再增长（至多一次在飞刷新落地），实际 +%d", grew)
	}
	if got, err := a.Get(ctx, "mode"); err != nil || got != "pro" {
		t.Fatalf("Close 后 Get 仍应可读最后快照，实际 (%v, %v)", got, err)
	}
}

package worker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 测试纪律：所有等待均用带超时上限的 select 同步，不用裸 sleep（对齐 worker_test.go）；
// 环境变量每个用例开头显式设置或清除，防用例间污染（并行会话可能设过 ENABLE_WORKERS）。
const enabledTestTimeout = 3 * time.Second

// enabledFakeWorker 假 Worker：Name 固定，Run 被调时向共享 started channel 发名（可断言启动顺序）。
type enabledFakeWorker struct {
	name    string
	started chan string
}

func (w *enabledFakeWorker) Name() string { return w.name }

// Run 记录被调后阻塞至 ctx 取消（模拟常驻 worker，随 manager 优雅退出）
func (w *enabledFakeWorker) Run(ctx context.Context) error {
	select {
	case w.started <- w.name:
	case <-ctx.Done():
		return nil
	}
	<-ctx.Done()
	return nil
}

// newEnabledFakeWorker 造一个假 worker，启动信号发到共享 started channel
func newEnabledFakeWorker(started chan string, name string) *enabledFakeWorker {
	return &enabledFakeWorker{name: name, started: started}
}

// collectEnabledStarts 从启动 channel 收 n 个名字（任意顺序），超时失败。
// 注意 manager.Start 逐 worker 起 goroutine，实际调度顺序无保证，故不做接收顺序断言；
// 过滤结果的注册顺序契约由 TestFilterWorkersKeepsRegisterOrder 纯函数断言承载。
func collectEnabledStarts(t *testing.T, started <-chan string, n int) map[string]bool {
	t.Helper()
	got := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		select {
		case name := <-started:
			got[name] = true
		case <-time.After(enabledTestTimeout):
			t.Fatalf("等待第 %d 个 worker 启动超时（已收到 %v）", i+1, got)
		}
	}
	return got
}

// waitEnabledDone 等 RunEnabled 返回并断言 err 为 nil
func waitEnabledDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunEnabled 返回 %v, 期望 nil", err)
		}
	case <-time.After(enabledTestTimeout):
		t.Fatal("等待 RunEnabled 返回超时")
	}
}

// assertEnabledZeroStart 零启动断言：RunEnabled 返回 nil 且无 worker 被调。
// 该路径同步返回（未 Register 任何 worker、未拉起 goroutine），返回后零调用是确定性的。
func assertEnabledZeroStart(t *testing.T, started chan string, workers ...Worker) {
	t.Helper()
	if err := RunEnabled(context.Background(), workers...); err != nil {
		t.Fatalf("RunEnabled 返回 %v, 期望 nil", err)
	}
	if got := len(started); got != 0 {
		t.Fatalf("期望零 worker 启动, 实际启动 %d 个", got)
	}
}

// TestParseEnabledList 纯函数：逗号拆分 → TrimSpace → 去空项 → 去重保序
func TestParseEnabledList(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"空串", "", nil},
		{"纯空白项", ", ,", nil},
		{"正常逗号分隔", "a,b", []string{"a", "b"}},
		{"带空格与空项与重复", "a, b,,a", []string{"a", "b"}},
		{"去重保序", "b,a,b", []string{"b", "a"}},
		{"项内空格 Trim", " order-sync , attachment-archive ", []string{"order-sync", "attachment-archive"}},
	}
	for _, c := range cases {
		got := parseEnabledList(c.raw)
		if len(got) != len(c.want) {
			t.Fatalf("%s: parseEnabledList(%q) = %v, 期望 %v", c.name, c.raw, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: parseEnabledList(%q) = %v, 期望 %v", c.name, c.raw, got, c.want)
			}
		}
	}
}

// TestRunEnabled_EnvUnset 未设 ENABLE_WORKERS（显式 Unsetenv，防并行会话/宿主环境残留）
// → 返回 nil、零 worker 启动（fail-closed）
func TestRunEnabled_EnvUnset(t *testing.T) {
	os.Unsetenv("ENABLE_WORKERS")

	started := make(chan string, 16)
	assertEnabledZeroStart(t, started,
		newEnabledFakeWorker(started, "a"),
		newEnabledFakeWorker(started, "b"),
	)
}

// TestRunEnabled_EnvEmpty 空串 "" → 同未设：返回 nil、零启动
func TestRunEnabled_EnvEmpty(t *testing.T) {
	t.Setenv("ENABLE_WORKERS", "")

	started := make(chan string, 16)
	assertEnabledZeroStart(t, started,
		newEnabledFakeWorker(started, "a"),
		newEnabledFakeWorker(started, "b"),
	)
}

// TestRunEnabled_EnvBlankItems 纯空白项 ", ," → 解析结果为空：返回 nil、零启动
func TestRunEnabled_EnvBlankItems(t *testing.T) {
	t.Setenv("ENABLE_WORKERS", ", ,")

	started := make(chan string, 16)
	assertEnabledZeroStart(t, started,
		newEnabledFakeWorker(started, "a"),
		newEnabledFakeWorker(started, "b"),
	)
}

// TestRunEnabled_UnknownNameRejected 白名单含未知名 → 拒绝启动，
// 错误信息含未知名与全部可用名（按注册顺序逗号连接）、零启动
func TestRunEnabled_UnknownNameRejected(t *testing.T) {
	t.Setenv("ENABLE_WORKERS", "a,typo-name")

	started := make(chan string, 16)
	err := RunEnabled(context.Background(),
		newEnabledFakeWorker(started, "a"),
		newEnabledFakeWorker(started, "b"),
	)
	if err == nil {
		t.Fatal("含未知名时期望 error, 实际 nil")
	}
	for _, want := range []string{"typo-name", "可用: a, b"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息 %q 未包含 %q", err.Error(), want)
		}
	}
	if got := len(started); got != 0 {
		t.Fatalf("拒绝启动后仍有 %d 个 worker 被调, 期望 0", got)
	}
}

// TestRunEnabled_CaseSensitive 精确匹配区分大小写：注册 "order-sync"、env "ORDER-SYNC" → 拒绝启动
func TestRunEnabled_CaseSensitive(t *testing.T) {
	t.Setenv("ENABLE_WORKERS", "ORDER-SYNC")

	started := make(chan string, 16)
	err := RunEnabled(context.Background(), newEnabledFakeWorker(started, "order-sync"))
	if err == nil {
		t.Fatal("大小写不匹配时期望 error, 实际 nil")
	}
	// 错误信息应同时含未知名（env 原样）与可用名（注册名），提示运维哪里不一致
	if !strings.Contains(err.Error(), "ORDER-SYNC") || !strings.Contains(err.Error(), "order-sync") {
		t.Fatalf("错误信息 %q 应同时含未知名与可用名", err.Error())
	}
	if got := len(started); got != 0 {
		t.Fatalf("拒绝启动后仍有 %d 个 worker 被调, 期望 0", got)
	}
}

// TestRunEnabled_DuplicateNameRejected 重名拒绝（全量，无论是否在白名单内）：
// 两个 Name()=="dup" 且 env 不含 dup，仍应先报重名错误（代码 bug 先于配置 bug 暴露）
func TestRunEnabled_DuplicateNameRejected(t *testing.T) {
	t.Setenv("ENABLE_WORKERS", "a")

	started := make(chan string, 16)
	err := RunEnabled(context.Background(),
		newEnabledFakeWorker(started, "dup"),
		newEnabledFakeWorker(started, "dup"),
		newEnabledFakeWorker(started, "a"),
	)
	if err == nil {
		t.Fatal("重名时期望 error, 实际 nil")
	}
	if want := "重复注册 worker 名: dup"; !strings.Contains(err.Error(), want) {
		t.Fatalf("错误信息 %q 未包含 %q", err.Error(), want)
	}
	if got := len(started); got != 0 {
		t.Fatalf("拒绝启动后仍有 %d 个 worker 被调, 期望 0", got)
	}
}

// TestFilterWorkersKeepsRegisterOrder 过滤结果按注册顺序保留（与 env 顺序无关）：
// 注册 a/b/c、白名单解析为 [b,a]，kept 应为 [a,b]
func TestFilterWorkersKeepsRegisterOrder(t *testing.T) {
	started := make(chan string, 16)
	workers := []Worker{
		newEnabledFakeWorker(started, "a"),
		newEnabledFakeWorker(started, "b"),
		newEnabledFakeWorker(started, "c"),
	}

	kept := filterWorkers([]string{"b", "a"}, workers)
	var got []string
	for _, w := range kept {
		got = append(got, w.Name())
	}
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("过滤结果 %v, 期望按注册顺序 [a b]", got)
	}
}

// TestRunEnabled_FilterKeepsRegisterOrder 正常过滤：注册 a/b/c、env "b,a"（反序）
// → 恰好启动 a、b，c 的 Run 不被调
func TestRunEnabled_FilterKeepsRegisterOrder(t *testing.T) {
	t.Setenv("ENABLE_WORKERS", "b,a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan string, 16)

	done := make(chan error, 1)
	go func() {
		done <- RunEnabled(ctx,
			newEnabledFakeWorker(started, "a"),
			newEnabledFakeWorker(started, "b"),
			newEnabledFakeWorker(started, "c"),
		)
	}()

	got := collectEnabledStarts(t, started, 2)
	if !got["a"] || !got["b"] {
		t.Fatalf("启动名单 %v, 期望恰好 a、b", got)
	}

	cancel()
	waitEnabledDone(t, done)

	// a/b 的信号已被消费，残留即白名单外的 c 被误启
	if rest := len(started); rest != 0 {
		t.Fatalf("白名单外的 worker c 被启动（started 残留 %d 个信号）", rest)
	}
}

// TestRunEnabled_DuplicateInEnvEquivalent "a, b,,a" ≡ "a,b"：env 内重复项去重后正常启动不报错
func TestRunEnabled_DuplicateInEnvEquivalent(t *testing.T) {
	t.Setenv("ENABLE_WORKERS", "a, b,,a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan string, 16)

	done := make(chan error, 1)
	go func() {
		done <- RunEnabled(ctx,
			newEnabledFakeWorker(started, "a"),
			newEnabledFakeWorker(started, "b"),
		)
	}()

	got := collectEnabledStarts(t, started, 2)
	if !got["a"] || !got["b"] {
		t.Fatalf("启动名单 %v, 期望恰好 a、b", got)
	}

	cancel()
	waitEnabledDone(t, done)
}

// TestRunEnabled_SmokePeriodic 全链路冒烟：两个毫秒级 PeriodicWorker（interval 20ms，
// handle 发 channel 信号），env 只含其一；Start 内 signal.NotifyContext 会包一层，
// 测试用 ctx cancel 触发退出。断言：被选中有执行信号、未选中零信号、RunEnabled 返回 nil。
func TestRunEnabled_SmokePeriodic(t *testing.T) {
	t.Setenv("ENABLE_WORKERS", "chosen")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const interval = 20 * time.Millisecond
	chosenSig := make(chan struct{}, 16)
	skippedSig := make(chan struct{}, 16)

	chosen := NewPeriodicWorker("chosen", interval, func(ctx context.Context) error {
		chosenSig <- struct{}{}
		return nil
	})
	skipped := NewPeriodicWorker("skipped", interval, func(ctx context.Context) error {
		skippedSig <- struct{}{}
		return nil
	})

	done := make(chan error, 1)
	go func() {
		done <- RunEnabled(ctx, chosen, skipped)
	}()

	// 被选中的 worker 至少执行一次
	select {
	case <-chosenSig:
	case <-time.After(enabledTestTimeout):
		t.Fatal("等待被选中 worker 执行超时")
	}

	// 给未选中者留 5 个 tick 周期（若被误启动必已留痕），断言零信号
	select {
	case <-time.After(5 * interval):
	case <-ctx.Done():
		t.Fatal("ctx 意外取消")
	}
	if got := len(skippedSig); got != 0 {
		t.Fatalf("白名单外 worker 执行了 %d 次, 期望 0", got)
	}

	cancel()
	waitEnabledDone(t, done)
}

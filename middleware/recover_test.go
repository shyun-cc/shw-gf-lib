// Package middleware 测试：RecoverConnFunc TCP 入口 panic 守卫的本地服务端到端取证。
//
// 覆盖点（对应 change logging-json tasks 2.1/2.2）：
//   - 场景 1 panic 不崩：连接 handler panic 被 recover，日志含 "[TCP PANIC] conn=t"
//     与堆栈特征（"goroutine"），随后新连接正常收发（服务仍可用）
//   - 场景 2 每连接独立 traceId：两条不同连接的 panic 日志行 traceId 非空且互不相同
//   - 场景 3 正常路径零额外：handler 正常收发，日志无 "[TCP PANIC]"
//
// 日志捕获：g.Log().SetWriter(并发安全缓冲)（参照 trace_test.go 先例，从日志行
// 提取 {traceId}——glog 以花括号包裹输出），测试尾恢复 stdout。
// 服务端到端：真起 gtcp server（127.0.0.1:0 随机端口，GetListenedPort 轮询就绪，
// gtcp 原生 FreePortAddress 形态，无 listen-close-重占竞态）。
package middleware_test

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	mw "github.com/shyun-cc/shw-gf-lib/middleware"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gtcp"
	"github.com/gogf/gf/v2/os/glog"
)

// syncBuffer 并发安全的日志缓冲：glog 从多个连接 handler goroutine 写入，
// 测试 goroutine 同时轮询读取（裸 bytes.Buffer 有 data race）。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs 把 g.Log() 输出重定向到并发安全缓冲，测试尾恢复 stdout。
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	var out syncBuffer
	g.Log().SetLevel(glog.LEVEL_ALL)
	g.Log().SetWriter(&out)
	t.Cleanup(func() { g.Log().SetWriter(os.Stdout) })
	return &out
}

// startGuardedTCPServer 起一个被 RecoverConnFunc 包装的本地 gtcp server，
// 返回就绪后的监听地址。handler 协议：收到行 "panic" 触发 panic，其余回 "pong"。
func startGuardedTCPServer(t *testing.T) string {
	t.Helper()
	s := gtcp.NewServer("127.0.0.1:0", mw.RecoverConnFunc("t", func(conn *gtcp.Conn) {
		line, err := conn.RecvLine()
		if err != nil {
			return
		}
		if string(line) == "panic" {
			panic("boom")
		}
		_ = conn.Send([]byte("pong"))
	}))
	go func() { _ = s.Run() }() // Run 阻塞（Accept 循环），Close 后返回退出
	t.Cleanup(func() { _ = s.Close() })

	deadline := time.Now().Add(2 * time.Second)
	for s.GetListenedPort() <= 0 {
		if time.Now().After(deadline) {
			t.Fatal("gtcp server 启动超时（2s 内未进入监听）")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Sprintf("127.0.0.1:%d", s.GetListenedPort())
}

// waitPanicLog 轮询等待日志中出现 wantCount 处 panic 标记（glog 写入与客户端
// 返回存在时序差，不能发完立即断言）。
func waitPanicLog(t *testing.T, out *syncBuffer, wantCount int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if strings.Count(out.String(), "[TCP PANIC] conn=t") >= wantCount {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 %d 条 panic 日志超时, 当前日志:\n%s", wantCount, out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// panicTraceIDs 提取全部 panic 日志行（"[TCP PANIC]" 在消息首行，堆栈行不含）
// 的 traceId（复用 trace_test.go 的 traceIDRe，glog 行内 {32位hex} 形式）。
func panicTraceIDs(logs string) []string {
	var ids []string
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, "[TCP PANIC]") {
			continue
		}
		if m := traceIDRe.FindStringSubmatch(line); m != nil {
			ids = append(ids, m[1])
		}
	}
	return ids
}

// TestRecoverConnFunc_PanicNotCrashServer 场景 1：handler panic 被 recover，
// 日志含契约文案与堆栈特征，服务对新连接仍可用。
func TestRecoverConnFunc_PanicNotCrashServer(t *testing.T) {
	out := captureLogs(t)
	addr := startGuardedTCPServer(t)

	// 触发 panic 的连接：发送后客户端即关（TCP 保证数据先于 FIN 到达）
	if err := gtcp.Send(addr, []byte("panic\n")); err != nil {
		t.Fatalf("触发 panic 的连接发送失败: %v", err)
	}
	waitPanicLog(t, out, 1)

	logs := out.String()
	if !strings.Contains(logs, "[TCP PANIC] conn=t") {
		t.Fatalf("panic 日志缺少契约文案 \"[TCP PANIC] conn=t\", 日志:\n%s", logs)
	}
	if !strings.Contains(logs, "boom") {
		t.Fatalf("panic 日志缺少 recover 值 boom, 日志:\n%s", logs)
	}
	if !strings.Contains(logs, "goroutine") {
		t.Fatalf("panic 日志缺少堆栈特征 goroutine, 日志:\n%s", logs)
	}

	// 进程未崩的取证：再开一条新连接正常收发
	resp, err := gtcp.SendRecv(addr, []byte("ping\n"), len("pong"))
	if err != nil {
		t.Fatalf("panic 后新连接收发失败（服务不可用）: %v", err)
	}
	if string(resp) != "pong" {
		t.Fatalf("panic 后新连接响应异常: %q", resp)
	}
}

// TestRecoverConnFunc_PerConnTraceId 场景 2：两条不同连接的 panic 日志行
// traceId 非空且互不相同（每连接入口独立 Ensure）。
func TestRecoverConnFunc_PerConnTraceId(t *testing.T) {
	out := captureLogs(t)
	addr := startGuardedTCPServer(t)

	for i := 0; i < 2; i++ {
		if err := gtcp.Send(addr, []byte("panic\n")); err != nil {
			t.Fatalf("第 %d 条 panic 连接发送失败: %v", i+1, err)
		}
	}
	waitPanicLog(t, out, 2)

	ids := panicTraceIDs(out.String())
	if len(ids) != 2 {
		t.Fatalf("应提取到 2 条 panic 日志的 traceId, 实得 %d 条, 日志:\n%s", len(ids), out.String())
	}
	for i, id := range ids {
		if id == "" {
			t.Fatalf("第 %d 条 panic 日志 traceId 为空, 日志:\n%s", i+1, out.String())
		}
	}
	if ids[0] == ids[1] {
		t.Fatalf("两条不同连接的 panic 日志 traceId 相同: %s", ids[0])
	}
}

// TestRecoverConnFunc_NormalPathSilent 场景 3：正常收发路径零额外日志（无 panic 告警）。
func TestRecoverConnFunc_NormalPathSilent(t *testing.T) {
	out := captureLogs(t)
	addr := startGuardedTCPServer(t)

	resp, err := gtcp.SendRecv(addr, []byte("hello\n"), len("pong"))
	if err != nil {
		t.Fatalf("正常连接收发失败: %v", err)
	}
	if string(resp) != "pong" {
		t.Fatalf("正常连接响应异常: %q", resp)
	}
	if logs := out.String(); strings.Contains(logs, "[TCP PANIC]") {
		t.Fatalf("正常路径不应出现 panic 日志, 日志:\n%s", logs)
	}
}

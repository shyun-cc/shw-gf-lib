// Package middleware 测试：Trace 中间件的 traceId 同源贯穿取证。
//
// 覆盖点（对应 specs/trace-context）：
//   - 同一请求：响应 JSON 的 traceId 与请求期间 glog 日志行的 traceId 相等（同源）
//   - 两个不同请求的响应 traceId 互不相同
//
// 日志捕获：g.Log().SetWriter(bytes.Buffer)，从日志行提取 {traceId}（glog 以花括号包裹输出）
// 与响应体 traceId 比对；测试结束恢复 stdout。
package middleware_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	mw "github.com/shyun-cc/shw-gf-lib/middleware"
	"github.com/shyun-cc/shw-gf-lib/response"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/glog"
	"github.com/gogf/gf/v2/util/guid"
)

// traceIDRe 匹配 glog 行内 {32位hex} 形式的 traceId
var traceIDRe = regexp.MustCompile(`\{([0-9a-f]{32})\}`)

// traceResp 响应体结构（只关心 traceId）
type traceResp struct {
	TraceID string `json:"traceId"`
}

func fetchTrace(t *testing.T, path string) string {
	t.Helper()
	resp, err := http.Get(path)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	var body traceResp
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v, 原始: %s", err, raw)
	}
	if body.TraceID == "" {
		t.Fatalf("响应 traceId 为空, 原始: %s", raw)
	}
	return body.TraceID
}

func TestTraceMiddleware_ResponseAndLogSameTraceId(t *testing.T) {
	var buf bytes.Buffer
	g.Log().SetLevel(glog.LEVEL_ALL)
	g.Log().SetWriter(&buf)
	t.Cleanup(func() { g.Log().SetWriter(os.Stdout) })

	s := g.Server(guid.S())
	s.SetAddr("127.0.0.1:0")
	s.SetDumpRouterMap(false)
	s.SetLogStdout(false)
	s.SetAccessLogEnabled(false)

	s.BindMiddleware("/traced", mw.Trace)
	s.BindHandler("/traced", func(r *ghttp.Request) {
		g.Log().Infof(r.Context(), "handling traced request")
		response.Success(r, g.Map{"ok": true})
	})

	if err := s.Start(); err != nil {
		t.Fatalf("启动 server 失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown() })
	for s.GetListenedPort() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())

	// 同一请求：响应 traceId 与日志 traceId 同源
	respTID := fetchTrace(t, base+"/traced")

	matches := traceIDRe.FindAllStringSubmatch(buf.String(), -1)
	if len(matches) == 0 {
		t.Fatalf("日志输出未包含 traceId, 原始日志:\n%s", buf.String())
	}
	found := false
	for _, m := range matches {
		if m[1] == respTID {
			found = true
		}
	}
	if !found {
		t.Fatalf("响应 traceId %s 未出现在日志中, 日志:\n%s", respTID, buf.String())
	}

	// 不同请求：traceId 互不相同
	buf.Reset()
	second := fetchTrace(t, base+"/traced")
	if second == respTID {
		t.Fatalf("两个不同请求的 traceId 相同: %s", respTID)
	}
	if second == "" {
		t.Fatal("第二个请求 traceId 为空")
	}
}

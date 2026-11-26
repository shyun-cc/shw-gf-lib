// middleware 包测试（外部测试包，通过真实 ghttp 本地随机端口 server 验证中间件行为）。
//
// 结构（对应 specs/auth-context-passthrough）：
//   - Auth：无 Authorization 头 401 / 坏 token 401 / 有效 token 注入 session data
//     全量 map（键名保持 session 存储形态，不改写为驼峰；机制键一并透传）
//   - Response：规范路由成功封装 / Application 异常 HTTP 200 + code / 旧式手写响应跳过
//
// session 走真 SessionService + miniredis（internal/testredis）；
// 每个用例独立 server（随机命名 + 127.0.0.1:0 随机端口），Start 后 t.Cleanup 注册 Shutdown。
package middleware_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
	mw "github.com/shyun-cc/shw-gf-lib/middleware"
	"github.com/shyun-cc/shw-gf-lib/response"
	"github.com/shyun-cc/shw-gf-lib/session"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
)

// ---------------------------------------------------------------- 测试基建 --

// newTestServer 创建每用例独立的本地 server：随机命名避免 server 注册表冲突，
// 127.0.0.1:0 由内核分配随机端口；关闭路由表打印与访问日志减少输出噪音。
func newTestServer() *ghttp.Server {
	s := g.Server(guid.S())
	s.SetAddr("127.0.0.1:0")
	s.SetDumpRouterMap(false)
	s.SetLogStdout(false)
	s.SetAccessLogEnabled(false)
	return s
}

// startServer 启动 server 并注册清理（Shutdown），返回基础 URL。
// Start 后轮询等待端口就绪（不 sleep 硬等，最多 2 秒）。
func startServer(t *testing.T, s *ghttp.Server) string {
	t.Helper()
	if err := s.Start(); err != nil {
		t.Fatalf("启动 server 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Shutdown()
	})
	deadline := time.Now().Add(2 * time.Second)
	for s.GetListenedPort() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.GetListenedPort() == 0 {
		t.Fatal("等待 server 端口就绪超时")
	}
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

// httpResult 一次请求的结果：状态码 + 原始响应体。
type httpResult struct {
	Status int
	Body   string
}

// doRequest 发起 HTTP 请求，返回状态码与响应体（标准库客户端，5 秒超时）。
func doRequest(t *testing.T, method, url string, headers map[string]string) httpResult {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	return httpResult{Status: resp.StatusCode, Body: string(b)}
}

// parseJSON 断言响应体可完整解析为单个 JSON 对象并返回 map。
// 「单个」很关键：若中间件重复写入会出现 `{...}{...}` 拼接体，此处解析直接失败。
func parseJSON(t *testing.T, res httpResult) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(res.Body), &m); err != nil {
		t.Fatalf("响应体应是单个合法 JSON 对象，实际: %q，解析错误: %v", res.Body, err)
	}
	return m
}

// jsonField 断言顶层字段存在且按期望类型取出（数字统一为 float64，字符串为 string）。
func jsonNumber(t *testing.T, m map[string]interface{}, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("响应字段 %s 应为数字，实际 %T(%v)", key, m[key], m[key])
	}
	return v
}

func jsonString(t *testing.T, m map[string]interface{}, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("响应字段 %s 应为字符串，实际 %T(%v)", key, m[key], m[key])
	}
	return v
}

// jsonData 断言 data 字段为 JSON 对象并返回。
func jsonData(t *testing.T, m map[string]interface{}) map[string]interface{} {
	t.Helper()
	v, ok := m["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("响应 data 应为对象，实际 %T(%v)", m["data"], m["data"])
	}
	return v
}

// superAdminData / normalAdminData 构造测试会话数据（int 型布尔语义，
// 避免不同 redis 序列化实现下 bool 形态不确定的问题）。
func superAdminData() map[string]interface{} {
	return map[string]interface{}{
		"admin_id":       "42",
		"name":           "张三",
		"username":       "zhang",
		"role":           2,
		"is_super_admin": 1,
	}
}

func normalAdminData() map[string]interface{} {
	return map[string]interface{}{
		"admin_id":       "43",
		"name":           "李四",
		"username":       "li",
		"role":           1,
		"is_super_admin": 0,
	}
}

// createSession 用真 SessionService 写入 miniredis，返回 token（会话 UUID）。
func createSession(t *testing.T, sess *session.SessionService, data map[string]interface{}) string {
	t.Helper()
	sd, err := sess.Create(context.Background(), data["admin_id"].(string), data)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if sd == nil || sd.UUID == "" {
		t.Fatalf("创建会话应返回非空 UUID，实际: %+v", sd)
	}
	return sd.UUID
}

// ---------------------------------------------------------------- Auth --

// TestAuthNoAuthorizationHeader 无 Authorization 头：HTTP 401 + 统一错误体，
// 业务 handler 不执行（body 不含 pong）。
func TestAuthNoAuthorizationHeader(t *testing.T) {
	testredis.Start(t)
	sess := session.New("test", 86400, "admin_id")

	s := newTestServer()
	s.BindMiddleware("/*", func(r *ghttp.Request) { mw.Auth(r, sess) })
	s.BindHandler("/ping", func(r *ghttp.Request) { r.Response.WriteString("pong") })
	base := startServer(t, s)

	res := doRequest(t, http.MethodGet, base+"/ping", nil)
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("状态码应为 401，实际 %d，body: %s", res.Status, res.Body)
	}
	m := parseJSON(t, res)
	if jsonString(t, m, "message") != "未登录，请先登录" {
		t.Errorf("message 应为未登录提示，实际 %q", m["message"])
	}
	if jsonNumber(t, m, "code") != 401 {
		t.Errorf("code 应为 401，实际 %v", m["code"])
	}
	if jsonString(t, m, "traceId") == "" {
		t.Error("traceId 不应为空")
	}
	if d := jsonData(t, m); len(d) != 0 {
		t.Errorf("data 应为空对象，实际 %v", d)
	}
	if strings.Contains(res.Body, "pong") {
		t.Error("未认证时业务 handler 不应执行")
	}
}

// TestAuthBadToken 两类坏 token 均 401：
//   - 无 "Bearer " 前缀（如 "Token xxx"）→ 未登录提示
//   - 格式正常但会话不存在 → 过期提示
func TestAuthBadToken(t *testing.T) {
	testredis.Start(t)
	sess := session.New("test", 86400, "admin_id")

	s := newTestServer()
	s.BindMiddleware("/*", func(r *ghttp.Request) { mw.Auth(r, sess) })
	s.BindHandler("/ping", func(r *ghttp.Request) { r.Response.WriteString("pong") })
	base := startServer(t, s)

	// 无 Bearer 前缀：TrimPrefix 后 token == header，判为未登录
	res := doRequest(t, http.MethodGet, base+"/ping", map[string]string{
		"Authorization": "Token abcdef",
	})
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("无 Bearer 前缀应 401，实际 %d，body: %s", res.Status, res.Body)
	}
	if m := parseJSON(t, res); jsonString(t, m, "message") != "未登录，请先登录" {
		t.Errorf("无 Bearer 前缀的 message 应为未登录提示，实际 %q", m["message"])
	}

	// Bearer 格式正常但 miniredis 中无此会话：session 不存在 → 过期提示
	res2 := doRequest(t, http.MethodGet, base+"/ping", map[string]string{
		"Authorization": "Bearer 00000000-0000-0000-0000-000000000000",
	})
	if res2.Status != http.StatusUnauthorized {
		t.Fatalf("不存在的会话应 401，实际 %d，body: %s", res2.Status, res2.Body)
	}
	if m := parseJSON(t, res2); jsonString(t, m, "message") != "登录状态已过期，请重新登录" {
		t.Errorf("不存在的会话 message 应为过期提示，实际 %q", m["message"])
	}
}

// TestAuthValidTokenInjectsUser 有效 token：放行 200，且 handler 内通过
// GetCtxVar(ContextKeyUser) 取到的 map 即 session data 全量——Create 会话时
// 写入什么键就原样透传什么键（键名保持 session 存储形态），role 也只来自
// session data（库不再提供 sess.Role 注入路径）；uuid/last_active_at 等
// session 机制键一并透传（多传无害，挑选才是越位）。
// 两组会话数据（超管/非超管）各自断言键值对应。
func TestAuthValidTokenInjectsUser(t *testing.T) {
	testredis.Start(t)
	sess := session.New("test", 86400, "admin_id")
	superToken := createSession(t, sess, superAdminData())
	normalToken := createSession(t, sess, normalAdminData())

	s := newTestServer()
	s.BindMiddleware("/*", func(r *ghttp.Request) { mw.Auth(r, sess) })
	// 把 ctx 中的用户 map 原样写入响应，供断言注入内容
	s.BindHandler("/whoami", func(r *ghttp.Request) {
		user := r.GetCtxVar(mw.ContextKeyUser)
		if user == nil || !user.IsMap() {
			r.Response.WriteJson(g.Map{"error": "no user in ctx"})
			return
		}
		r.Response.WriteJson(user.Map())
	})
	base := startServer(t, s)

	cases := map[string]struct {
		token string
		want  map[string]string
	}{
		"super_admin": {
			token: superToken,
			want: map[string]string{
				"admin_id":       "42",
				"name":           "张三",
				"username":       "zhang",
				"role":           "2",
				"is_super_admin": "1",
			},
		},
		"normal_admin": {
			token: normalToken,
			want: map[string]string{
				"admin_id":       "43",
				"name":           "李四",
				"username":       "li",
				"role":           "1",
				"is_super_admin": "0",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := doRequest(t, http.MethodGet, base+"/whoami", map[string]string{
				"Authorization": "Bearer " + tc.token,
			})
			if res.Status != http.StatusOK {
				t.Fatalf("有效 token 应 200，实际 %d，body: %s", res.Status, res.Body)
			}
			m := parseJSON(t, res)
			// 会话数据经 Redis Hash 往返后均为字符串形态
			for field, want := range tc.want {
				if got := jsonString(t, m, field); got != want {
					t.Errorf("ctx 用户字段 %s = %q, 期望 %q", field, got, want)
				}
			}
			// 反向断言：ctx user 不应存在 isSuperAdmin 改写键
			// （守护"库不改名"契约——session 键 is_super_admin 不得被改写为驼峰）
			if _, ok := m["isSuperAdmin"]; ok {
				t.Errorf("ctx 用户 map 不应存在改写键 isSuperAdmin，实际值: %v", m["isSuperAdmin"])
			}
			// session 机制键也全量透传（uuid 即会话 token）
			if got := jsonString(t, m, "uuid"); got != tc.token {
				t.Errorf("ctx 用户 uuid 应为会话 UUID（机制键一并透传），实际 %q", got)
			}
			if got := jsonString(t, m, "last_active_at"); got == "" {
				t.Error("ctx 用户应含 last_active_at 机制键，实际为空")
			}
		})
	}
}

// --------------------------------------------------------------- Response --

// 规范路由测试用请求/响应结构（path 由 group.GET pattern 指定，无需 g.Meta）
type detailReq struct{}
type detailRes struct {
	Id    int    `json:"id"`
	Title string `json:"title"`
}
type payReq struct{}
type payRes struct{}

// TestResponseMiddlewareSuccess 规范路由成功：handler 返回 (res, nil)，
// Response 中间件封装为 {traceId, code:0, message:"success", data:res}，HTTP 200。
func TestResponseMiddlewareSuccess(t *testing.T) {
	testredis.Start(t)

	s := newTestServer()
	group := s.Group("/")
	group.Middleware(mw.Response)
	group.GET("/detail", func(ctx context.Context, req *detailReq) (res *detailRes, err error) {
		return &detailRes{Id: 7, Title: "订单"}, nil
	})
	base := startServer(t, s)

	res := doRequest(t, http.MethodGet, base+"/detail", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("成功响应应 200，实际 %d，body: %s", res.Status, res.Body)
	}
	m := parseJSON(t, res)
	if jsonNumber(t, m, "code") != 0 {
		t.Errorf("code 应为 0，实际 %v", m["code"])
	}
	if jsonString(t, m, "message") != "success" {
		t.Errorf("message 应为 success，实际 %q", m["message"])
	}
	if jsonString(t, m, "traceId") == "" {
		t.Error("traceId 不应为空")
	}
	data := jsonData(t, m)
	if jsonNumber(t, data, "id") != 7 {
		t.Errorf("data.id 应为 7，实际 %v", data["id"])
	}
	if jsonString(t, data, "title") != "订单" {
		t.Errorf("data.title 应为 订单，实际 %q", data["title"])
	}
}

// TestResponseMiddlewareAppError 规范路由返回 Application 层 AppError：
// HTTP 200（前端通过 code 区分）+ 统一体携带业务 code/message，data 为空对象。
func TestResponseMiddlewareAppError(t *testing.T) {
	testredis.Start(t)

	s := newTestServer()
	group := s.Group("/")
	group.Middleware(mw.Response)
	group.GET("/pay", func(ctx context.Context, req *payReq) (res *payRes, err error) {
		return nil, exception.NewApplication(40001, "余额不足")
	})
	base := startServer(t, s)

	res := doRequest(t, http.MethodGet, base+"/pay", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("Application 异常应 HTTP 200，实际 %d，body: %s", res.Status, res.Body)
	}
	m := parseJSON(t, res)
	if jsonNumber(t, m, "code") != 40001 {
		t.Errorf("code 应为 40001，实际 %v", m["code"])
	}
	if jsonString(t, m, "message") != "余额不足" {
		t.Errorf("message 应为 余额不足，实际 %q", m["message"])
	}
	if d := jsonData(t, m); len(d) != 0 {
		t.Errorf("data 应为空对象，实际 %v", d)
	}
}

// TestResponseMiddlewareLegacySkip 旧式函数路由兼容：handler 手写
// response.Success（WriteJsonExit）后响应缓冲非空，Response 中间件跳过不重复写。
// 断言依据：响应体仍是单个合法 JSON（若中间件未跳过会追加第二份导致解析失败）。
func TestResponseMiddlewareLegacySkip(t *testing.T) {
	testredis.Start(t)

	s := newTestServer()
	group := s.Group("/")
	group.Middleware(mw.Response)
	group.GET("/legacy", func(r *ghttp.Request) {
		response.Success(r, g.Map{"way": "old"})
	})
	base := startServer(t, s)

	res := doRequest(t, http.MethodGet, base+"/legacy", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("旧式手写响应应 200，实际 %d，body: %s", res.Status, res.Body)
	}
	// 能解析为单个 JSON 即证明中间件没有重复写入
	m := parseJSON(t, res)
	if jsonNumber(t, m, "code") != 0 {
		t.Errorf("code 应为 0，实际 %v", m["code"])
	}
	if jsonString(t, m, "message") != "success" {
		t.Errorf("message 应为 success，实际 %q", m["message"])
	}
	if jsonString(t, jsonData(t, m), "way") != "old" {
		t.Errorf("data.way 应为 old，实际 %v", m["data"])
	}
}

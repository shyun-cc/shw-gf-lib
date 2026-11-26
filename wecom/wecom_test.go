package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"

	"github.com/gogf/gf/v2/frame/g"
)

// ---- client + token ----

// wecomSrv 可编程 server：按路径计数并返回夹具；gettoken 慢响应制造并发窗口
type wecomSrv struct {
	replies   map[string]string
	fetchN    int32 // gettoken 次数
	reqN      int32 // 业务路径请求次数
	slowToken bool
}

func (s *wecomSrv) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/cgi-bin/gettoken" {
			n := atomic.AddInt32(&s.fetchN, 1)
			if s.slowToken {
				time.Sleep(30 * time.Millisecond)
			}
			_ = n
			_, _ = w.Write([]byte(s.replies["token"]))
			return
		}
		atomic.AddInt32(&s.reqN, 1)
		// 42001 熔断一次后成功（按请求序）
		if strings.Contains(string(body), "TRIGGER_42001") || r.URL.Query().Get("trigger") == "1" {
			if s.reqN == 1 {
				_, _ = w.Write([]byte(`{"errcode":42001,"errmsg":"access_token expired"}`))
				return
			}
		}
		reply, ok := s.replies[r.URL.Path]
		if !ok {
			reply = `{"errcode":0,"errmsg":"ok"}`
		}
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, srvURL string) *Client {
	t.Helper()
	testredis.Start(t)
	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.BaseURL = srvURL
	// token fetch 走 defaultQyAPI 域名——注入点：覆写 fetchAndCache 的目标由测试 server 承担。
	// 这里直接把 token 写进缓存避免真实网络。逐用例控制。
	return c
}

// seedToken 预置 token 缓存（绕过 gettoken 的真实域名）
func seedToken(t *testing.T, token string) {
	t.Helper()
	_, err := g.Redis().Set(context.Background(), "wecom:corp1:100:access_token", token)
	if err != nil {
		t.Fatal(err)
	}
}

// TestClient_TokenExpiredRefreshOnce errcode 42001：恰一次强制刷新重试后成功
func TestClient_TokenExpiredRefreshOnce(t *testing.T) {
	testredis.Start(t)
	srv := &wecomSrv{
		replies: map[string]string{
			"token":             `{"errcode":0,"access_token":"TK_NEW"}`,
			"/cgi-bin/user/get": `{"errcode":0,"errmsg":"ok","userid":"u1"}`,
		},
	}
	base := srv.server(t).URL

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(base)
	// 首次 token 经真实 gettoken 不可达——直接预置「过期 token」+ 让 server 首请回 42001：
	seedToken(t, "TK_OLD")

	resp, err := c.Get(context.Background(), "/cgi-bin/user/get", map[string]string{"trigger": "1"})
	if err != nil {
		t.Fatalf("42001 刷新重试后应成功: %v", err)
	}
	if resp["userid"] != "u1" {
		t.Fatalf("重试响应不符: %v", resp)
	}
	if srv.reqN != 2 {
		t.Fatalf("业务请求应恰 2 次（首失败+重试），实际 %d", srv.reqN)
	}
}

// TestClient_OtherErrcodeNoRetry 其他 errcode 不重试直接报错
func TestClient_OtherErrcodeNoRetry(t *testing.T) {
	testredis.Start(t)
	srv := &wecomSrv{
		replies: map[string]string{
			"/cgi-bin/user/get": `{"errcode":60011,"errmsg":"no privilege"}`,
		},
	}
	base := srv.server(t).URL
	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(base)
	seedToken(t, "TK")

	if _, err := c.Get(context.Background(), "/cgi-bin/user/get", nil); err == nil {
		t.Fatal("60011 应报错")
	}
	if srv.reqN != 1 {
		t.Fatalf("非 token 错误不应重试，请求数 %d", srv.reqN)
	}
}

// TestToken_CacheHit 缓存命中不发 HTTP（server 计数取证）
func TestToken_CacheHit(t *testing.T) {
	testredis.Start(t)
	srv := &wecomSrv{replies: map[string]string{"token": `{"errcode":0,"access_token":"TK_FETCHED"}`}}
	base := srv.server(t).URL
	seedToken(t, "TK_CACHED")
	m := NewTokenManager(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	m.SetBaseURL(base, nil)
	got, err := m.Token(context.Background(), false)
	if err != nil || got != "TK_CACHED" {
		t.Fatalf("缓存命中: %v %s", err, got)
	}
	if srv.fetchN != 0 {
		t.Fatalf("缓存命中不应发 gettoken，实际 %d 次", srv.fetchN)
	}
}

// TestToken_ConcurrentSingleFetch 缓存 miss 时 N 并发恰一次 gettoken
// （redislock 互斥 + double-check；慢响应制造并发窗口，其余请求自旋等待后取缓存同值）
func TestToken_ConcurrentSingleFetch(t *testing.T) {
	testredis.Start(t)
	srv := &wecomSrv{
		replies:   map[string]string{"token": `{"errcode":0,"access_token":"TK_RACE"}`},
		slowToken: true,
	}
	base := srv.server(t).URL
	m := NewTokenManager(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	m.SetBaseURL(base, nil)
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := m.Token(ctx, false)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = tok
		}(i)
	}
	wg.Wait()

	if srv.fetchN != 1 {
		t.Fatalf("并发应恰 1 次 gettoken，实际 %d", srv.fetchN)
	}
	for i, r := range results {
		if r != "TK_RACE" {
			t.Fatalf("结果[%d]应同值 TK_RACE: %q", i, r)
		}
	}
}

// TestToken_Invalidate 清缓存语义（42001 强制刷新的前提）
func TestToken_Invalidate(t *testing.T) {
	testredis.Start(t)
	seedToken(t, "TK")
	m := NewTokenManager(Credential{CorpID: "corp1", AgentID: 100, AppType: AppAgent})
	m.Invalidate(context.Background())
	v, _ := g.Redis().Get(context.Background(), "wecom:corp1:100:access_token")
	if v.String() != "" {
		t.Fatal("Invalidate 应清缓存")
	}
}

// ---- oauth ----

// TestClient_OauthDomainMethods OAuth 两域方法：端点/参数/自动带 access_token
func TestClient_OauthDomainMethods(t *testing.T) {
	testredis.Start(t)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+"|"+string(body))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	if _, err := c.GetUserinfoByCode(context.Background(), "CODE1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetUserDetailByTicket(context.Background(), "TICKET1"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"GET /cgi-bin/auth/getuserinfo?access_token=TK&code=CODE1|",
		`POST /cgi-bin/auth/getuserdetail?access_token=TK|{"user_ticket":"TICKET1"}`,
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("应恰 2 请求，实际 %d: %v", len(seen), seen)
	}
	for i, w := range want {
		if seen[i] != w {
			t.Fatalf("请求[%d]不符:\n got %s\nwant %s", i, seen[i], w)
		}
	}
}

// TestStateStore_CreateAndConsume state 生命周期：TTL 30s / 值=returnURL / 一次性消费
func TestStateStore_CreateAndConsume(t *testing.T) {
	mr := testredis.Start(t)
	ctx := context.Background()
	s := NewStateStore()

	state, err := s.Create(ctx, "https://fe.example.com/home")
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 32 {
		t.Fatalf("state 应为 32 字符 hex: %q", state)
	}
	ttl := mr.TTL("wecom:oauth:state:" + state)
	if ttl != 30*time.Second {
		t.Fatalf("TTL 应 30s，实际 %v", ttl)
	}
	v, _ := g.Redis().Get(ctx, "wecom:oauth:state:"+state)
	if v.String() != "https://fe.example.com/home" {
		t.Fatalf("值应为 returnURL: %q", v.String())
	}

	// 首次消费取回 returnURL，key 已删
	got, ok, err := s.Consume(ctx, state)
	if err != nil || !ok || got != "https://fe.example.com/home" {
		t.Fatalf("首次消费: %q %v %v", got, ok, err)
	}
	if mr.Exists("wecom:oauth:state:" + state) {
		t.Fatal("消费后 key 应已删")
	}

	// 二次消费失败
	_, ok, err = s.Consume(ctx, state)
	if err != nil || ok {
		t.Fatalf("二次消费应 ok=false: %v %v", ok, err)
	}

	// 不存在的 state 同样 ok=false
	_, ok, err = s.Consume(ctx, "not-exist-state-0000000000000000")
	if err != nil || ok {
		t.Fatalf("不存在 state 应 ok=false: %v %v", ok, err)
	}
}

// TestOauthManager_AuthorizeAndFrontendRedirect 编排链：授权 URL 内含可消费的 state，回跳消费一次性
func TestOauthManager_AuthorizeAndFrontendRedirect(t *testing.T) {
	testredis.Start(t)
	m := NewOauthManager(nil, NewStateStore(), "corp1", 1002, "https://example.com/cb")
	ctx := context.Background()

	u, err := m.BuildAuthorizeURL(ctx, "https://fe.example.com/home")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(strings.SplitN(u, "?", 2)[1])
	state := strings.TrimSuffix(q.Get("state"), "#wechat_redirect")
	got, ok, err := m.BuildFrontendRedirectURL(ctx, state, "CODE9")
	if err != nil || !ok {
		t.Fatalf("回跳应成功: %v %v %v", got, ok, err)
	}
	if want := "https://fe.example.com/home?code=CODE9"; got != want {
		t.Fatalf("回跳 URL 不符: %s", got)
	}

	// privateinfo 变体：scope 断言 + state 可消费同构
	pu, err := m.BuildPrivateInfoAuthorizeURL(ctx, "https://fe.example.com/home")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pu, "scope=snsapi_privateinfo") {
		t.Fatalf("privateinfo scope 不符: %s", pu)
	}
	pq, _ := url.ParseQuery(strings.SplitN(pu, "?", 2)[1])
	pState := strings.TrimSuffix(pq.Get("state"), "#wechat_redirect")
	if _, ok, _ := m.BuildFrontendRedirectURL(ctx, pState, "C"); !ok {
		t.Fatal("privateinfo state 应可消费")
	}

	// state 已消费，再次回跳 ok=false；不存在 state 同样
	if _, ok, _ := m.BuildFrontendRedirectURL(ctx, state, "CODE9"); ok {
		t.Fatal("已消费 state 二次回跳应 ok=false")
	}
	if _, ok, _ := m.BuildFrontendRedirectURL(ctx, "ghost-0000000000000000000000000000", "C"); ok {
		t.Fatal("不存在 state 应 ok=false")
	}
}

// TestOauthManager_DomainMethodsDelegate 域方法委托 Client（httptest 桩取证端点）
func TestOauthManager_DomainMethodsDelegate(t *testing.T) {
	testredis.Start(t)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"errcode":0,"userid":"u1"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")
	m := NewOauthManager(c, NewStateStore(), "corp1", 1002, "https://example.com/cb")

	if _, err := m.GetUserinfoByCode(context.Background(), "CODE1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetUserDetailByTicket(context.Background(), "TK1"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /cgi-bin/auth/getuserinfo", "POST /cgi-bin/auth/getuserdetail"}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("委托请求不符: %v", seen)
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	u := BuildAuthorizeURL("corp1", 1002, "https://example.com/cb?x=1", "st123")
	parsed, err := url.Parse(strings.Split(u, "#")[0])
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("appid") != "corp1" || q.Get("agentid") != "1002" || q.Get("state") != "st123" {
		t.Fatalf("参数不符: %v", q)
	}
	if q.Get("redirect_uri") != "https://example.com/cb?x=1" {
		t.Fatalf("redirect_uri 应已编码且可还原: %q", q.Get("redirect_uri"))
	}
	if !strings.Contains(u, "#wechat_redirect") {
		t.Fatal("缺 wechat_redirect 锚")
	}
}

func TestBuildQrConnectURL(t *testing.T) {
	u := BuildQrConnectURL("corp1", 1002, "https://example.com/cb", "st")
	if !strings.Contains(u, "login_type=CorpApp") || !strings.Contains(u, "appid=corp1") ||
		!strings.Contains(u, "agentid=1002") || !strings.Contains(u, "state=st") {
		t.Fatalf("扫码 URL 参数不符: %s", u)
	}
	if !strings.Contains(u, url.QueryEscape("https://example.com/cb")) {
		t.Fatalf("redirect_uri 应编码: %s", u)
	}
}

// TestBuildPrivateInfoAuthorizeURL scope=snsapi_privateinfo，其余参数与 base 变体逐参数一致
func TestBuildPrivateInfoAuthorizeURL(t *testing.T) {
	got := BuildPrivateInfoAuthorizeURL("corp1", 1002, "https://example.com/cb?x=1", "st9")
	base := BuildAuthorizeURL("corp1", 1002, "https://example.com/cb?x=1", "st9")

	q, err := url.ParseQuery(strings.TrimPrefix(strings.SplitN(got, "#", 2)[0], "https://open.weixin.qq.com/connect/oauth2/authorize?"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("scope") != "snsapi_privateinfo" {
		t.Fatalf("scope 应为 snsapi_privateinfo: %s", got)
	}

	// 与 base 变体同输入逐参数 diff：仅 scope 差异
	baseQ, _ := url.ParseQuery(strings.TrimPrefix(strings.SplitN(base, "#", 2)[0], "https://open.weixin.qq.com/connect/oauth2/authorize?"))
	for k := range baseQ {
		if k == "scope" {
			continue
		}
		if q.Get(k) != baseQ.Get(k) {
			t.Fatalf("参数 %s 两变体不一致: %q vs %q", k, q.Get(k), baseQ.Get(k))
		}
	}
	if len(q) != len(baseQ) {
		t.Fatalf("参数集合应一致: %v vs %v", q, baseQ)
	}
	if !strings.Contains(got, "#wechat_redirect") {
		t.Fatal("缺 wechat_redirect 锚")
	}
}

// TestBuildFrontendRedirectURL 回跳 URL 按 returnURL 是否含 ? 选 &/? 拼接 code
func TestBuildFrontendRedirectURL(t *testing.T) {
	if got := BuildFrontendRedirectURL("https://fe.example.com/home?a=1", "C1"); got != "https://fe.example.com/home?a=1&code=C1" {
		t.Fatalf("含 ? 应以 & 拼接: %s", got)
	}
	if got := BuildFrontendRedirectURL("https://fe.example.com/home", "C1"); got != "https://fe.example.com/home?code=C1" {
		t.Fatalf("不含 ? 应以 ? 拼接: %s", got)
	}
}

// ---- message ----

func TestMessageSerializationKeys(t *testing.T) {
	cases := []struct {
		name     string
		msg      any
		wantKeys []string
	}{
		{"text", TextMsg{Content: "hi"}, []string{`"msgtype":"text"`, `"text":{"content":"hi"}`}},
		{"markdown", MarkdownMsg{Content: "# t"}, []string{`"msgtype":"markdown"`, `"markdown":{"content":"# t"}`}},
		{"image", ImageMsg{MediaID: "m1"}, []string{`"msgtype":"image"`, `"image":{"media_id":"m1"}`}},
		{"voice", VoiceMsg{MediaID: "m2"}, []string{`"msgtype":"voice"`, `"voice":{"media_id":"m2"}`}},
		{"video", VideoMsg{MediaID: "m3", Title: "T"}, []string{`"msgtype":"video"`, `"video":{"media_id":"m3","title":"T"`}},
		{"file", FileMsg{MediaID: "m4"}, []string{`"msgtype":"file"`, `"file":{"media_id":"m4"}`}},
		{"news", NewsMsg{Articles: []map[string]any{{"title": "a"}}}, []string{`"msgtype":"news"`, `"news":{"articles":`}},
		{"mpnews", MpNewsMsg{Articles: []map[string]any{{"title": "b"}}}, []string{`"msgtype":"mpnews"`, `"mpnews":{"articles":`}},
		{"textcard", TextCardMsg{Title: "T", Description: "D", URL: "https://x"}, []string{`"msgtype":"textcard"`, `"textcard":{"title":"T","description":"D","url":"https://x"}`}},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.msg)
		if err != nil {
			t.Fatalf("%s 序列化失败: %v", c.name, err)
		}
		for _, want := range c.wantKeys {
			if !strings.Contains(string(raw), want) {
				t.Errorf("%s 缺 %s: %s", c.name, want, raw)
			}
		}
	}
}

// ---- callback ----

// TestHandleCallback_BatchJobResult 导出任务完成通知：嵌套 BatchJob 节点展平 + 专属事件名
func TestHandleCallback_BatchJobResult(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	plain := `<xml><ToUserName><![CDATA[corp]]></ToUserName><CreateTime>1723963200</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[batch_job_result]]></Event><BatchJob><JobId><![CDATA[job123]]></JobId><JobType><![CDATA[export_user]]></JobType><ErrCode>0</ErrCode><ErrMsg><![CDATA[ok]]></ErrMsg></BatchJob></xml>`
	enc, _ := c.Encrypt(plain)
	ts, nonce := "1723963201", "n5"
	sig := signatureSHA1(testToken, ts, nonce, enc)
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, enc)

	d := events.New()
	var got *CallbackEvent
	d.Subscribe(EventBatchJobResult, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(CallbackEvent); ok {
			got = &p
		}
	})

	ev, err := c.HandleCallback(context.Background(), d, sig, ts, nonce, body)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("应派发到 wecom.batch_job_result")
	}
	if got.Fields["JobId"] != "job123" || got.Fields["JobType"] != "export_user" ||
		got.Fields["ErrCode"] != "0" || got.Fields["ErrMsg"] != "ok" {
		t.Fatalf("BatchJob 嵌套字段应展平进 Fields: %v", got.Fields)
	}
	if ev.Event != "batch_job_result" {
		t.Fatalf("事件名不符: %s", ev.Event)
	}
}

// TestHandleCallback_UpdateTagIndependent 标签变更独立派发（不再落入 change_contact 族）
func TestHandleCallback_UpdateTagIndependent(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	plain := `<xml><ToUserName><![CDATA[corp]]></ToUserName><CreateTime>1723963200</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[update_tag]]></Event><TagId><![CDATA[1]]></TagId><AddUserItems></AddUserItems></xml>`
	enc, _ := c.Encrypt(plain)
	ts, nonce := "1723963202", "n6"
	sig := signatureSHA1(testToken, ts, nonce, enc)
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, enc)

	d := events.New()
	var tagHits, contactHits int
	d.Subscribe(EventUpdateTag, 0, func(_ context.Context, e events.Event) { tagHits++ })
	d.Subscribe(EventChangeContact, 0, func(_ context.Context, e events.Event) { contactHits++ })

	if _, err := c.HandleCallback(context.Background(), d, sig, ts, nonce, body); err != nil {
		t.Fatal(err)
	}
	if tagHits != 1 || contactHits != 0 {
		t.Fatalf("update_tag 应派发 wecom.update_tag 且不落 change_contact: tag=%d contact=%d", tagHits, contactHits)
	}
}

// TestHandleCallback_SwitchWorkbenchMode 工作台自定义模式开关切换事件
func TestHandleCallback_SwitchWorkbenchMode(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	plain := `<xml><ToUserName><![CDATA[corp]]></ToUserName><CreateTime>1723963200</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[switch_workbench_mode]]></Event><AgentID>1001</AgentID><UserID><![CDATA[u1]]></UserID><Mode>1</Mode></xml>`
	enc, _ := c.Encrypt(plain)
	ts, nonce := "1723963204", "n8"
	sig := signatureSHA1(testToken, ts, nonce, enc)
	body := `<xml><Encrypt><![CDATA[` + enc + `]]></Encrypt></xml>`

	d := events.New()
	var got *CallbackEvent
	d.Subscribe(EventSwitchWorkbenchMode, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(CallbackEvent); ok {
			got = &p
		}
	})
	if _, err := c.HandleCallback(context.Background(), d, sig, ts, nonce, body); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("应派发到 wecom.switch_workbench_mode")
	}
	if got.Fields["AgentID"] != "1001" || got.Fields["Mode"] != "1" {
		t.Fatalf("字段不符: %v", got.Fields)
	}
}

// TestHandleCallback_ExternalFamilyIndependent external 族事件独立派发（不落通讯录事件）
func TestHandleCallback_ExternalFamilyIndependent(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	mk := func(event, changeType string) (sig, ts, nonce, body string) {
		plain := `<xml><ToUserName><![CDATA[corp]]></ToUserName><CreateTime>1723963200</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[` + event + `]]></Event><ChangeType><![CDATA[` + changeType + `]]></ChangeType><UserID><![CDATA[u1]]></UserID><ExternalUserID><![CDATA[EXT1]]></ExternalUserID></xml>`
		enc, _ := c.Encrypt(plain)
		ts, nonce = "1723963205", "n9"
		return signatureSHA1(testToken, ts, nonce, enc), ts, nonce, `<xml><Encrypt><![CDATA[` + enc + `]]></Encrypt></xml>`
	}

	for _, tc := range []struct {
		event, changeType, wantEvent string
	}{
		{"change_external_contact", "del_follow_user", EventChangeExternalContact},
		{"change_external_tag", "shuffle", EventChangeExternalTag},
		{"change_external_chat", "dismiss", EventChangeExternalChat},
		{"customer_acquisition", "friend_request", EventCustomerAcquisition},
		{"kf_msg_or_event", "", EventKfMsgOrEvent},
		{"sys_approval_change", "", EventSysApprovalChange},
		{"open_approval_change", "", EventOpenApprovalChange},
		{"modify_calendar", "", EventModifyCalendar},
		{"delete_calendar", "", EventDeleteCalendar},
		{"modify_schedule", "", EventModifySchedule},
		{"delete_schedule", "", EventDeleteSchedule},
		{"respond_schedule", "", EventRespondSchedule},
		{"meeting_change", "modify_meeting", EventMeetingChange},
		{"meeting_change", "cancel_meeting", EventMeetingChange},
	} {
		d := events.New()
		var hit string
		var ct string
		d.Subscribe(tc.wantEvent, 0, func(_ context.Context, e events.Event) {
			if p, ok := e.Payload.(CallbackEvent); ok {
				hit = tc.wantEvent
				ct = p.ChangeType
			}
		})
		contactHit := false
		d.Subscribe(EventChangeContact, 0, func(_ context.Context, e events.Event) { contactHit = true })

		sig, ts, nonce, body := mk(tc.event, tc.changeType)
		if _, err := c.HandleCallback(context.Background(), d, sig, ts, nonce, body); err != nil {
			t.Fatal(err)
		}
		if hit == "" || ct != tc.changeType {
			t.Fatalf("%s 应派发 %s（ChangeType=%s）: hit=%q ct=%q", tc.event, tc.wantEvent, tc.changeType, hit, ct)
		}
		if contactHit {
			t.Fatalf("%s 不应落入 EventChangeContact", tc.event)
		}
	}
}

func TestHandleCallback_EventDispatch(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	plain := `<xml><ToUserName><![CDATA[corp]]></ToUserName><CreateTime>1723963200</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[change_contact]]></Event><ChangeType><![CDATA[create_user]]></ChangeType><UserID><![CDATA[u9]]></UserID></xml>`
	enc, _ := c.Encrypt(plain)
	ts, nonce := "1723963200", "n4"
	sig := signatureSHA1(testToken, ts, nonce, enc)
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, enc)

	d := events.New()
	var mu sync.Mutex
	var got []CallbackEvent
	d.Subscribe(EventChangeContact, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(CallbackEvent); ok {
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
		}
	})

	ev, err := c.HandleCallback(context.Background(), d, sig, ts, nonce, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("应恰捕获一次，实际 %d", len(got))
	}
	if got[0].ChangeType != "create_user" || got[0].Fields["UserID"] != "u9" {
		t.Fatalf("事件字段不符: %+v", got[0])
	}
	if ev.Event != "change_contact" {
		t.Fatalf("事件名不符: %s", ev.Event)
	}

	// 签名篡改拒绝
	if _, err := c.HandleCallback(context.Background(), d, "bad"+sig[3:], ts, nonce, body); err == nil {
		t.Fatal("签名篡改应拒绝")
	}
}

// TestHandleCallback_IdentityFields 载荷身份：CorpID（crypt receiveid）与 AgentID 在位
func TestHandleCallback_IdentityFields(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, "corp_id_1")
	plain := `<xml><ToUserName><![CDATA[corp_id_1]]></ToUserName><CreateTime>1723963200</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[change_contact]]></Event><ChangeType><![CDATA[create_user]]></ChangeType><AgentID>1001</AgentID><UserID><![CDATA[u1]]></UserID></xml>`
	enc, _ := c.Encrypt(plain)
	ts, nonce := "1723963206", "n10"
	sig := signatureSHA1(testToken, ts, nonce, enc)
	body := fmt.Sprintf(`<xml><Encrypt><![CDATA[%s]]></Encrypt></xml>`, enc)

	d := events.New()
	var got *CallbackEvent
	d.Subscribe(EventChangeContact, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(CallbackEvent); ok {
			got = &p
		}
	})
	if _, err := c.HandleCallback(context.Background(), d, sig, ts, nonce, body); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CorpID != "corp_id_1" || got.AgentID != "1001" {
		t.Fatalf("身份字段不符: %+v", got)
	}
}

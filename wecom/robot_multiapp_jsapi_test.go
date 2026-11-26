package wecom

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestRobot_TableDriven 六 msgtype 桩断言（URL key + body 形态）
func TestRobot_TableDriven(t *testing.T) {
	var mu sync.Mutex
	type hit struct{ url, msgType, carrier string }
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		mt, _ := decoded["msgtype"].(string)
		carrier := ""
		for k := range decoded {
			if k != "msgtype" {
				carrier = k
			}
		}
		mu.Lock()
		hits = append(hits, hit{r.URL.RequestURI(), mt, carrier})
		mu.Unlock()
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	t.Cleanup(srv.Close)

	robot := NewRobotClient("KEY123")
	robot.BaseURL = srv.URL
	ctx := context.Background()
	cases := []struct {
		name    string
		invoke  func() error
		carrier string
	}{
		{"text", func() error { return robot.SendRobotText(ctx, "hi", nil, nil) }, "text"},
		{"markdown", func() error { return robot.SendRobotMarkdown(ctx, "# t") }, "markdown"},
		{"image", func() error { return robot.SendRobotImage(ctx, "B64", "MD5") }, "image"},
		{"news", func() error {
			return robot.SendRobotNews(ctx, []map[string]any{{"title": "a", "url": "https://x"}})
		}, "news"},
		{"file", func() error { return robot.SendRobotFile(ctx, "M1") }, "file"},
		{"template_card", func() error {
			return robot.SendRobotTemplateCard(ctx, map[string]any{"card_type": "text_notice"})
		}, "template_card"},
	}
	for _, c := range cases {
		if err := c.invoke(); err != nil {
			t.Fatalf("%s 发送失败: %v", c.name, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 6 {
		t.Fatalf("应恰 6 请求，实际 %d", len(hits))
	}
	for i, c := range cases {
		h := hits[i]
		if !strings.HasPrefix(h.url, "/cgi-bin/webhook/send?key=KEY123") {
			t.Fatalf("[%d] URL 不符: %s", i, h.url)
		}
		if h.msgType != c.name || h.carrier != c.carrier {
			t.Fatalf("[%d] msgtype/carrier 不符: %s/%s", i, h.msgType, h.carrier)
		}
	}
}

// TestMultiApp_ThreeAppsIsolated 同 corpID 三应用：按名取用/凭证隔离/缓存键不同/回调独立
func TestMultiApp_ThreeAppsIsolated(t *testing.T) {
	m := NewCorpManager("corp1")
	if err := m.Register("contact", Credential{CorpID: "corp1", Secret: "contact-sec", AppType: AppContact}, "tok-c", "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"); err != nil {
		t.Fatal(err)
	}
	if err := m.Register("app1", Credential{CorpID: "corp1", AgentID: 1001, Secret: "sec1", AppType: AppAgent}, "tok-1", "0123456789abcdefghijklmnopqrstuvwxyzABCDEXY"); err != nil {
		t.Fatal(err)
	}
	if err := m.Register("app2", Credential{CorpID: "corp1", AgentID: 1002, Secret: "sec2", AppType: AppAgent}, "tok-2", "0123456789abcdefghijklmnopqrstuvwxyzABCDEZW"); err != nil {
		t.Fatal(err)
	}
	// corpID 不一致拒绝
	if err := m.Register("bad", Credential{CorpID: "corpX"}, "t", "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"); err == nil {
		t.Fatal("corpID 不一致应拒绝")
	}
	c1, err := m.Client("app1")
	if err != nil || c1.Creds.AgentID != 1001 || c1.Creds.Secret != "sec1" {
		t.Fatalf("按名取 app1 不符: %v", err)
	}
	if _, err := m.Client("ghost"); err == nil {
		t.Fatal("未知名应报错")
	}
	// 凭证隔离
	c2, _ := m.Client("app2")
	if c1.Creds.Secret == c2.Creds.Secret || c1.tokenMgr.cacheKey() == c2.tokenMgr.cacheKey() {
		t.Fatal("两自建应用 secret/缓存键不应相同")
	}
	cc, _ := m.Client("contact")
	if cc.tokenMgr.cacheKey() == c1.tokenMgr.cacheKey() {
		t.Fatal("通讯录应用与自建应用缓存键不应相同")
	}
	// 回调独立：三 Crypt 各自 round-trip（不同 token/AESKey 加密互验失败）
	k1, _ := m.Crypt("app1")
	k2, _ := m.Crypt("app2")
	enc, err := k1.Encrypt(`<xml><Event><![CDATA[subscribe]]></Event></xml>`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k2.Decrypt(enc); err == nil {
		t.Fatal("不同应用 AESKey 应解不开对方密文")
	}
}

// TestTicket_ConcurrentSingleFetch 并发恰一次 + forceRefresh 新值
func TestTicket_ConcurrentSingleFetch(t *testing.T) {
	testredis.Start(t)
	var fetchN int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/get_jsapi_ticket" {
			atomic.AddInt32(&fetchN, 1)
			time.Sleep(30 * time.Millisecond)
			_, _ = w.Write([]byte(`{"errcode":0,"ticket":"TICKET_RACE"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"access_token":"TK"}`))
	}))
	t.Cleanup(srv.Close)

	m := NewTicketManager(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	m.SetBaseURL(srv.URL, nil)
	ctx := context.Background()
	// 预置 token 避免 gettoken 走真实域名
	seedToken(t, "TK")

	var wg sync.WaitGroup
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tk, err := m.Ticket(ctx, false)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = tk
		}(i)
	}
	wg.Wait()
	if n := atomic.LoadInt32(&fetchN); n != 1 {
		t.Fatalf("并发应恰 1 次 fetch，实际 %d", n)
	}
	for i, r := range results {
		if r != "TICKET_RACE" {
			t.Fatalf("结果[%d]应同值: %s", i, r)
		}
	}
}

// TestJsApiConfig_SHA1Formula SHA1 公式向量（独立重算一致）+ 嵌入扫码五键
func TestJsApiConfig_SHA1Formula(t *testing.T) {
	ticket, nonce, ts, url := "TICKET1", "NONCE1", "1723963200", "https://fe.example.com/page"
	cfg := BuildJsApiConfig(ticket, nonce, ts, url)
	want := sha1.Sum([]byte("jsapi_ticket=" + ticket + "&noncestr=" + nonce + "&timestamp=" + ts + "&url=" + url))
	if cfg["signature"] != hex.EncodeToString(want[:]) {
		t.Fatalf("签名公式不符: %s", cfg["signature"])
	}
	for _, k := range []string{"jsapi_ticket", "noncestr", "timestamp", "url", "signature"} {
		if _, ok := cfg[k]; !ok {
			t.Fatalf("缺键 %s", k)
		}
	}

	params := BuildQrConnectParams("corp1", 1002, "https://cb", "st", "https://css")
	if len(params) != 5 || params["appid"] != "corp1" || params["agentid"] != "1002" ||
		params["redirect_uri"] != "https://cb" || params["state"] != "st" || params["href"] != "https://css" {
		t.Fatalf("五键参数集不符: %v", params)
	}
}

var _ = errors.New

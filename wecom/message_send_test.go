package wecom

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestSendMessage_MsgExpansion msg 键（Msg 类型）展开合并进外层路由字段
func TestSendMessage_MsgExpansion(t *testing.T) {
	testredis.Start(t)
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"errcode":0,"msgid":"MSG1"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	resp, err := c.SendMessage(context.Background(), map[string]any{
		"touser":  "u1",
		"agentid": 1001,
		"msg":     TextMsg{Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp["msgid"] != "MSG1" {
		t.Fatalf("msgid 不符: %v", resp)
	}
	for _, want := range []string{`"touser":"u1"`, `"agentid":1001`, `"msgtype":"text"`, `"text":{"content":"hi"}`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body 缺 %s: %s", want, body)
		}
	}
	if strings.Contains(body, `"msg":`) {
		t.Fatalf("msg 键应被展开而非保留: %s", body)
	}
}

// TestSendMessage_RawMap 原生 map（内联载体）透传
func TestSendMessage_RawMap(t *testing.T) {
	testredis.Start(t)
	var body, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, path = string(b), r.URL.Path
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	if _, err := c.SendMessage(context.Background(), map[string]any{
		"touser": "u2", "msgtype": "text", "text": map[string]any{"content": "raw"},
	}); err != nil {
		t.Fatal(err)
	}
	if path != "/cgi-bin/message/send" {
		t.Fatalf("path 不符: %s", path)
	}
	if !strings.Contains(body, `"content":"raw"`) {
		t.Fatalf("内联载体应透传: %s", body)
	}
}

// TestAppchatAndRecall 群聊四件 + 撤回 + 模版卡片 + 学校通知 path 命中
func TestAppchatAndRecall(t *testing.T) {
	testredis.Start(t)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")
	ctx := context.Background()

	_, _ = c.CreateAppChat(ctx, map[string]any{"name": "g", "userlist": []string{"u1"}})
	_, _ = c.UpdateAppChat(ctx, "CHAT1", map[string]any{"name": "g2"})
	_, _ = c.GetAppChat(ctx, "CHAT1")
	_, _ = c.SendAppChat(ctx, map[string]any{"chatid": "CHAT1", "msgtype": "text", "text": map[string]any{"content": "hi"}})
	_, _ = c.RecallMessage(ctx, "MSG1")
	_, _ = c.UpdateTemplateCard(ctx, map[string]any{"response_code": "RC", "template_card": map[string]any{}})
	_, _ = c.SendSchoolNotice(ctx, map[string]any{"recv_scope": 1, "msgtype": "text", "text": map[string]any{"content": "n"}})

	want := []string{
		"POST /cgi-bin/appchat/create",
		"POST /cgi-bin/appchat/update",
		"GET /cgi-bin/appchat/get",
		"POST /cgi-bin/appchat/send",
		"POST /cgi-bin/message/recall",
		"POST /cgi-bin/message/update_template_card",
		"POST /cgi-bin/externalcontact/message/send",
	}
	if len(paths) != len(want) {
		t.Fatalf("请求 %d 次 != %d: %v", len(paths), len(want), paths)
	}
	for i, w := range want {
		if paths[i] != w {
			t.Fatalf("[%d] %s != %s", i, paths[i], w)
		}
	}
}

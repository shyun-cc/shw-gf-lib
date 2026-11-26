package wecom

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

// TestMedia_UploadMultipart multipart 上传：Content-Type/path/query/文件字段/响应解析
func TestMedia_UploadMultipart(t *testing.T) {
	testredis.Start(t)
	var mu sync.Mutex
	var gotCT, gotPath, gotQuery, gotFile string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotCT, gotPath, gotQuery = r.Header.Get("Content-Type"), r.URL.Path, r.URL.RawQuery
		mu.Unlock()
		// 解析 multipart 文件字段 media
		mr := multipart.NewReader(strings.NewReader(string(body)), strings.Split(r.Header.Get("Content-Type"), "boundary=")[1])
		form, err := mr.ReadForm(1 << 20)
		if err == nil {
			if f, ok := form.File["media"]; ok && len(f) > 0 {
				gotFile = f[0].Filename
			}
		}
		_, _ = w.Write([]byte(`{"errcode":0,"type":"image","media_id":"M_1","created_at":1723963200}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	resp, err := c.UploadMedia(context.Background(), "image", "pic.jpg", strings.NewReader("JPEGBYTES"))
	if err != nil {
		t.Fatal(err)
	}
	if resp["media_id"] != "M_1" {
		t.Fatalf("media_id 不符: %v", resp)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(gotCT, "multipart/form-data") {
		t.Fatalf("Content-Type 应 multipart: %s", gotCT)
	}
	if gotPath != "/cgi-bin/media/upload" {
		t.Fatalf("path 不符: %s", gotPath)
	}
	if !strings.Contains(gotQuery, "type=image") || !strings.Contains(gotQuery, "access_token=TK") {
		t.Fatalf("query 不符: %s", gotQuery)
	}
	if gotFile != "pic.jpg" {
		t.Fatalf("文件名字段不符: %q", gotFile)
	}
}

// TestMedia_UploadMultipartBizError multipart 上传 errcode → BizError
func TestMedia_UploadMultipartBizError(t *testing.T) {
	testredis.Start(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":44001,"errmsg":"empty post data"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	_, err := c.UploadMedia(context.Background(), "image", "pic.jpg", strings.NewReader("x"))
	var biz *BizError
	if !errors.As(err, &biz) || biz.Errcode != 44001 {
		t.Fatalf("应 BizError 44001: %v", err)
	}
}

// TestMedia_DownloadFile 二进制下载：字节/ContentType/filename
func TestMedia_DownloadFile(t *testing.T) {
	testredis.Start(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-disposition", `attachment; filename="pic.jpg"; filename*=UTF-8''pic.jpg`)
		w.Header().Set("Content-Length", "4")
		_, _ = w.Write([]byte("DATA"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	ct, name, data, err := c.GetMedia(context.Background(), "M_1")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "image/jpeg" || string(data) != "DATA" {
		t.Fatalf("下载结果不符: ct=%s data=%q", ct, data)
	}
	if !strings.Contains(name, "pic.jpg") {
		t.Fatalf("filename 不符: %q", name)
	}
}

// TestMedia_DownloadBizError 下载失败响应为 JSON errcode → BizError
func TestMedia_DownloadBizError(t *testing.T) {
	testredis.Start(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"errcode":40007,"errmsg":"invalid media_id"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	_, _, _, err := c.GetMedia(context.Background(), "BAD")
	var biz *BizError
	if !errors.As(err, &biz) || biz.Errcode != 40007 {
		t.Fatalf("应 BizError 40007: %v", err)
	}
}

// TestMedia_UploadByURLJsonBody 异步上传两端点 JSON body 形态
func TestMedia_UploadByURLJsonBody(t *testing.T) {
	testredis.Start(t)
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.Path+"|"+string(b))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"errcode":0,"jobid":"J1","status":2,"detail":{"media_id":"M_2"}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(Credential{CorpID: "corp1", AgentID: 100, Secret: "sec", AppType: AppAgent})
	c.SetBaseURL(srv.URL)
	seedToken(t, "TK")

	if _, err := c.UploadMediaByURL(context.Background(), map[string]any{
		"scene": 1, "type": "video", "filename": "v.mp4", "url": "https://cdn.example.com/v.mp4", "md5": "d41d8",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetUploadByURLResult(context.Background(), "J1"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("应恰 2 请求: %v", bodies)
	}
	if !strings.Contains(bodies[0], "/cgi-bin/media/upload_by_url|") || !strings.Contains(bodies[0], `"filename":"v.mp4"`) {
		t.Fatalf("upload_by_url 请求不符: %s", bodies[0])
	}
	if !strings.Contains(bodies[1], "/cgi-bin/media/get_upload_by_url_result|") || !strings.Contains(bodies[1], `"jobid":"J1"`) {
		t.Fatalf("get_upload_by_url_result 请求不符: %s", bodies[1])
	}
}

// TestHandleCallback_UploadMediaJobFinish 异步上传完成事件派发
func TestHandleCallback_UploadMediaJobFinish(t *testing.T) {
	c, _ := NewWXBizMsgCrypt(testToken, testAESKey, testCorpID)
	plain := `<xml><ToUserName><![CDATA[corp]]></ToUserName><CreateTime>1723963200</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[upload_media_job_finish]]></Event><JobId><![CDATA[J42]]></JobId></xml>`
	enc, _ := c.Encrypt(plain)
	ts, nonce := "1723963203", "n7"
	sig := signatureSHA1(testToken, ts, nonce, enc)
	body := `<xml><Encrypt><![CDATA[` + enc + `]]></Encrypt></xml>`

	d := events.New()
	var got *CallbackEvent
	d.Subscribe(EventUploadMediaJobFinish, 0, func(_ context.Context, e events.Event) {
		if p, ok := e.Payload.(CallbackEvent); ok {
			got = &p
		}
	})

	if _, err := c.HandleCallback(context.Background(), d, sig, ts, nonce, body); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("应派发到 wecom.upload_media_job_finish")
	}
	if got.Fields["JobId"] != "J42" {
		t.Fatalf("JobId 应在位: %v", got.Fields)
	}
}

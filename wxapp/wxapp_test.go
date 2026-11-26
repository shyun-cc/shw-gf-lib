package wxapp

import (
	"bytes"
	"context"
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

// captureSrv 捕获请求（路径/query/body）并按 path 返回夹具
func captureSrv(t *testing.T, capPath *string, capQuery *string, capBody *string, replies map[string]string, tokenFetchN *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capPath = r.URL.Path
		*capQuery = r.URL.RawQuery
		body, _ := io.ReadAll(r.Body)
		*capBody = string(body)
		if r.URL.Path == epToken.Path && tokenFetchN != nil {
			atomic.AddInt32(tokenFetchN, 1)
		}
		_, _ = w.Write([]byte(replies[r.URL.Path]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCode2Session_RequestAndParse(t *testing.T) {
	var p, q, b string
	srv := captureSrv(t, &p, &q, &b, map[string]string{
		epCode2Session.Path: `{"openid":"oABC","session_key":"sk123","unionid":"uXYZ"}`,
	}, nil)

	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL
	sess, err := s.Code2Session(context.Background(), "jscode1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.OpenID != "oABC" || sess.SessionKey != "sk123" || sess.UnionID != "uXYZ" {
		t.Fatalf("session 不符: %+v", sess)
	}
	if p != "/sns/jscode2session" {
		t.Fatalf("路径不符: %s", p)
	}
	for _, want := range []string{"appid=wx123", "secret=sec", "js_code=jscode1", "grant_type=authorization_code"} {
		if !strings.Contains(q, want) {
			t.Fatalf("query 缺 %s: %s", want, q)
		}
	}
}

func TestCode2Session_Errcode(t *testing.T) {
	var p, q, b string
	srv := captureSrv(t, &p, &q, &b, map[string]string{
		epCode2Session.Path: `{"errcode":40029,"errmsg":"invalid code"}`,
	}, nil)
	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL
	_, err := s.Code2Session(context.Background(), "bad")
	if err == nil {
		t.Fatal("errcode≠0 应报错")
	}
	var biz *BizError
	if !errors.As(err, &biz) || biz.Errcode != 40029 {
		t.Fatalf("应捕获 BizError 携带原始 errcode: %v", err)
	}
	var infra *InfraError
	if errors.As(err, &infra) {
		t.Fatal("业务错误不应同时是 InfraError")
	}
}

func TestGetPhoneNumber_PostBody(t *testing.T) {
	testredis.Start(t)
	var p, q, b string
	srv := captureSrv(t, &p, &q, &b, map[string]string{
		epToken.Path:        `{"access_token":"TK1"}`,
		epGetUserPhone.Path: `{"errcode":0,"errmsg":"ok","phone_info":{"phoneNumber":"10800000000"}}`,
	}, nil)

	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL
	phone, err := s.GetPhoneNumber(context.Background(), "phonecode")
	if err != nil {
		t.Fatal(err)
	}
	if phone != "10800000000" {
		t.Fatalf("手机号不符: %s", phone)
	}
	if p != "/wxa/business/getuserphonenumber" {
		t.Fatalf("路径不符: %s", p)
	}
	if !strings.Contains(q, "access_token=TK1") {
		t.Fatalf("query 应带 access_token: %s", q)
	}
	if b != `{"code":"phonecode"}` {
		t.Fatalf("body 应为 JSON code: %s", b)
	}
}

func TestAccessToken_CacheHitNoHTTP(t *testing.T) {
	mr := testredis.Start(t)
	var fetchN int32
	var p, q, b string
	srv := captureSrv(t, &p, &q, &b, map[string]string{
		epToken.Path: `{"access_token":"TK_CACHE"}`,
	}, &fetchN)

	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL
	ctx := context.Background()

	first, err := s.GetAccessToken(ctx)
	if err != nil || first != "TK_CACHE" {
		t.Fatalf("首次获取失败: %v %s", err, first)
	}
	if fetchN != 1 {
		t.Fatalf("首次应 fetch 1 次，实际 %d", fetchN)
	}
	// TTL=7000s
	ttl := mr.TTL("wxapp:access_token:wx123")
	if ttl <= 0 || ttl > 7000e9 {
		t.Fatalf("TTL 应为 7000s 附近，实际 %v", ttl)
	}

	// 缓存命中：不再发 HTTP
	second, err := s.GetAccessToken(ctx)
	if err != nil || second != "TK_CACHE" {
		t.Fatalf("缓存命中应返回同值: %v %s", err, second)
	}
	if fetchN != 1 {
		t.Fatalf("缓存命中不应发 HTTP，fetch 计数 %d", fetchN)
	}
}

func TestAccessToken_ConcurrentSingleFetch(t *testing.T) {
	testredis.Start(t)
	var fetchN int32
	// token 端点返回慢响应制造并发窗口
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == epToken.Path {
			atomic.AddInt32(&fetchN, 1)
			// 慢响应制造并发窗口：让全部并发请求都到达后再回写
			time.Sleep(30 * time.Millisecond)
		}
		_, _ = w.Write([]byte(`{"access_token":"TK_RACE"}`))
	}))
	t.Cleanup(srv.Close)
	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := s.GetAccessToken(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = tok
		}(i)
	}
	wg.Wait()

	if fetchN != 1 {
		t.Fatalf("并发应恰 1 次 fetch，实际 %d", fetchN)
	}
	for i, r := range results {
		if r != "TK_RACE" {
			t.Fatalf("结果[%d]应同值 TK_RACE: %s", i, r)
		}
	}
}

// TestErr_InfraErrorNonJSON 响应非 JSON：InfraError 单独可断言
func TestErr_InfraErrorNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>gateway error</html>"))
	}))
	t.Cleanup(srv.Close)

	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL
	_, err := s.Code2Session(context.Background(), "C1")
	if err == nil {
		t.Fatal("非 JSON 应报错")
	}
	var infra *InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("应可 errors.As 捕获 InfraError: %v", err)
	}
	var biz *BizError
	if errors.As(err, &biz) {
		t.Fatal("基础设施错误不应同时是 BizError")
	}
}

// TestErr_InfraErrorNetworkFail 网络失败：InfraError 且 Unwrap 非 nil
func TestErr_InfraErrorNetworkFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // 立即关闭制造连接拒绝

	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL
	_, err := s.Code2Session(context.Background(), "C1")
	if err == nil {
		t.Fatal("连接拒绝应报错")
	}
	var infra *InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("应可 errors.As 捕获 InfraError: %v", err)
	}
	if errors.Unwrap(infra) == nil {
		t.Fatal("InfraError 应可 Unwrap 底层错误")
	}
}

// TestQrcodeLink_ImageEndpoints 图片三件：成功应答二进制 buffer 透传 + 失败 JSON → BizError
func TestQrcodeLink_ImageEndpoints(t *testing.T) {
	testredis.Start(t)
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == epToken.Path {
			_, _ = w.Write([]byte(`{"access_token":"TK1"}`))
			return
		}
		switch r.URL.Path {
		case epGetWXACode.Path, epGetWXACodeUnlimit.Path, epCreateWXAQRCode.Path:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		default:
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		}
	}))
	t.Cleanup(srv.Close)
	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL

	for name, fn := range map[string]func() ([]byte, error){
		"CreateWXACode": func() ([]byte, error) {
			return s.CreateWXACode(context.Background(), map[string]any{"path": "pages/i"})
		},
		"CreateUnlimitedWXACode": func() ([]byte, error) {
			return s.CreateUnlimitedWXACode(context.Background(), map[string]any{"scene": "a=1"})
		},
		"CreateWXAQRCode": func() ([]byte, error) {
			return s.CreateWXAQRCode(context.Background(), map[string]any{"path": "pages/i"})
		},
	} {
		got, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.HasPrefix(got, []byte{0x89, 'P'}) {
			t.Fatalf("%s 应返回 PNG 二进制: %v", name, got)
		}
	}
}

// TestQrcodeLink_BizErrorOnJSONFail 码数量超限（45029）→ BizError
func TestQrcodeLink_BizErrorOnJSONFail(t *testing.T) {
	testredis.Start(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == epToken.Path {
			_, _ = w.Write([]byte(`{"access_token":"TK1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":45029,"errmsg":"reach max api daily quota"}`))
	}))
	t.Cleanup(srv.Close)
	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL
	_, err := s.CreateWXACode(context.Background(), map[string]any{"path": "p"})
	var biz *BizError
	if !errors.As(err, &biz) || biz.Errcode != 45029 {
		t.Fatalf("应返回 BizError 45029: %v", err)
	}
}

// TestUserInfo_FourEndpoints 用户信息四件路径与 query 断言
func TestUserInfo_FourEndpoints(t *testing.T) {
	testredis.Start(t)
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.URL.RawQuery
		mu.Unlock()
		if r.URL.Path == epToken.Path {
			_, _ = w.Write([]byte(`{"access_token":"TK1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","openpid":"OP1","vaild":true,"unionid":"UN1","key_info_list":[]}`))
	}))
	t.Cleanup(srv.Close)
	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL

	if _, err := s.GetPluginOpenPID(context.Background(), "pc1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckEncryptedData(context.Background(), "hash1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPaidUnionID(context.Background(), "o1", "TX1", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUserEncryptKey(context.Background(), "o1", "sig1", "hmac_sha256"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	for path, wantPart := range map[string]string{
		"/wxa/getpluginopenpid":           "access_token=TK1",
		"/wxa/business/checkencryptedmsg": "access_token=TK1",
		"/wxa/getpaidunionid":             "openid=o1&transaction_id=TX1",
		"/wxa/business/getuserencryptkey": "openid=o1&signature=sig1&sig_method=hmac_sha256",
	} {
		if _, ok := seen[path]; !ok {
			t.Fatalf("未命中 %s", path)
		}
		for _, part := range strings.Split(wantPart, "&") {
			if !strings.Contains(seen[path], part) {
				t.Fatalf("%s query 缺 %s: %s", path, part, seen[path])
			}
		}
	}
	// GetPaidUnionID 空参数不入 query（子集语义）
	if strings.Contains(seen["/wxa/getpaidunionid"], "mch_id") || strings.Contains(seen["/wxa/getpaidunionid"], "out_trade_no") {
		t.Fatalf("空订单参数不应入 query: %s", seen["/wxa/getpaidunionid"])
	}
}

// TestQrcodeLink_JSONSix scheme/urllink/shortlink 六件 JSON 端点路径断言
func TestQrcodeLink_JSONSix(t *testing.T) {
	testredis.Start(t)
	var mu sync.Mutex
	seen := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = true
		mu.Unlock()
		if r.URL.Path == epToken.Path {
			_, _ = w.Write([]byte(`{"access_token":"TK1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok","openlink":"weixin://"}`))
	}))
	t.Cleanup(srv.Close)
	s := NewWxAppService("wx123", "sec")
	s.BaseURL = srv.URL

	calls := []struct {
		path string
		fn   func() (map[string]any, error)
	}{
		{"/wxa/queryscheme", func() (map[string]any, error) {
			return s.QueryScheme(context.Background(), map[string]any{"scheme": "x"})
		}},
		{"/wxa/generatescheme", func() (map[string]any, error) { return s.GenerateScheme(context.Background(), map[string]any{}) }},
		{"/wxa/generatenfcscheme", func() (map[string]any, error) {
			return s.GenerateNFCScheme(context.Background(), map[string]any{"model_id": "M1"})
		}},
		{"/wxa/generate_urllink", func() (map[string]any, error) { return s.GenerateURLLink(context.Background(), map[string]any{}) }},
		{"/wxa/query_urllink", func() (map[string]any, error) {
			return s.QueryURLLink(context.Background(), map[string]any{"url_link": "x"})
		}},
		{"/wxa/genwxashortlink", func() (map[string]any, error) {
			return s.GenerateShortLink(context.Background(), map[string]any{"page_url": "pages/i?a=1"})
		}},
	}
	for _, c := range calls {
		if _, err := c.fn(); err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, c := range calls {
		if !seen[c.path] {
			t.Fatalf("未命中 %s", c.path)
		}
	}
	// 表项计数防漂移：249 = 前 246 + 微信客服绑定族 3（wxapp 按范围裁决收官）
	if len(allEndpoints) != 249 {
		t.Fatalf("allEndpoints 应为 249: %d", len(allEndpoints))
	}
}

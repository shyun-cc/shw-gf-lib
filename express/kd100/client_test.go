// Package kd100 测试：双签名固定向量 + 重试计数 + 回调验签。
package kd100

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 已知向量
const (
	vKey      = "testKey123"
	vSecret   = "testSecret456"
	vCustomer = "testCustomer789"
)

// indepMD5upper 测试内独立重算 UPPER(MD5(...))
func indepMD5upper(s string) string {
	h := md5.Sum([]byte(s))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

// TestSignPoll_FixedVector 查询类签名固定向量
func TestSignPoll_FixedVector(t *testing.T) {
	paramJSON := `{"com":"jd","num":"JD0001","resultv2":"4"}`
	c := New(vKey, vSecret, vCustomer)
	want := indepMD5upper(paramJSON + vKey + vCustomer)
	if got := c.signPoll(paramJSON); got != want {
		t.Fatalf("查询签名不符:\n got=%s\nwant=%s", got, want)
	}
}

// TestSignOrder_FixedVector 下单类签名固定向量
func TestSignOrder_FixedVector(t *testing.T) {
	paramJSON := `{"orderId":"O1","kuaidicom":"jd"}`
	tt := "1723963200"
	c := New(vKey, vSecret, vCustomer)
	want := indepMD5upper(paramJSON + tt + vKey + vSecret)
	if got := c.signOrder(paramJSON, tt); got != want {
		t.Fatalf("下单签名不符:\n got=%s\nwant=%s", got, want)
	}
}

// retryServer 可编程响应 server：bodies 依次返回（非 JSON 则触发重试），计数请求次数
func retryServer(t *testing.T, count *int32, bodies ...string) *httptest.Server {
	t.Helper()
	var idx int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(count, 1)
		i := int(atomic.AddInt32(&idx, 1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(bodies[i]))
		_ = n
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fastClient(base string) *Client {
	c := New(vKey, vSecret, vCustomer)
	c.BaseURL = base
	c.Sleep = func(time.Duration) {} // 重试不真睡
	return c
}

// TestRetry_NonJSONRetriesExactly3 非 JSON 响应重试至多 3 次后报错
func TestRetry_NonJSONRetriesExactly3(t *testing.T) {
	var count int32
	srv := retryServer(t, &count, "not json at all", "still not json", "<html>err</html>")

	_, err := fastClient(srv.URL).Track(context.Background(), "jd", "JD0001")
	if err == nil {
		t.Fatal("3 次非 JSON 应报错")
	}
	if count != 3 {
		t.Fatalf("应恰好请求 3 次，实际 %d", count)
	}
}

// TestRetry_ResultFalseNoRetry 合法 JSON 但 result=false：不重试、单次返回
func TestRetry_ResultFalseNoRetry(t *testing.T) {
	var count int32
	srv := retryServer(t, &count, `{"result":false,"message":"业务失败"}`)

	payload, err := fastClient(srv.URL).Track(context.Background(), "jd", "JD0001")
	if err != nil {
		t.Fatalf("业务失败不应报错: %v", err)
	}
	if count != 1 {
		t.Fatalf("result=false 不应重试，请求数 %d", count)
	}
	if payload["result"] != false || payload["message"] != "业务失败" {
		t.Fatalf("响应体应原样返回: %v", payload)
	}
}

// TestFormFields_BothChains 双链表单字段与签名（捕获断言）
func TestFormFields_BothChains(t *testing.T) {
	var lastForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastForm, _ = url.ParseQuery(string(body))
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("应为表单 Content-Type: %s", r.Header.Get("Content-Type"))
		}
		w.Write([]byte(`{"result":true}`))
	}))
	defer srv.Close()

	c := fastClient(srv.URL)
	c.Now = func() time.Time { return time.Unix(1723963200, 0) }

	if _, err := c.Track(context.Background(), "jd", "JD0001"); err != nil {
		t.Fatal(err)
	}
	if lastForm.Get("customer") != vCustomer || lastForm.Get("param") == "" {
		t.Fatalf("查询链表单字段不符: %v", lastForm)
	}
	wantPoll := indepMD5upper(lastForm.Get("param") + vKey + vCustomer)
	if lastForm.Get("sign") != wantPoll {
		t.Fatalf("查询链签名不符: %s vs %s", lastForm.Get("sign"), wantPoll)
	}

	if _, err := c.CreateOrder(context.Background(), map[string]any{"orderId": "O1"}); err != nil {
		t.Fatal(err)
	}
	if lastForm.Get("key") != vKey || lastForm.Get("t") != "1723963200" {
		t.Fatalf("下单链表单字段不符: %v", lastForm)
	}
	wantOrder := indepMD5upper(lastForm.Get("param") + "1723963200" + vKey + vSecret)
	if lastForm.Get("sign") != wantOrder {
		t.Fatalf("下单链签名不符: %s vs %s", lastForm.Get("sign"), wantOrder)
	}
}

// TestCallbackVerify 回调验签：合法通过、篡改 param/sign 拒绝
func TestCallbackVerify(t *testing.T) {
	c := New(vKey, vSecret, vCustomer)

	param := `{"lastResult":{"status":"signed"}}`
	goodSign := indepMD5upper(param + vKey + vCustomer)
	if err := c.HandleTrackingCallback(param, goodSign); err != nil {
		t.Fatalf("合法签名应通过: %v", err)
	}

	badSign := strings.ToLower(goodSign[:31]) + "X"
	if err := c.HandleTrackingCallback(param, badSign); err == nil {
		t.Fatal("篡改签名应拒绝")
	}

	tamperedParam := `{"lastResult":{"status":"tampered"}}`
	if err := c.HandleTrackingCallback(tamperedParam, goodSign); err == nil {
		t.Fatal("篡改 param 应拒绝")
	}

	// 下单类回调
	tt := "1723963200"
	goodOrder := indepMD5upper(param + tt + vKey + vSecret)
	if err := c.HandleOrderCallback(param, goodOrder, tt); err != nil {
		t.Fatalf("下单回调合法签名应通过: %v", err)
	}
	if err := c.HandleOrderCallback(param, goodOrder, "9999999999"); err == nil {
		t.Fatal("下单回调时间戳篡改应拒绝")
	}
	_ = fmt.Sprint
}

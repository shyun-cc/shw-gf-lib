package tencent

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSign_FixedVector 固定向量：签名用原始未编码值、md5 小写
func TestSign_FixedVector(t *testing.T) {
	c := New("mykey", "mysk")
	query := map[string]string{"key": "mykey", "id": "110105"}
	// 独立重算：参数升序（key<id? k-i 字典序 key 在 id 前），原始值拼串
	raw := "/ws/district/v1/getchildren?id=110105&key=mykey" + "mysk"
	h := md5.Sum([]byte(raw))
	want := hex.EncodeToString(h[:])
	if got := c.sign("/ws/district/v1/getchildren", query); got != want {
		t.Fatalf("签名不符:\n got=%s\nwant=%s", got, want)
	}
}

// TestRequest_EncodedWithSig 请求编码发送 + sig 追加（两步分离的发送侧）
func TestRequest_EncodedWithSig(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"status":0,"result":[[{"id":"110105"}]]}`))
	}))
	defer srv.Close()

	c := New("mykey", "mysk")
	c.BaseURL = srv.URL
	if _, err := c.Get(context.Background(), "/ws/district/v1/getchildren", map[string]string{"id": "110105"}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(gotQuery, "sig=") {
		t.Fatalf("sig 应追加到 query: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "key=mykey") || !strings.Contains(gotQuery, "id=110105") {
		t.Fatalf("参数应编码发送: %s", gotQuery)
	}
}

// TestRetry_StatusNonZero status≠0 重试恰 3 次后报错；status=0 首次成功不重试
func TestRetry_StatusNonZero(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&count, 1)
		if n <= 3 {
			_, _ = w.Write([]byte(`{"status":110,"message":"请求来源未被授权"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":0}`))
	}))
	defer srv.Close()

	c := New("k", "")
	c.BaseURL = srv.URL
	c.Sleep = func(time.Duration) {}
	if _, err := c.Get(context.Background(), "/x", nil); err == nil {
		t.Fatal("3 次 status=110 应报错")
	}
	if count != 3 {
		t.Fatalf("应恰请求 3 次，实际 %d", count)
	}

	// 成功即返回：第一次就 status=0
	var count2 int32
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count2, 1)
		_, _ = w.Write([]byte(`{"status":0}`))
	}))
	defer srv2.Close()
	c2 := New("k", "")
	c2.BaseURL = srv2.URL
	if _, err := c2.Get(context.Background(), "/x", nil); err != nil {
		t.Fatal(err)
	}
	if count2 != 1 {
		t.Fatalf("成功不应重试，请求数 %d", count2)
	}
	_ = fmt.Sprint
}

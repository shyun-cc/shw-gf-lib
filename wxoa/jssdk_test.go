package wxoa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
	"github.com/shyun-cc/shw-gf-lib/redislock"
)

func TestJSAPITicketCacheConcurrencyExpiryAndAppIsolation(t *testing.T) {
	mr := testredis.Start(t)
	var tokens, tickets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == epToken.Path {
			tokens.Add(1)
			fmt.Fprint(w, `{"access_token":"TOKEN"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != epJSAPITicket.Path || r.URL.Query().Get("access_token") != "TOKEN" || r.URL.Query().Get("type") != "jsapi" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		n := tickets.Add(1)
		time.Sleep(80 * time.Millisecond)
		fmt.Fprintf(w, `{"errcode":0,"ticket":"TICKET_%d","expires_in":7200}`, n)
	}))
	defer srv.Close()
	s := NewWxOAService("wx-a", "SECRET")
	s.BaseURL = srv.URL
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticket, err := s.GetJSAPITicket(context.Background())
			if err != nil || ticket != "TICKET_1" {
				t.Errorf("ticket=%q err=%v", ticket, err)
			}
		}()
	}
	wg.Wait()
	if tokens.Load() != 1 || tickets.Load() != 1 {
		t.Fatalf("token=%d ticket=%d", tokens.Load(), tickets.Load())
	}
	if ttl := mr.TTL(jsapiTicketKeyPrefix + s.AppID); ttl != 7000*time.Second {
		t.Fatalf("TTL=%v", ttl)
	}
	if _, err := s.GetJSAPITicket(context.Background()); err != nil || tickets.Load() != 1 {
		t.Fatalf("cache miss: %v", err)
	}
	mr.FastForward(7001 * time.Second)
	if ticket, err := s.GetJSAPITicket(context.Background()); err != nil || ticket != "TICKET_2" {
		t.Fatalf("refresh=%q %v", ticket, err)
	}
	other := NewWxOAService("wx-b", "SECRET")
	other.BaseURL = srv.URL
	if ticket, err := other.GetJSAPITicket(context.Background()); err != nil || ticket != "TICKET_3" {
		t.Fatalf("app isolation=%q %v", ticket, err)
	}
}

func TestJSAPITicketErrorsAndShortTTL(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		biz        int
		ttl        time.Duration
	}{
		{name: "biz", body: `{"errcode":40014,"errmsg":"bad TOKEN SECRET"}`, biz: 40014},
		{name: "invalid json", body: `TOKEN SECRET`},
		{name: "missing ticket", body: `{"expires_in":7200}`},
		{name: "missing expiry", body: `{"ticket":"TICKET"}`},
		{name: "negative expiry", body: `{"ticket":"TICKET","expires_in":-1}`},
		{name: "http failure", body: `{"ticket":"TICKET","expires_in":7200}`, status: 503},
		{name: "short ttl", body: `{"ticket":"TICKET","expires_in":10}`, ttl: 9 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr := testredis.Start(t)
			mr.Set(accessTokenKeyPrefix+"wx", "TOKEN")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			s := NewWxOAService("wx", "SECRET")
			s.BaseURL = srv.URL
			ticket, err := s.GetJSAPITicket(context.Background())
			if tc.ttl > 0 {
				if err != nil || ticket != "TICKET" || mr.TTL(jsapiTicketKeyPrefix+"wx") != tc.ttl {
					t.Fatalf("short expiry %q %v", ticket, err)
				}
				return
			}
			if err == nil || mr.Exists(jsapiTicketKeyPrefix+"wx") {
				t.Fatal("failure cached or no error")
			}
			if strings.Contains(err.Error(), "TOKEN") || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("credential leaked: %v", err)
			}
			var biz *BizError
			var infra *InfraError
			if tc.biz != 0 {
				if !errors.As(err, &biz) || biz.Errcode != tc.biz {
					t.Fatalf("biz error: %v", err)
				}
			} else if !errors.As(err, &infra) {
				t.Fatalf("infra error: %v", err)
			}
		})
	}
}

func TestJSAPITicketCancellationAndRedisFailure(t *testing.T) {
	mr := testredis.Start(t)
	s := NewWxOAService("wx", "SECRET")
	lock := redislock.Create("wxoa:jsapi_ticket:wx", 10)
	if !lock.Get(context.Background()) {
		t.Fatal("lock")
	}
	defer lock.Release(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	if _, err := s.GetJSAPITicket(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	mr.SetError("cache failure")
	defer mr.SetError("")
	if _, err := s.GetJSAPITicket(context.Background()); err == nil {
		t.Fatal("Redis failure must not bypass lock")
	}
}

type jsFailTransport struct{}

func (jsFailTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("transport failed %s", r.URL)
}

func TestJSAPITicketTransportCredentialProtection(t *testing.T) {
	mr := testredis.Start(t)
	mr.Set(accessTokenKeyPrefix+"wx", "TOKEN")
	s := NewWxOAService("wx", "SECRET")
	s.HTTPClient = &http.Client{Transport: jsFailTransport{}}
	_, err := s.GetJSAPITicket(context.Background())
	if err == nil || strings.Contains(fmt.Sprintf("%+v", err), "TOKEN") || strings.Contains(fmt.Sprint(errors.Unwrap(err)), "TOKEN") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSignJSURLGolden(t *testing.T) {
	ticket := "sM4AOVdWfPE4DxkXGEs8VMCPGGVi4C3VM0P37wVUCFvkVAy_90u5h9nbSlYy3-Sl-HhTdfl2fzFy1AOcHKP7qg"
	got := signJSURL(ticket, "Wm3WZYTPz0wzccnW", 1414587457, "http://mp.weixin.qq.com?params=value")
	if got != "0f9de62fce790f9a083d5c99e95740ceb90c27ed" {
		t.Fatalf("golden signature=%s", got)
	}
}

func TestSignJSURLPreservesRawURLAndSafeJSON(t *testing.T) {
	mr := testredis.Start(t)
	mr.Set(jsapiTicketKeyPrefix+"wx", "TICKET")
	s := NewWxOAService("wx", "SECRET")
	raw := "https://Example.com/p%2f?a=2&a=1&z=%2f&b=+&empty=#/route?ignored=1"
	config, err := s.SignJSURL(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	page, _, _ := strings.Cut(raw, "#")
	if config.Signature != signJSURL("TICKET", config.NonceStr, config.Timestamp, page) || len(config.NonceStr) != 32 || config.AppID != "wx" || config.Timestamp <= 0 {
		t.Fatalf("config=%+v", config)
	}
	parsed, _ := url.Parse(page)
	parsed.RawQuery = parsed.Query().Encode()
	if config.Signature == signJSURL("TICKET", config.NonceStr, config.Timestamp, parsed.String()) {
		t.Fatal("signature must preserve raw encoding and order")
	}
	data, _ := json.Marshal(config)
	var keys map[string]any
	json.Unmarshal(data, &keys)
	if len(keys) != 4 || keys["appId"] != "wx" || keys["nonceStr"] == nil || keys["timestamp"] == nil || keys["signature"] == nil {
		t.Fatalf("JSON=%s", data)
	}
	if strings.Contains(string(data), "TICKET") || strings.Contains(string(data), "SECRET") {
		t.Fatalf("credentials in JSON: %s", data)
	}
	for _, raw := range []string{"", "/relative", "ftp://example.com", "https://u:p@example.com", "https://x/\n", "https://x/%zz"} {
		if _, err := s.SignJSURL(context.Background(), raw); err == nil {
			t.Errorf("accepted invalid URL %q", raw)
		}
	}
}

func ExampleWxOAService_SignJSURL() {
	s := NewWxOAService("APP_ID", "APP_SECRET")
	config, err := s.SignJSURL(context.Background(), "https://example.com/page?b=2&a=%2F")
	if err != nil {
		return
	}
	// 将 config 序列化到业务响应；不要返回 access_token、secret 或 ticket。
	_, _ = json.Marshal(config)
}

func TestJSAPITicketCacheWriteFailureReturnsCurrentTicket(t *testing.T) {
	mr := testredis.Start(t)
	mr.Set(accessTokenKeyPrefix+"wx", "TOKEN")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr.SetError("write unavailable")
		fmt.Fprint(w, `{"ticket":"TICKET","expires_in":7200}`)
	}))
	defer srv.Close()
	defer mr.SetError("")
	s := NewWxOAService("wx", "SECRET")
	s.BaseURL = srv.URL
	ticket, err := s.GetJSAPITicket(context.Background())
	if err != nil || ticket != "TICKET" {
		t.Fatalf("current ticket lost: %q %v", ticket, err)
	}
}

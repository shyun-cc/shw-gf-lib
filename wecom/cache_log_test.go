package wecom

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

func TestCredentialCacheWriteFailureLogsNoCredentials(t *testing.T) {
	for _, kind := range []string{"token", "jsapi_ticket"} {
		t.Run(kind, func(t *testing.T) {
			mr := testredis.Start(t)
			output := testredis.CaptureLog(t)
			const (
				secret = "fake-wecom-cache-log-secret"
				token  = "fake-wecom-cache-log-access-token"
				ticket = "fake-wecom-cache-log-jsapi-ticket"
			)
			creds := Credential{CorpID: "fake-cache-log-corp", AgentID: 137, Secret: secret, AppType: AppAgent}
			tokenManager := NewTokenManager(creds)
			ticketManager := NewTicketManager(creds)
			key, want, path := tokenManager.cacheKey(), token, "/cgi-bin/gettoken"
			if kind == "jsapi_ticket" {
				key, want, path = ticketManager.cacheKey(), ticket, "/cgi-bin/get_jsapi_ticket"
				// TicketManager constructs its own TokenManager; seed its cache so
				// every possible network request stays inside this httptest server.
				if err := mr.Set(tokenManager.cacheKey(), token); err != nil {
					t.Fatal(err)
				}
			}
			rejected := testredis.RejectCacheSET(t, mr, key)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path {
					t.Error("unexpected credential request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if kind == "token" {
					if r.URL.Query().Get("corpsecret") != secret {
						t.Error("token request did not carry the synthetic secret")
					}
					fmt.Fprintf(w, `{"errcode":0,"access_token":%q,"expires_in":7200}`, token)
				} else {
					if r.URL.Query().Get("access_token") != token {
						t.Error("ticket request did not use the cached synthetic token")
					}
					fmt.Fprintf(w, `{"errcode":0,"ticket":%q,"expires_in":7200}`, ticket)
				}
			}))
			t.Cleanup(srv.Close)
			tokenManager.SetBaseURL(srv.URL, srv.Client())
			ticketManager.SetBaseURL(srv.URL, srv.Client())

			var got string
			var err error
			if kind == "token" {
				got, err = tokenManager.Token(context.Background(), false)
			} else {
				got, err = ticketManager.Ticket(context.Background(), false)
			}
			if err != nil || got != want {
				t.Fatalf("cache failure lost current credential: value=%q err=%v", got, err)
			}
			if rejected.Load() == 0 || mr.Exists(key) {
				t.Fatal("expected the credential cache SET to fail")
			}
			testredis.RequireSafeWarning(t, output.String(), "wecom "+kind+" 缓存回写失败", token, ticket, secret, key)
		})
	}
}

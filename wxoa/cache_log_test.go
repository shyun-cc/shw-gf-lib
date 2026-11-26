package wxoa

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shyun-cc/shw-gf-lib/internal/testredis"
)

func TestAccessTokenCacheWriteFailureLogsNoCredentials(t *testing.T) {
	mr := testredis.Start(t)
	output := testredis.CaptureLog(t)
	const (
		appID  = "fake-wxoa-cache-log-app"
		secret = "fake-wxoa-cache-log-secret"
		token  = "fake-wxoa-cache-log-access-token"
	)
	key := accessTokenKeyPrefix + appID
	rejected := testredis.RejectCacheSET(t, mr, key)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != epToken.Path || r.URL.Query().Get("secret") != secret {
			t.Error("unexpected token request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":7200}`, token)
	}))
	t.Cleanup(srv.Close)
	s := NewWxOAService(appID, secret)
	s.BaseURL, s.HTTPClient = srv.URL, srv.Client()

	got, err := s.GetAccessToken(context.Background())
	if err != nil || got != token {
		t.Fatalf("cache failure lost current credential: token=%q err=%v", got, err)
	}
	if rejected.Load() == 0 || mr.Exists(key) {
		t.Fatal("expected the credential cache SET to fail")
	}
	testredis.RequireSafeWarning(t, output.String(), "wxoa access_token 缓存回写失败", token, secret, key)
}

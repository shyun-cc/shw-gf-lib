package sms

import (
	"context"
	"fmt"
	"github.com/shyun-cc/shw-gf-lib/aliyun"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAliyunMetadata(t *testing.T) {
	for _, code := range []string{"OK", "isv.INVALID_PARAMETERS"} {
		t.Run(code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"Code":%q,"Message":"message","RequestId":"request","BizId":"biz"}`, code)
			}))
			defer srv.Close()
			client := aliyun.NewClient(aliyun.Credentials{AccessKeyID: "test", AccessKeySecret: "test"})
			client.BaseURL = srv.URL
			p := NewAliyunProvider(AliyunConfig{Client: client, Templates: map[string]string{"login_code": "SMS_1"}})
			got, err := p.Send(context.Background(), Message{Scene: SceneLoginCode})
			if err != nil || got.RequestID != "request" || got.ErrorCode != code || got.MessageID != "biz" || got.Success != (code == "OK") {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

package aliyun

import (
	"context"
	"testing"
)

func TestGetSmsTemplate(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 200, `{"Code":"OK","RequestId":"r1","TemplateCode":"SMS_1","TemplateContent":"hello ${code}","TemplateStatus":"10","RelatedSignName":"sign","VariableAttribute":"{\"code\":\"number\"}","AuditInfo":{"RejectInfo":"reason","AuditDate":"date"}}`)
	got, err := NewSms(fixedClient(srv.URL)).GetSmsTemplate(context.Background(), "SMS_1")
	if err != nil || !got.Success || got.RequestID != "r1" || got.TemplateStatus != "10" || got.AuditInfo.RejectInfo != "reason" || got.VariableAttribute != `{"code":"number"}` {
		t.Fatalf("%+v %v", got, err)
	}
	if cap.method != "POST" || cap.body != "" || cap.header.Get("X-Acs-Action") != "GetSmsTemplate" || cap.header.Get("X-Acs-Version") != "2017-05-25" || cap.query.Get("TemplateCode") != "SMS_1" {
		t.Fatalf("%+v", cap)
	}
}
func TestGetSmsTemplateRejected(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 404, `{"Code":"TemplateNotFound","Message":"missing","RequestId":"r2"}`)
	got, err := NewSms(fixedClient(srv.URL)).GetSmsTemplate(context.Background(), "missing")
	if err != nil || got.Success || got.ErrorCode != "TemplateNotFound" || got.RequestID != "r2" {
		t.Fatalf("%+v %v", got, err)
	}
}

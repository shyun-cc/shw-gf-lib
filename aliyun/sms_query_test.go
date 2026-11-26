// QuerySendDetails 单测：请求形态 / bizID 空不携带 / 正常解析 / 空态 / 厂商拒绝
// （沿用 client_test.go 的 captureServer/fixedClient 形态）。
package aliyun

import (
	"context"
	"testing"
)

// TestQuerySendDetails_RequestShape 请求形态：POST、body 空、Action/Version 头、
// query 业务参数齐备（PhoneNumber 单数、BizId 携带）
func TestQuerySendDetails_RequestShape(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 200, `{"Code":"OK","RequestId":"r1","TotalCount":0}`)

	sms := NewSms(fixedClient(srv.URL))
	if _, err := sms.QuerySendDetails(context.Background(), "10800000000", "20260901", "b1", 10, 1); err != nil {
		t.Fatalf("QuerySendDetails 失败: %v", err)
	}
	if cap == nil {
		t.Fatal("未捕获请求")
	}
	if cap.method != "POST" {
		t.Fatalf("应 POST，实际 %s", cap.method)
	}
	if cap.body != "" {
		t.Fatalf("Sms 链 body 应为空，实际 %q", cap.body)
	}
	if act := cap.header.Get("X-Acs-Action"); act != "QuerySendDetails" {
		t.Fatalf("x-acs-action 应为 QuerySendDetails，实际 %s", act)
	}
	if ver := cap.header.Get("X-Acs-Version"); ver != "2017-05-25" {
		t.Fatalf("x-acs-version 应为 2017-05-25，实际 %s", ver)
	}
	for k, want := range map[string]string{
		"PhoneNumber": "10800000000", // 单数，与 SendSms 的 PhoneNumbers 不同
		"SendDate":    "20260901",
		"PageSize":    "10",
		"CurrentPage": "1",
		"BizId":       "b1",
	} {
		if got := cap.query.Get(k); got != want {
			t.Fatalf("query.%s 应为 %s，实际 %s（全量 %v）", k, want, got, cap.query)
		}
	}
}

// TestQuerySendDetails_NoBizId bizID 空时不携带 BizId，其余必填参数齐备
func TestQuerySendDetails_NoBizId(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 200, `{"Code":"OK","TotalCount":0}`)

	sms := NewSms(fixedClient(srv.URL))
	if _, err := sms.QuerySendDetails(context.Background(), "10800000000", "20260901", "", 50, 1); err != nil {
		t.Fatalf("QuerySendDetails 失败: %v", err)
	}
	if _, ok := cap.query["BizId"]; ok {
		t.Fatalf("bizID 空不应携带 BizId: %v", cap.query)
	}
	for k, want := range map[string]string{
		"PhoneNumber": "10800000000",
		"SendDate":    "20260901",
		"PageSize":    "50",
		"CurrentPage": "1",
	} {
		if got := cap.query.Get(k); got != want {
			t.Fatalf("query.%s 应为 %s，实际 %s", k, want, got)
		}
	}
}

// TestQuerySendDetails_ParseDetails 正常解析：TotalCount string 形态、逐字段映射、
// SendStatus 与三常量比对
func TestQuerySendDetails_ParseDetails(t *testing.T) {
	if SmsStatusWaiting != 1 || SmsStatusFailed != 2 || SmsStatusSuccess != 3 {
		t.Fatalf("SendStatus 常量值不符: %d/%d/%d", SmsStatusWaiting, SmsStatusFailed, SmsStatusSuccess)
	}
	reply := `{"RequestId":"r1","Code":"OK","TotalCount":"2","SmsSendDetailDTOs":{"SmsSendDetailDTO":[` +
		`{"PhoneNum":"10800000000","SendDate":"2026-09-01 12:00:00","ReceiveDate":"2026-09-01 12:00:05","SendStatus":3,"ErrCode":"DELIVERED","TemplateCode":"SMS_001","Content":"【测试】您的验证码1234，5分钟内有效"},` +
		`{"PhoneNum":"10800000000","SendDate":"2026-09-01 12:01:00","SendStatus":1,"Content":"【测试】您的验证码5678，5分钟内有效"}]}}`
	var cap *captured
	srv := captureServer(t, &cap, 200, reply)

	sms := NewSms(fixedClient(srv.URL))
	res, err := sms.QuerySendDetails(context.Background(), "10800000000", "20260901", "b1", 10, 1)
	if err != nil {
		t.Fatalf("QuerySendDetails 失败: %v", err)
	}
	if !res.Success || res.TotalCount != 2 || res.RequestID != "r1" {
		t.Fatalf("应成功且 TotalCount=2、RequestID=r1: %+v", res)
	}
	if len(res.Details) != 2 {
		t.Fatalf("Details 应长 2，实际 %d", len(res.Details))
	}
	want := QuerySendDetail{
		Phone:        "10800000000",
		SendDate:     "2026-09-01 12:00:00",
		ReceiveDate:  "2026-09-01 12:00:05",
		SendStatus:   SmsStatusSuccess,
		ErrCode:      "DELIVERED",
		TemplateCode: "SMS_001",
		Content:      "【测试】您的验证码1234，5分钟内有效",
	}
	if got := res.Details[0]; got != want {
		t.Fatalf("明细字段映射不符:\n got=%+v\nwant=%+v", got, want)
	}
	if got := res.Details[1]; got.SendStatus != SmsStatusWaiting || got.ReceiveDate != "" || got.ErrCode != "" || got.TemplateCode != "" {
		t.Fatalf("第二条应为等待回执且缺失字段零值: %+v", got)
	}
}

// TestQuerySendDetails_EmptyResult 空态：TotalCount number 形态、DTOs 缺失或
// null 时 Details 为空切片（非 nil）
func TestQuerySendDetails_EmptyResult(t *testing.T) {
	cases := map[string]string{
		"DTOs 缺失":   `{"Code":"OK","TotalCount":0}`,
		"DTOs null": `{"Code":"OK","TotalCount":0,"SmsSendDetailDTOs":null}`,
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			var cap *captured
			srv := captureServer(t, &cap, 200, reply)

			sms := NewSms(fixedClient(srv.URL))
			res, err := sms.QuerySendDetails(context.Background(), "10800000000", "20260901", "b1", 10, 1)
			if err != nil {
				t.Fatalf("QuerySendDetails 失败: %v", err)
			}
			if !res.Success || res.TotalCount != 0 {
				t.Fatalf("应成功且 TotalCount=0: %+v", res)
			}
			if res.Details == nil {
				t.Fatal("DTOs 缺失/null 时 Details 应为空切片非 nil")
			}
			if len(res.Details) != 0 {
				t.Fatalf("Details 应为空，实际 %d", len(res.Details))
			}
		})
	}
}

// TestQuerySendDetails_VendorReject 厂商拒绝（400 + 业务错误码）：不返回 Go error，
// Success=false 且 ErrorCode/ErrorMessage 带回
func TestQuerySendDetails_VendorReject(t *testing.T) {
	var cap *captured
	srv := captureServer(t, &cap, 400, `{"Code":"isv.SMS_SIGNATURE_ILLEGAL","Message":"签名不合法"}`)

	sms := NewSms(fixedClient(srv.URL))
	res, err := sms.QuerySendDetails(context.Background(), "10800000000", "20260901", "b1", 10, 1)
	if err != nil {
		t.Fatalf("厂商拒绝不应返回 Go error: %v", err)
	}
	if res == nil || res.Success {
		t.Fatalf("应返回 Success=false 的 Result: %+v", res)
	}
	if res.ErrorCode != "isv.SMS_SIGNATURE_ILLEGAL" || res.ErrorMessage != "签名不合法" {
		t.Fatalf("错误信息不符: %+v", res)
	}
	if res.Details == nil {
		t.Fatal("拒绝时 Details 也应为空切片非 nil")
	}
}

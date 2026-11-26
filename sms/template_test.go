package sms

import (
	"reflect"
	"testing"
)

func TestTemplateMapping(t *testing.T) {
	if got := Placeholders("${z} ${code} ${code}"); !reflect.DeepEqual(got, []string{"code", "z"}) {
		t.Fatal(got)
	}
	got, err := ResolveParams("验证码 ${code}", map[string]string{"code": "verifyCode"}, []string{"verifyCode"}, map[string]any{"verifyCode": "123456"})
	if err != nil || got["code"] != "123456" {
		t.Fatalf("%v %v", got, err)
	}
	for _, tc := range []struct {
		name    string
		mapping map[string]string
		allowed []string
		values  map[string]any
	}{
		{"missing mapping", nil, []string{"verifyCode"}, map[string]any{"verifyCode": "123456"}},
		{"extra mapping", map[string]string{"code": "verifyCode", "other": "verifyCode"}, []string{"verifyCode"}, map[string]any{"verifyCode": "123456"}},
		{"undeclared mapping", map[string]string{"code": "secret"}, []string{"verifyCode"}, map[string]any{"secret": "hidden"}},
		{"missing value", map[string]string{"code": "verifyCode"}, []string{"verifyCode"}, nil},
		{"undeclared value", map[string]string{"code": "verifyCode"}, []string{"verifyCode"}, map[string]any{"verifyCode": "123456", "secret": "hidden"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ResolveParams("${code}", tc.mapping, tc.allowed, tc.values); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if got, err := ResolveParams("无变量", nil, nil, nil); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

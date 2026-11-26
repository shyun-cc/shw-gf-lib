package storage

import (
	"testing"
	"time"
)

var fixedTime = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

// TestUriEncode 编码规则
func TestUriEncode(t *testing.T) {
	if got := uriEncode("a/b c~d"); got != "a/b%20c~d" {
		t.Fatalf("uriEncode 不符: %q", got)
	}
}

// TestDeriveRegion 端点推导签名 region（表驱动：标准形态逐类 + 兜底路径）
func TestDeriveRegion(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"oss 标准", "https://oss-cn-hangzhou.aliyuncs.com", "cn-hangzhou"},
		{"oss 内网", "https://oss-cn-hangzhou-internal.aliyuncs.com", "cn-hangzhou"},
		{"oss 无协议+尾斜杠", "oss-cn-shenzhen.aliyuncs.com/", "cn-shenzhen"},
		{"s3 点分形态", "s3.us-east-2.amazonaws.com", "us-east-2"},
		{"s3 横线形态", "s3-ap-southeast-1.amazonaws.com", "ap-southeast-1"},
		{"s3 中国区分域", "s3.cn-north-1.amazonaws.com.cn", "cn-north-1"},
		{"cos 腾讯云", "cos.ap-guangzhou.myqcloud.com", "ap-guangzhou"},
		{"s3 全局端点", "s3.amazonaws.com", "us-east-1"},
		{"IP+端口", "http://127.0.0.1:9000", "us-east-1"},
		{"未知域名", "minio.example.com", "us-east-1"},
		{"空串", "", "us-east-1"},
	}
	for _, c := range cases {
		if got := deriveRegion(c.endpoint); got != c.want {
			t.Errorf("%s: deriveRegion(%q) = %q, want %q", c.name, c.endpoint, got, c.want)
		}
	}
}

// TestNormalizeDomain 协议前缀与尾随斜杠剥离
func TestNormalizeDomain(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://oss.example.com/", "oss.example.com"},
		{"http://127.0.0.1:9000", "127.0.0.1:9000"},
		{"minio.example.com", "minio.example.com"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeDomain(c.in); got != c.want {
			t.Errorf("normalizeDomain(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestResolveHost 域名链解析：第一个非空胜出，全空回退默认
func TestResolveHost(t *testing.T) {
	if got := resolveHost([]string{"", "a.internal", "b.internal"}, "pub"); got != "a.internal" {
		t.Errorf("resolveHost 取第一个非空: got %q, want a.internal", got)
	}
	if got := resolveHost(nil, "pub"); got != "pub" {
		t.Errorf("resolveHost 全空回退: got %q, want pub", got)
	}
	if got := resolveHost([]string{"", ""}, "pub"); got != "pub" {
		t.Errorf("resolveHost 全空串回退: got %q, want pub", got)
	}
}

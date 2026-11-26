package rabbitmq

// rabbitmq_test.go：Config 默认值补齐 / URL 拼接 / 退避序列。

import (
	"testing"
	"time"
)

// TestConfigWithDefaults 零值字段以默认值补齐，显式值保留
func TestConfigWithDefaults(t *testing.T) {
	got := Config{}.withDefaults()
	want := DefaultConfig()
	if got != want {
		t.Fatalf("零值 Config 应等价 DefaultConfig:\n%+v\n%+v", got, want)
	}

	got = Config{
		Host: "mq.example.com", Port: 5672, User: "app", Password: "secret", Vhost: "prod",
		Heartbeat:            30 * time.Second,
		ReconnectBackoffBase: 2 * time.Second,
		ReconnectBackoffCap:  time.Minute,
	}.withDefaults()
	if got.Host != "mq.example.com" || got.Heartbeat != 30*time.Second ||
		got.ReconnectBackoffBase != 2*time.Second || got.ReconnectBackoffCap != time.Minute {
		t.Fatalf("显式配置不应被默认值覆盖: %+v", got)
	}

	// cap < base 时钳到 base（防呆）
	got = Config{ReconnectBackoffBase: 10 * time.Second, ReconnectBackoffCap: time.Second}.withDefaults()
	if got.ReconnectBackoffCap != got.ReconnectBackoffBase {
		t.Fatalf("cap<base 应钳到 base: %+v", got)
	}
}

// TestConfigURL URL 拼接（凭证与 vhost 转义）
func TestConfigURL(t *testing.T) {
	cases := []struct {
		cfg  Config
		want string
	}{
		{
			Config{Host: "127.0.0.1", Port: 5672, User: "guest", Password: "guest", Vhost: "/"},
			"amqp://guest:guest@127.0.0.1:5672/%2F",
		},
		{
			Config{Host: "mq.example.com", Port: 5672, User: "app", Password: "p@ss/word", Vhost: "prod"},
			"amqp://app:p%40ss%2Fword@mq.example.com:5672/prod",
		},
	}
	for _, tc := range cases {
		if got := tc.cfg.withDefaults().URL(); got != tc.want {
			t.Fatalf("URL 应 %s，实际 %s", tc.want, got)
		}
	}
}

// TestConfigBackoff 退避序列：1s·2^n 封顶 30s
func TestConfigBackoff(t *testing.T) {
	c := Config{ReconnectBackoffBase: time.Second, ReconnectBackoffCap: 30 * time.Second}.withDefaults()
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 30 * time.Second},  // 32s 封顶 30s
		{10, 30 * time.Second}, // 大 attempt 仍封顶（无溢出）
	}
	for _, tc := range cases {
		if got := c.backoff(tc.attempt); got != tc.want {
			t.Errorf("attempt=%d: backoff=%v want %v", tc.attempt, got, tc.want)
		}
	}
}

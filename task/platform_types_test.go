package task

import (
	"strings"
	"testing"
	"time"
)

func platformPolicy(recovery string) TypePolicy {
	return TypePolicy{Lease: time.Minute, Retry: RetryOptions{MaxAttempts: 3, Base: time.Second, Factor: 2, Max: time.Minute}, Recovery: recovery}
}

func TestSettleOutcomes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	retry := Definition{Recovery: RecoveryRetry, Retry: platformPolicy(RecoveryRetry).Retry}
	review := Definition{Recovery: RecoveryReview}
	cases := []struct {
		name     string
		def      Definition
		attempts int
		outcome  string
		status   string
		retry    bool
	}{
		{"success", retry, 1, OutcomeSuccess, StatusSucceeded, false},
		{"partial", retry, 1, OutcomePartial, StatusSucceeded, false},
		{"skipped", retry, 1, OutcomeSkipped, StatusSucceeded, false},
		{"permanent ignores retry policy", retry, 1, OutcomePermanentFailure, StatusAbnormal, false},
		{"unknown ends abnormal", retry, 1, OutcomeUnknown, StatusAbnormal, false},
		{"retryable backs off", retry, 1, OutcomeRetryableFailure, StatusFailed, true},
		{"legacy failed alias", retry, 1, OutcomeFailed, StatusFailed, true},
		{"interrupted retries", retry, 2, OutcomeInterrupted, StatusFailed, true},
		{"exhausted is abnormal", retry, 3, OutcomeRetryableFailure, StatusAbnormal, false},
		{"non idempotent failure ends abnormal", review, 1, OutcomeRetryableFailure, StatusAbnormal, false},
		{"stop on interruption", Definition{Recovery: RecoveryStop}, 1, OutcomeInterrupted, StatusAbnormal, false},
		{"legacy reconcile ends abnormal", Definition{Recovery: RecoveryReconcile}, 1, OutcomeUnknown, StatusAbnormal, false},
	}
	for _, c := range cases {
		status, at, again := settle(c.def, c.attempts, 3, c.outcome, now, 0.5)
		if status != c.status || again != c.retry {
			t.Fatalf("%s: got %s retry=%v", c.name, status, again)
		}
		if again && !at.After(now) {
			t.Fatalf("%s: retry time not in future: %v", c.name, at)
		}
	}
}

func TestTypeConfigValidate(t *testing.T) {
	ok := TypeConfig{HandlerVersion: "v1", Class: DefaultClass, GlobalConcurrency: 4, Policy: platformPolicy(RecoveryRetry)}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(*TypeConfig){
		func(c *TypeConfig) { c.HandlerVersion = "" },
		func(c *TypeConfig) { c.Class = "" },
		func(c *TypeConfig) { c.GlobalConcurrency = 0 },
		func(c *TypeConfig) { c.GlobalConcurrency = 10001 },
		func(c *TypeConfig) { c.RateLimit = 5 },
		func(c *TypeConfig) { c.RateLimit = 5; c.RateWindow = time.Millisecond },
		func(c *TypeConfig) { c.Policy.Recovery = "" },
		func(c *TypeConfig) { c.Policy.Lease = 0 },
	}
	for i, mutate := range bad {
		c := ok
		mutate(&c)
		if c.validate() == nil {
			t.Fatalf("case %d accepted invalid config", i)
		}
	}
	spec := TypeSpec{Type: "sync", HandlerVersion: "v1", Policy: platformPolicy(RecoveryRetry)}.config()
	if spec.Class != DefaultClass || spec.GlobalConcurrency != 1 || spec.SubmitEnabled || spec.ConsumeEnabled {
		t.Fatalf("spec defaults must be closed with single concurrency: %+v", spec)
	}
}

func TestCompletionStatsBounds(t *testing.T) {
	if err := validCompletion(Completion{Outcome: OutcomeSkipped, Stats: map[string]int64{"rows": 3}}); err != nil {
		t.Fatal(err)
	}
	if validCompletion(Completion{Outcome: OutcomeSuccess, Stats: map[string]int64{strings.Repeat("k", 65): 1}}) == nil {
		t.Fatal("long stats key accepted")
	}
	many := map[string]int64{}
	for i := 0; i < 65; i++ {
		many[strings.Repeat("a", i+1)] = 1
	}
	if validCompletion(Completion{Outcome: OutcomeSuccess, Stats: many}) == nil {
		t.Fatal("too many stats accepted")
	}
	if validCompletion(Completion{Outcome: "done"}) == nil {
		t.Fatal("unknown outcome accepted")
	}
}

func TestPlatformSchemaModes(t *testing.T) {
	wall, err := PlatformSchema(TimeWall)
	if err != nil || strings.Contains(wall, "TIMESTAMPTZ") || !strings.Contains(wall, "task_type") {
		t.Fatal(wall, err)
	}
	full, err := Schema(TimeInstant)
	if err != nil || !strings.Contains(full, "task_config_revision") {
		t.Fatal(err)
	}
	conv, err := ConvertTimeColumnsSQL(TimeWall, "Asia/Shanghai")
	if err != nil || !strings.Contains(conv, "task_type ALTER COLUMN registered_at") {
		t.Fatal(conv, err)
	}
	if typeTarget("a") == typeTarget("b") || typeTarget("a") != typeTarget("a") {
		t.Fatal("type target must be stable and distinct")
	}
}

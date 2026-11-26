package task

import (
	"context"
	"errors"
	"testing"
)

func noopHandler(context.Context, *Execution) (Completion, error) { return Completion{}, nil }

type nilTransport struct{}

func (nilTransport) Publish(context.Context, string, DispatchMessage) error { return nil }
func (nilTransport) Consume(context.Context, string, int, func(context.Context, Delivery)) error {
	return nil
}

func TestNormalizeCompletion(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		in        Completion
		err       error
		cancelled bool
		want      string
	}{
		{Completion{}, nil, false, OutcomeSuccess},
		{Completion{}, nil, true, OutcomeInterrupted},
		{Completion{Outcome: OutcomePartial}, nil, true, OutcomePartial},
		{Completion{}, boom, false, OutcomeRetryableFailure},
		{Completion{Outcome: OutcomeSuccess}, boom, true, OutcomeInterrupted},
		{Completion{Outcome: OutcomePermanentFailure}, boom, false, OutcomePermanentFailure},
	}
	for i, c := range cases {
		got := normalize(c.in, c.err, c.cancelled)
		if got.Outcome != c.want {
			t.Fatalf("case %d: %s", i, got.Outcome)
		}
		if c.err != nil && (got.ErrorCode == "" || got.ErrorSummary == "") {
			t.Fatalf("case %d: error detail missing", i)
		}
	}
}

func TestNewRuntimeValidation(t *testing.T) {
	plain := &Store{cfg: StoreConfig{Namespace: "n"}}
	controlled := &Store{cfg: StoreConfig{Namespace: "n", TypeControl: true}}
	ok := map[string]PlatformHandler{"sync": {Version: "v1", Run: noopHandler}, "blocked": {Version: "v1", Run: noopHandler}}
	if _, err := NewRuntime(RuntimeConfig{Store: plain, Handlers: ok, Concurrency: 1}); err == nil {
		t.Fatal("runtime must require TypeControl")
	}
	if _, err := NewRuntime(RuntimeConfig{Store: controlled, Handlers: ok, Concurrency: 1, Transport: nilTransport{}}); err == nil {
		t.Fatal("transport must require Outbox")
	}
	if _, err := NewRuntime(RuntimeConfig{Store: controlled, Handlers: ok}); err == nil {
		t.Fatal("concurrency required")
	}
	if _, err := NewRuntime(RuntimeConfig{Store: controlled, Handlers: ok, Concurrency: 1, Refresh: 6e9}); err == nil {
		t.Fatal("refresh above 5s accepted")
	}
	if _, err := NewRuntime(RuntimeConfig{Store: controlled, Handlers: map[string]PlatformHandler{"x": {Version: "v1"}}, Concurrency: 1}); err == nil {
		t.Fatal("handler without Run accepted")
	}
	r, err := NewRuntime(RuntimeConfig{Store: controlled, Handlers: ok, Allowed: map[string]bool{"sync": true}, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.caps) != 1 || r.caps["sync"] != "v1" {
		t.Fatalf("capabilities must be handlers ∩ allowed: %v", r.caps)
	}
	if !errors.Is(r.ValidatePayload("ghost", nil), ErrTypeUnavailable) {
		t.Fatal("unknown type payload accepted")
	}
	if got := r.classes(); len(got) != 1 || got[0] != DefaultClass {
		t.Fatalf("classes default: %v", got)
	}
	if !r.acquire(context.Background()) || !r.acquire(context.Background()) {
		t.Fatal("pool must admit capacity")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r.acquire(ctx) {
		t.Fatal("pool must not exceed capacity")
	}
}

//go:build integration

package tests

import (
	"testing"
	"time"

	"github.com/shyun-cc/shw-gf-lib/task"
)

func TestPlatformTypeAndDispatchStats(t *testing.T) {
	ctx, s, _, clock := setupPlatform(t, withOutbox)
	openType(t, ctx, s, "sync", 4, nil)
	openType(t, ctx, s, "orphan", 4, func(c *task.TypeConfig) { c.Class = "nobody" })
	instance := platformInstance(t, ctx, s, 10, map[string]string{"sync": "v1"})
	for _, k := range []string{"a", "b", "c"} {
		if _, err := s.Submit(ctx, task.Definition{Type: "sync", Payload: []byte(`{}`)}, k, nil); err != nil {
			t.Fatal(err)
		}
	}
	l, err := s.Claim(ctx, instance, "", "")
	if err != nil || l == nil {
		t.Fatal(l, err)
	}
	if err = s.Complete(ctx, l, task.Completion{Outcome: task.OutcomeUnknown}, nil); err != nil {
		t.Fatal(err)
	}
	if l, err = s.Claim(ctx, instance, "", ""); err != nil || l == nil {
		t.Fatal(l, err)
	}
	views, err := s.TypeStats(ctx)
	if err != nil || len(views) != 2 {
		t.Fatal(views, err)
	}
	var sync task.TypeView
	for _, v := range views {
		if v.Type == "sync" {
			sync = v
		}
	}
	if sync.GlobalConcurrency != 4 || sync.Running != 1 || sync.Ready != 1 || sync.Abnormal != 1 || sync.NeedsReview != 0 || sync.OldestReadyAt == nil {
		t.Fatalf("target vs actual: %+v", sync)
	}
	if _, err = s.Submit(ctx, task.Definition{Type: "orphan", Payload: []byte(`{}`)}, "o", nil); err != nil {
		t.Fatal(err)
	}
	items, err := s.ClaimOutbox(ctx, "relay", 100, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Class == "nobody" {
			if err = s.MarkOutboxFailed(ctx, it, "relay", "task: dispatch_unroutable: class=nobody", time.Unix(0, clock.Load()).Add(time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	d, err := s.DispatchStats(ctx)
	if err != nil || d.Failing != 1 || d.Pending < 1 || d.OldestPendingAt == nil || d.LastError == "" {
		t.Fatalf("%+v %v", d, err)
	}
}

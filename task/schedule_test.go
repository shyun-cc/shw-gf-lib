package task

import (
	"testing"
	"time"
)

func testSchedule() Schedule {
	return Schedule{Name: "hourly", Enabled: true, DefinitionVersion: 1, Definition: Definition{Type: "sync", HandlerVersion: "1", Lease: time.Minute, Retry: RetryOptions{MaxAttempts: 3, Base: time.Second, Factor: 2, Max: time.Hour}, Recovery: RecoveryRetry}, Kind: "interval", Interval: time.Hour, Timezone: "Asia/Shanghai", Anchor: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Misfire: MisfireCoalesce, MaxCatchUp: 2}
}
func TestPersistentSchedulePoints(t *testing.T) {
	s := testSchedule()
	s.NextRunAt = s.Anchor
	now := s.Anchor.Add(3*time.Hour + 20*time.Minute)
	points, next, missed, err := s.Due(now, 100)
	if err != nil || len(points) != 1 || !points[0].Equal(s.Anchor.Add(3*time.Hour)) || !next.Equal(s.Anchor.Add(4*time.Hour)) || missed != 3 {
		t.Fatalf("%v %v %d %v", points, next, missed, err)
	}
	s.Misfire = MisfireSkip
	points, next, missed, err = s.Due(now, 100)
	if err != nil || len(points) != 0 || missed != 4 || !next.After(now) {
		t.Fatal(points, next, missed, err)
	}
	s.Misfire = MisfireCatchUp
	points, next, _, err = s.Due(now, 100)
	if err != nil || len(points) != 2 || !next.Equal(s.Anchor.Add(2*time.Hour)) {
		t.Fatal(points, next, err)
	}
	s.NextRunAt = next
	points, next, _, err = s.Due(now, 1)
	if err != nil || len(points) != 1 || !next.Equal(s.Anchor.Add(3*time.Hour)) {
		t.Fatal(points, next, err)
	}
	// restarting at minute 20/40 does not change fixed hourly anchor.
	for _, m := range []int{20, 40, 59} {
		v, e := s.Next(s.Anchor.Add(time.Duration(m) * time.Minute))
		if e != nil || !v.Equal(s.Anchor.Add(time.Hour)) {
			t.Fatal(v, e)
		}
	}
}
func TestFiveFieldCronWithExplicitTimezone(t *testing.T) {
	s := testSchedule()
	s.Kind = "cron"
	s.Cron = "0 9 * * *"
	now := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	next, err := s.Next(now)
	if err != nil || !next.Equal(time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Fatal(next, err)
	}
	s.Cron = "0 0 9 * * *"
	if _, err = s.Next(now); err == nil {
		t.Fatal("accepted six fields")
	}
	s.Cron = "0 0 31 2 *"
	if _, err = s.Next(now); err == nil {
		t.Fatal("accepted impossible schedule")
	}
}
func TestRetryDelayBound(t *testing.T) {
	r := RetryOptions{MaxAttempts: 3, Base: 5 * time.Second, Factor: 2, Max: time.Hour, Jitter: .2}
	if err := r.validate(); err != nil {
		t.Fatal(err)
	}
	if r.delay(1, 1) != 5*time.Second || r.delay(2, 0) != 8*time.Second || r.delay(10000, 1) != time.Hour {
		t.Fatal("bad retry bound")
	}
}

func TestTimeMigrationOutputIsDeterministic(t *testing.T) {
	want, err := ConvertTimeColumnsSQL(TimeWall, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		got, err := ConvertTimeColumnsSQL(TimeWall, "Asia/Shanghai")
		if err != nil || got != want {
			t.Fatal("non-deterministic migration", err)
		}
	}
}

package task

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

func (s Schedule) validate() error {
	if s.Name == "" || s.DefinitionVersion < 1 || s.Anchor.IsZero() || s.Timezone == "" {
		return fmt.Errorf("task: schedule name/version/anchor/timezone required")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return err
	}
	if err := s.Definition.validate(); err != nil {
		return err
	}
	if s.Misfire != MisfireCoalesce && s.Misfire != MisfireSkip && s.Misfire != MisfireCatchUp {
		return fmt.Errorf("task: invalid misfire policy")
	}
	if s.MaxCatchUp < 1 || s.MaxCatchUp > 100 {
		return fmt.Errorf("task: catch-up bound must be 1..100")
	}
	_, err := s.Next(s.Anchor)
	return err
}

// Next 返回 after 之后的严格下一时点。interval 始终基于 Anchor；cron 为显式时区五段式。
func (s Schedule) Next(after time.Time) (time.Time, error) {
	switch s.Kind {
	case "interval":
		if s.Interval < time.Second {
			return time.Time{}, fmt.Errorf("task: interval must be >= 1s")
		}
		if after.Before(s.Anchor) {
			return s.Anchor, nil
		}
		elapsed := after.Sub(s.Anchor)
		n := elapsed / s.Interval
		next := s.Anchor.Add(n * s.Interval).Add(s.Interval)
		if !next.After(after) {
			return time.Time{}, fmt.Errorf("task: interval overflow")
		}
		return next, nil
	case "cron":
		if len(strings.Fields(s.Cron)) != 5 {
			return time.Time{}, fmt.Errorf("task: cron requires five fields")
		}
		loc, err := time.LoadLocation(s.Timezone)
		if err != nil {
			return time.Time{}, err
		}
		parsed, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(s.Cron)
		if err != nil {
			return time.Time{}, err
		}
		next := parsed.Next(after.In(loc))
		if next.IsZero() {
			return next, fmt.Errorf("task: cron has no next occurrence")
		}
		return next, nil
	default:
		return time.Time{}, fmt.Errorf("task: invalid schedule kind")
	}
}

// Due 计算一个有界扫描批次。catch_up 限额后保留未派发游标；其余模式最多扫描十万个时点，超限返回错误且不推进。
func (s Schedule) Due(now time.Time, budget int) (points []time.Time, next time.Time, missed int64, err error) {
	if err = s.validate(); err != nil {
		return
	}
	next = s.NextRunAt
	if next.IsZero() {
		next = s.Anchor
		if s.Kind == "cron" {
			next, err = s.Next(s.Anchor.Add(-time.Nanosecond))
			if err != nil {
				return
			}
		}
	}
	if next.After(now) || budget <= 0 {
		return
	}
	limit := s.MaxCatchUp
	if limit > budget {
		limit = budget
	}
	if s.Kind == "interval" && s.Misfire != MisfireCatchUp {
		count := int64(now.Sub(next)/s.Interval) + 1
		last := next.Add(time.Duration(count-1) * s.Interval)
		next = last.Add(s.Interval)
		if s.Misfire == MisfireCoalesce {
			points = []time.Time{last}
			missed = count - 1
		} else {
			missed = count
		}
		return
	}
	for i := 0; !next.After(now); i++ {
		if s.Misfire == MisfireCatchUp && len(points) >= limit {
			return
		}
		if i >= 100000 {
			return nil, s.NextRunAt, 0, fmt.Errorf("task: misfire scan bound exceeded; configure explicit anchor")
		}
		current := next
		next, err = s.Next(current)
		if err != nil {
			return nil, s.NextRunAt, 0, err
		}
		switch s.Misfire {
		case MisfireCatchUp:
			points = append(points, current)
		case MisfireCoalesce:
			if len(points) > 0 {
				missed++
			}
			points = []time.Time{current}
		case MisfireSkip:
			missed++
		}
	}
	return
}

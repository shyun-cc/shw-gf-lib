package task

import (
	"context"
	"time"
)

// TypeView 是管理端类型行：配置（目标值）与实时统计（实际值）并列展示。
type TypeView struct {
	TaskType
	Running         int64 // 实际并发（全部实例）
	Ready           int64 // pending 且已到 available_at
	Delayed         int64 // pending 但未到 available_at
	RetryWaiting    int64 // failed 等待退避
	Abnormal        int64 // 异常终态累计数量
	NeedsReview     int64 // Deprecated: 始终为零，使用 Abnormal
	WaitingChildren int64
	OldestReadyAt   *time.Time // 最早就绪运行的 available_at，用于积压告警
}

// TypeStats 返回全部类型的目标配置与实际运行统计。
func (s *Store) TypeStats(ctx context.Context) ([]TypeView, error) {
	types, err := s.Types(ctx)
	if err != nil {
		return nil, err
	}
	now := s.stamp(s.now())
	rows, err := s.cfg.DB.GetAll(ctx, `SELECT type,
 COUNT(*) FILTER (WHERE status='running') AS running,
 COUNT(*) FILTER (WHERE status='pending' AND available_at<=?) AS ready,
 COUNT(*) FILTER (WHERE status='pending' AND available_at>?) AS delayed,
 COUNT(*) FILTER (WHERE status='failed') AS retry,
 COUNT(*) FILTER (WHERE status IN ('abnormal','needs_review')) AS review,
 COUNT(*) FILTER (WHERE status='waiting_children') AS waiting,
 MIN(available_at) FILTER (WHERE status='pending' AND available_at<=?) AS oldest
 FROM task WHERE namespace=? AND status IN ('pending','running','failed','needs_review','abnormal','waiting_children') GROUP BY type`, now, now, now, s.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	stats := map[string]int{}
	for i, row := range rows {
		stats[row["type"].String()] = i
	}
	out := make([]TypeView, 0, len(types))
	for _, t := range types {
		v := TypeView{TaskType: *t}
		if i, ok := stats[t.Type]; ok {
			row := rows[i]
			v.Running, v.Ready, v.Delayed = row["running"].Int64(), row["ready"].Int64(), row["delayed"].Int64()
			v.RetryWaiting, v.Abnormal, v.WaitingChildren = row["retry"].Int64(), row["review"].Int64(), row["waiting"].Int64()
			if at := s.readTime(row["oldest"]); !at.IsZero() {
				v.OldestReadyAt = &at
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// DispatchView 是 Outbox 投递积压与失败统计。
type DispatchView struct {
	Pending         int64      // 待发布通知（含暂停/阻塞/延期中的）
	Failing         int64      // 最近一次发布失败（断连/不可路由/未知）仍待重试的通知
	Leased          int64      // 正在被投递器租约发布中的通知
	OldestPendingAt *time.Time // 最早待发布通知的计划发布时点
	LastError       string     // 最近一条失败原因（已截断，不含消息体）
}

// DispatchStats 返回 namespace 的投递积压与失败情况（未启用 Outbox 时全为零）。
func (s *Store) DispatchStats(ctx context.Context) (*DispatchView, error) {
	v := &DispatchView{}
	if !s.cfg.Outbox {
		return v, nil
	}
	now := s.stamp(s.now())
	row, err := s.cfg.DB.GetOne(ctx, `SELECT COUNT(*) AS pending,
 COUNT(*) FILTER (WHERE last_error<>'') AS failing,
 COUNT(*) FILTER (WHERE lease_until>?) AS leased,
 MIN(available_at) AS oldest
 FROM task_outbox WHERE namespace=? AND status='pending'`, now, s.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	v.Pending, v.Failing, v.Leased = row["pending"].Int64(), row["failing"].Int64(), row["leased"].Int64()
	if at := s.readTime(row["oldest"]); !at.IsZero() {
		v.OldestPendingAt = &at
	}
	last, err := s.cfg.DB.GetOne(ctx, `SELECT last_error FROM task_outbox WHERE namespace=? AND status='pending' AND last_error<>'' ORDER BY available_at DESC LIMIT 1`, s.cfg.Namespace)
	if err != nil {
		return nil, err
	}
	v.LastError = last["last_error"].String()
	return v, nil
}

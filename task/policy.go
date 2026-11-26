package task

import (
	"fmt"
	"time"
)

// ExponentialBackoffRetryPolicy 指数退避：backoff = base · factor^(attempts-1)，封顶 maxBackoff。
// 指数取 attempts-1：首次重试退避恰为 base（对齐 PHP，避免首退避即指数级）。
type ExponentialBackoffRetryPolicy struct {
	Base       time.Duration
	Factor     float64
	MaxBackoff time.Duration
}

// NewDefaultRetryPolicy base=5s factor=2 cap=3600s
func NewDefaultRetryPolicy() *ExponentialBackoffRetryPolicy {
	return &ExponentialBackoffRetryPolicy{
		Base:       5 * time.Second,
		Factor:     2,
		MaxBackoff: 3600 * time.Second,
	}
}

// ShouldRetry 未达上限
func (p *ExponentialBackoffRetryPolicy) ShouldRetry(t *Task) bool {
	return t.Attempts < t.MaxAttempts
}

// NextBackoff 计算退避时长（attempts=1→5s，2→10s，3→20s；封顶 3600s）
func (p *ExponentialBackoffRetryPolicy) NextBackoff(t *Task) time.Duration {
	exponent := t.Attempts - 1
	if exponent < 0 {
		exponent = 0
	}
	backoff := time.Duration(float64(p.Base) * pow(p.Factor, exponent))
	if backoff > p.MaxBackoff {
		backoff = p.MaxBackoff
	}
	return backoff
}

// NextAvailableAt 下次可用时间
func (p *ExponentialBackoffRetryPolicy) NextAvailableAt(t *Task, now time.Time) time.Time {
	return now.Add(p.NextBackoff(t))
}

func pow(base float64, exp int) float64 {
	result := 1.0
	for i := 0; i < exp; i++ {
		result *= base
	}
	return result
}

var _ = fmt.Sprintf // 保留 fmt 供未来错误信息扩展

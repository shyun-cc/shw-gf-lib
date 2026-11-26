package worker

import (
	"context"
	"time"

	"github.com/shyun-cc/shw-gf-lib/events"
	"github.com/shyun-cc/shw-gf-lib/redislock"

	"github.com/gogf/gf/v2/frame/g"
)

// PeriodicWorker 定期循环执行的 Worker：内嵌 BaseWorker 复用单轮保护壳
// （每轮独立 traceId / panic 轮转 / 轮次事件时序），自身只负责周期驱动
// （ticker）与分布式锁策略——WithRedisLock 防止多实例重复执行。
// 锁逻辑经底座钩子注入：beforeRound 获取失败跳过该轮，afterRound 与
// panicRound 统一释放（panic 轮锁不悬挂）。
type PeriodicWorker struct {
	*BaseWorker
	interval time.Duration
	lock     *redislock.Lock // 可选，nil 表示不使用分布式锁

	// Dispatcher 事件分发器注入：轮次事件（RoundStarted/RoundCompleted）经其派发，nil 降级 events.Default()
	Dispatcher *events.Dispatcher
}

// PeriodicOption PeriodicWorker 配置选项
type PeriodicOption func(*PeriodicWorker)

// WithRedisLock 启用 Redis 分布式锁（防止多实例重复执行）
func WithRedisLock(key string, ttl time.Duration) PeriodicOption {
	return func(w *PeriodicWorker) {
		w.lock = redislock.Create(key, int(ttl.Seconds()))
	}
}

// NewPeriodicWorker 创建定时 Worker
func NewPeriodicWorker(name string, interval time.Duration, handle func(ctx context.Context) error, opts ...PeriodicOption) *PeriodicWorker {
	w := &PeriodicWorker{
		BaseWorker: NewBaseWorker(name, handle),
		interval:   interval,
	}
	// 锁策略接进底座钩子：方法值持有 w，执行时才读 w.lock，
	// 故 WithRedisLock 在 opts 中后置设置也不影响接线
	w.beforeRound = w.beforeLockRound
	w.afterRound = w.releaseLock
	// panicRound 钩子签名携带 panic 值（供 ProcWorker 一类上抛策略用）；
	// 本类型仅需释放锁，闭包适配忽略 r，不 re-panic——panic 轮转语义不变
	w.panicRound = func(ctx context.Context, r any) { w.releaseLock(ctx) }
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Run Worker 接口实现：ticker 驱动周期循环，逐轮调底座 RunOnce（保护壳），
// ctx 取消返回 nil。
func (w *PeriodicWorker) Run(ctx context.Context) error {
	// Dispatcher 是本类型导出字段，构造后可被外部赋值；启动时同步进内嵌底座
	// 的非导出派发字段，保证「构造后赋值 Dispatcher 再 Run」的既有用法语义不变
	w.dispatcher = w.Dispatcher

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.RunOnce(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// beforeLockRound 轮前钩子：分布式锁存在且获取失败时跳过该轮
// （Debug 日志、无事件派发、handle 不执行）。
func (w *PeriodicWorker) beforeLockRound(ctx context.Context) bool {
	if w.lock != nil && !w.lock.Get(ctx) {
		g.Log().Debugf(ctx, "Worker %s 未获取到锁，跳过本次执行", w.Name())
		return false
	}
	return true
}

// releaseLock 收尾钩子（正常轮 afterRound 与 panic 轮 panicRound 同实现）：
// 释放分布式锁，保证 panic 轮锁也不悬挂。
func (w *PeriodicWorker) releaseLock(ctx context.Context) {
	if w.lock != nil {
		w.lock.Release(ctx)
	}
}

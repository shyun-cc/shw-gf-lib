// Package worker 后台定时任务框架（对应 PHP @Process 常驻进程）。
//
// 六个角色：
//   - Worker 接口：后台任务契约（Name + Run）；
//   - BaseWorker：裸底座（无间隔循环 + 每轮保护壳），PeriodicWorker /
//     ProcWorker 在其上叠加周期 / 分布式锁等策略；
//   - WorkerManager：多 Worker 生命周期管理（优雅退出 + panic 隔离）；
//   - PeriodicWorker：定时循环执行，支持 WithRedisLock 分布式锁选项，
//     防止多实例重复执行；
//   - ProcWorker：三层进程保护的周期任务（自毁上限/锁异常分层释放/周期抖动错峰）；
//   - RunEnabled：统一注册入口（部署编成唯一启动路径），经环境变量
//     ENABLE_WORKERS 白名单过滤后启动。
//
// 生命周期与轮次事件（五事件）经 events 组件派发，详见 events.go。
package worker

import "context"

// Worker 后台任务接口
type Worker interface {
	Name() string
	Run(ctx context.Context) error
}

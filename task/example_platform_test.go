package task_test

import (
	"context"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/shyun-cc/shw-gf-lib/rabbitmq"
	"github.com/shyun-cc/shw-gf-lib/task"
	taskamqp "github.com/shyun-cc/shw-gf-lib/task/amqp"
)

// assemblePlatform 演示统一 Worker 的完整装配：项目只提供业务处理器、部署白名单、
// 资源硬上限与连接；启停/周期/并发由数据库配置（管理端）控制。不连接真实服务。
func assemblePlatform(ctx context.Context, db gdb.DB, mq rabbitmq.Config, allowed map[string]bool) error {
	store, err := task.NewStore(task.StoreConfig{DB: db, Namespace: "project-worker", TypeControl: true, Outbox: true})
	if err != nil {
		return err
	}
	client := rabbitmq.NewClient(mq)
	if err = client.Connect(ctx); err != nil {
		return err
	}
	defer client.Close()
	if err = client.Declare(ctx, taskamqp.PlatformTopology("project.prod", []string{task.DefaultClass}, true)); err != nil {
		return err
	}
	policy := task.TypePolicy{Lease: time.Minute, Timeout: 10 * time.Minute, Retry: task.RetryOptions{MaxAttempts: 5, Base: 5 * time.Second, Factor: 2, Max: time.Hour, Jitter: 0.2}, Recovery: task.RecoveryRetry}
	rt, err := task.NewRuntime(task.RuntimeConfig{
		Store:     store,
		Transport: taskamqp.NewTransport(client, "project.prod"),
		Handlers: map[string]task.PlatformHandler{
			"sync-device": {Version: "v1", Run: func(ctx context.Context, e *task.Execution) (task.Completion, error) {
				return task.Completion{Outcome: task.OutcomeSuccess, Stats: map[string]int64{"rows": 1}}, nil
			}},
			"daily-sync": {Version: "v1", SplitBatchSize: 200, Splitter: func(ctx context.Context, parent *task.Run, cursor []byte, limit int) (task.SplitBatch, error) {
				return task.SplitBatch{Children: []task.ChildSpec{{Key: "device-1", Type: "sync-device", OrderingKey: "device-1"}}, Cursor: []byte(`{"after":"device-1"}`), Done: true}, nil
			}},
		},
		Allowed:      allowed,
		Types:        []task.TypeSpec{{Type: "sync-device", HandlerVersion: "v1", Policy: policy}, {Type: "daily-sync", HandlerVersion: "v1", Policy: policy}},
		Concurrency:  8,
		Role:         "worker",
		BuildVersion: "example",
	})
	if err != nil {
		return err
	}
	return rt.Run(ctx) // 阻塞到 ctx 取消（SIGTERM）后有界停机
}

var _ = assemblePlatform

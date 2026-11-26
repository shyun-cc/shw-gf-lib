package task_test

import (
	"context"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/shyun-cc/shw-gf-lib/task"
	"time"
)

// assemblePersistent documents the explicit project boundary without contacting a database in tests.
func assemblePersistent(ctx context.Context, db gdb.DB, allowed map[string]string) (*task.Store, *task.Executor, error) {
	store, err := task.NewStore(task.StoreConfig{DB: db, Namespace: "project-worker", TimeMode: task.TimeInstant})
	if err != nil {
		return nil, nil, err
	}
	instance, err := store.RegisterInstance(ctx, task.Instance{Role: "executor", Capacity: 1, Capabilities: allowed})
	if err != nil {
		return nil, nil, err
	}
	exec := &task.Executor{Store: store, InstanceID: instance.ID, Handlers: map[string]task.HandlerRegistration{"example": {Version: "1", Handler: func(context.Context, *task.Execution) (task.Completion, error) {
		return task.Completion{Outcome: "success"}, nil
	}}}}
	_ = time.Second // project drives Tick/Recover independently and calls exec.Run(ctx, time.Second).
	return store, exec, nil
}

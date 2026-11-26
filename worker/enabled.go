package worker

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
)

// envEnabledWorkers 部署编成白名单的环境变量名（变量名固定写死，是对外契约，不做可配置）。
const envEnabledWorkers = "ENABLE_WORKERS"

// RunEnabled 统一注册入口（部署编成唯一启动路径）：读取环境变量 ENABLE_WORKERS
// （逗号分隔白名单，变量名固定写死）过滤 workers，校验失败拒绝启动；
// 过滤结果经 NewWorkerManager 逐个 Register 后 Start，复用 WorkerManager
// 的优雅退出（SIGINT/SIGTERM 或 ctx 取消）与 panic 隔离。
//
// 契约（specs/worker-run-enabled）：
//   - ENABLE_WORKERS 未设或为空：打 Warning（含配置方法与示例）返回 nil，零 worker 启动（fail-closed）；
//   - 白名单含注册列表之外的名：返回 error 拒绝启动，错误列出未知名与全部可用名
//     （精确匹配区分大小写，无通配符，all 不是保留字）；
//   - workers 名字重复（全量检查，无论是否在白名单内）：返回 error 指明重复名；
//   - 启动时 Info 日志 n/m（实际启动数/注册总数）与实际启动名单。
func RunEnabled(ctx context.Context, workers ...Worker) error {
	enabled := parseEnabledList(os.Getenv(envEnabledWorkers))

	// 校验顺序：重名先于白名单——重名会让「可用名列表」自相矛盾，
	// 代码 bug 必须先于配置 bug 暴露（design D2）
	if err := validateUniqueNames(workers); err != nil {
		return err
	}
	if err := validateEnabledList(enabled, workers); err != nil {
		return err
	}

	// 空/未设白名单 = 显式全关：Warning 说明配置方法后零启动返回 nil
	if len(enabled) == 0 {
		g.Log().Warningf(ctx,
			"%s 未设置或为空，未启动任何 worker；如需启动请在环境变量配置白名单，如 %s=order-sync,attachment-archive",
			envEnabledWorkers, envEnabledWorkers)
		return nil
	}

	// 过滤：按注册顺序保留白名单内的 worker，逐个 Register 后经 manager 启动
	kept := filterWorkers(enabled, workers)
	manager := NewWorkerManager()
	for _, w := range kept {
		manager.Register(w)
	}
	g.Log().Infof(ctx, "启动 %d/%d 个 worker: %s",
		len(kept), len(workers), strings.Join(namesInRegisterOrder(kept), ", "))
	return manager.Start(ctx)
}

// parseEnabledList 解析 ENABLE_WORKERS 白名单：逗号拆分 → TrimSpace → 去空项 → 去重保序。
// 纯函数，"a, b,,a" 与 "a,b" 解析结果等价。
func parseEnabledList(raw string) []string {
	seen := make(map[string]struct{})
	var list []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		list = append(list, name)
	}
	return list
}

// validateUniqueNames 重名检查（全量，无论名字是否在白名单内）：
// 重名会让「可用名列表」自相矛盾，代码 bug 必须先于配置 bug 暴露（design D2）。
func validateUniqueNames(workers []Worker) error {
	seen := make(map[string]struct{}, len(workers))
	for _, w := range workers {
		name := w.Name()
		if _, dup := seen[name]; dup {
			return fmt.Errorf("重复注册 worker 名: %s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// validateEnabledList 白名单校验：enabled 中每个名都必须在注册名单内，否则拒绝启动。
// 错误信息列出未知名与全部可用名（按注册顺序逗号连接）；精确匹配区分大小写，无通配符。
func validateEnabledList(enabled []string, workers []Worker) error {
	registered := make(map[string]struct{}, len(workers))
	for _, w := range workers {
		registered[w.Name()] = struct{}{}
	}
	var unknown []string
	for _, name := range enabled {
		if _, ok := registered[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("%s 含未知 worker 名: %s（可用: %s）",
		envEnabledWorkers, strings.Join(unknown, ", "), strings.Join(namesInRegisterOrder(workers), ", "))
}

// filterWorkers 按注册顺序保留白名单内的 worker。
func filterWorkers(enabled []string, workers []Worker) []Worker {
	want := make(map[string]struct{}, len(enabled))
	for _, name := range enabled {
		want[name] = struct{}{}
	}
	var kept []Worker
	for _, w := range workers {
		if _, ok := want[w.Name()]; ok {
			kept = append(kept, w)
		}
	}
	return kept
}

// namesInRegisterOrder 按注册顺序收集全部 worker 名。
func namesInRegisterOrder(workers []Worker) []string {
	names := make([]string, 0, len(workers))
	for _, w := range workers {
		names = append(names, w.Name())
	}
	return names
}

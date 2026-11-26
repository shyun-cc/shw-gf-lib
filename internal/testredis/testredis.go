// Package testredis 提供基于 miniredis 的共享单测基建（内部包，不对外导出）。
//
// 用途：session、redislock、verifycode、seqnum、worker 等依赖 Redis 的包，
// 在单元测试中通过 Start 把 GoFrame 全局默认 redis 配置指向进程内的
// miniredis 实例，使生产代码里的 g.Redis() 零感知地跑在内存 Redis 上，
// 无需外部 Redis 环境。
//
// 设计要点（改动前必读）：
//   - gins.Redis()（frame/g 包）与 gredis.Instance() 都会按分组缓存已创建的
//     *gredis.Redis 实例，且缓存无法从包外清除（前者在 gf 的 internal 包里）。
//     因此 miniredis 必须是进程级单例且常驻不关闭：若每次 Start 换一个新端口，
//     缓存中的旧实例会指向已关闭的 miniredis，后续 g.Redis() 全部失败。
//   - Cleanup 还原的是 gredis 配置注册表（避免配置泄漏到其他测试），并
//     FlushAll 清空数据（避免测试间数据泄漏）；因实例常驻同一端口，测试结束
//     重新注入配置后，gins 缓存的实例依然有效。
package testredis

import (
	"sync"
	"testing"

	// blank import：注册 gf 的 go-redis adapter（gredis 的 defaultAdapterFunc）。
	// 生产代码不 import driver（由接入方项目注册），测试进程需在此自行注册，
	// 否则 g.Redis() 因缺少 adapter 返回 nil。
	"github.com/alicebob/miniredis/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"
	"github.com/gogf/gf/v2/database/gredis"
)

var (
	startOnce sync.Once
	mr        *miniredis.Miniredis // 进程级单例，所有测试共享同一 miniredis 实例
	startErr  error                // 首次启动的错误，仅记录一次
)

// Start 启动（或复用）进程级 miniredis 实例，并把 GoFrame 默认分组的
// redis 配置指向它，同时通过 t.Cleanup 注册还原逻辑：
//   - 恢复测试前的默认分组配置（测试前未配置则移除，避免配置泄漏）；
//   - FlushAll 清空 miniredis 数据（避免测试间数据泄漏）。
//
// 返回 *miniredis.Miniredis 供高级用例操作时间（FastForward）或检查数据。
func Start(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	// 进程内只启动一次；后续调用复用同一实例（原因见包注释）。
	startOnce.Do(func() {
		mr, startErr = miniredis.Run()
	})
	if startErr != nil {
		t.Fatalf("启动 miniredis 失败: %v", startErr)
	}

	// 记录注入前的默认分组配置，Cleanup 时还原。
	oldConfig, hadOldConfig := gredis.GetConfig()
	gredis.SetConfig(&gredis.Config{
		Address: mr.Addr(),
	})
	t.Cleanup(func() {
		if hadOldConfig {
			gredis.SetConfig(oldConfig)
		} else {
			gredis.RemoveConfig()
		}
		mr.FlushAll()
	})

	return mr
}

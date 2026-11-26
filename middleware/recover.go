package middleware

import (
	"context"
	"runtime/debug"

	"github.com/shyun-cc/shw-gf-lib/tracex"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gtcp"
)

// RecoverConnFunc TCP 入口 panic 守卫——包装 gtcp 连接 handler（gtcp.NewServer
// 第二参数的裸函数形态），连接处理链路上任一 panic 在此 recover，打一条带
// traceId 与堆栈的错误日志后正常返回：单连接异常只终结该连接，进程不崩。
//
// 定位对齐 Trace 中间件：纯入口保护、无业务逻辑。HTTP 入口已由 ghttp 内建
// recover + Trace 中间件覆盖，本守卫补齐裸 TCP 形态。
//
// gtcp.Conn 不携带 context，ctx 从 context.Background() 起，入口经
// tracex.Ensure 惰性注入 traceId（幂等，每连接 handler 调用一次），保证该
// 连接的 panic 日志有可检索的链路 id。
//
// panic 日志文案为 spec 契约、不可改动："[TCP PANIC] conn=%s: %v\n%s"
// （name / recover 值 / 堆栈）。
//
// 正常路径零额外日志、零行为差异；recover 后不主动关闭连接——与未被包装时
// handler 正常返回的行为一致，连接生命周期仍归业务 handler / 对端管理。
func RecoverConnFunc(name string, fn func(conn *gtcp.Conn)) func(conn *gtcp.Conn) {
	return func(conn *gtcp.Conn) {
		ctx, _ := tracex.Ensure(context.Background())
		defer func() {
			if r := recover(); r != nil {
				g.Log().Errorf(ctx, "[TCP PANIC] conn=%s: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn(conn)
	}
}

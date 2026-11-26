// Package middleware 通用 HTTP 中间件（认证 + 统一响应封装）。
//
// 认证中间件不硬编码任何项目角色——项目侧自行创建具体的认证中间件：
//
//	func PlatformAuth(r *ghttp.Request) {
//	    middleware.Auth(r, platformSession)
//	}
package middleware

import (
	"net/http"
	"strings"

	"github.com/shyun-cc/shw-gf-lib/response"
	"github.com/shyun-cc/shw-gf-lib/session"
	"github.com/shyun-cc/shw-gf-lib/tracex"

	"github.com/gogf/gf/v2/net/ghttp"
)

const (
	// ContextKeyUser 用户信息存入 request context 的 key
	ContextKeyUser = "user"
)

// Trace 请求入口 traceId 中间件——保证同一请求的响应 traceId 与请求期间全部日志同源。
// 应绑定在中间件链最前（网关已注入则沿用，不覆盖上游值）。
func Trace(r *ghttp.Request) {
	nc, _ := tracex.Ensure(r.Context())
	r.SetCtx(nc)
	r.Middleware.Next()
}

// Auth 通用认证中间件（项目侧包装为具体角色中间件）。
//
// 认证成功后把 session data 全量 map（sess.Validate 的返回值）原样注入
// request context（key "user"）：键名即 session 存储形态（如 admin_id、
// is_super_admin，以及 uuid/last_active_at 等 session 机制键）。库不挑选、
// 不改名、不转义字段——会话里存什么，下游就拿到什么；字段筛选与键名映射
// 是项目决策，由项目侧在 handler 里自取所需。
//
// 项目侧用法：
//
//	func PlatformAuth(r *ghttp.Request) {
//	    middleware.Auth(r, platformSession)
//	}
//
// 库不内置角色语义判定构件（角色/超管语义属项目决策）；项目侧需要超管校验时
// 自建中间件，从 context 取 user map 判 session 原始键即可：
//
//	func MustBeSuperAdmin(r *ghttp.Request) {
//	    // ctx 无 user（未过 Auth）时 Map 为空 map，gconv.Bool(nil) 同样为 false
//	    user := r.GetCtxVar(middleware.ContextKeyUser).Map()
//	    if !gconv.Bool(user["is_super_admin"]) {
//	        _, tid := tracex.Ensure(r.Context())
//	        r.Response.WriteStatus(http.StatusForbidden, g.Map{
//	            "traceId": tid,
//	            "code":    403,
//	            "message": "仅超级管理员可执行此操作",
//	            "data":    g.Map{},
//	        })
//	        r.Exit()
//	        return
//	    }
//	    r.Middleware.Next()
//	}
func Auth(r *ghttp.Request, sess *session.SessionService) {
	header := r.Header.Get("Authorization")
	if header == "" {
		unauthorized(r, "未登录，请先登录")
		return
	}

	token := strings.TrimPrefix(header, "Bearer ")
	if token == "" || token == header {
		unauthorized(r, "未登录，请先登录")
		return
	}

	ctx := r.Context()
	data, err := sess.Validate(ctx, token)
	if err != nil || data == nil {
		unauthorized(r, "登录状态已过期，请重新登录")
		return
	}

	// session data 全量透传：data 即 Validate 返回的 map（含机制键），
	// 不复制、不筛选、不改键名——挑选/映射是项目侧的事
	r.SetCtxVar(ContextKeyUser, data)

	r.Middleware.Next()
}

// unauthorized 返回 HTTP 401
func unauthorized(r *ghttp.Request, message string) {
	response.Unauthorized(r, message)
}

// Response 规范路由统一响应中间件。
//
// 在规范路由（group.Bind + api struct）模式下，controller 返回 (res, err)，
// 本中间件负责将其封装为标准响应格式：
//   - 成功（err==nil）：code=0，message 取自 MessageCarrier 或默认 "success"，data=res
//   - gvalid 校验失败：code=400，HTTP 200
//   - *exception.AppError：按 Type 分层（Domain→422, Infra→500, App→200）
//   - 未捕获异常：code=99999，HTTP 500
//
// 兼容旧式函数路由：旧 controller 手写 response.Success 调用 WriteJsonExit 后，
// 响应缓冲区已有内容，本中间件检测到 BufferLength>0 自动跳过。
func Response(r *ghttp.Request) {
	r.Middleware.Next()

	// 已有响应内容（旧式手写 WriteJsonExit / 认证中间件 401/403 直接写入），跳过
	if r.Response.BufferLength() > 0 || r.Response.BytesWritten() > 0 {
		return
	}

	// HTTP 状态非 200（路由未匹配 404 等场景），交给 BindStatusHandler 处理，跳过
	if r.Response.Status > 0 && r.Response.Status != http.StatusOK {
		return
	}

	err := r.GetError()
	res := r.GetHandlerResponse()

	if err != nil {
		response.WriteException(r, err)
		return
	}

	response.WriteSuccess(r, res)
}

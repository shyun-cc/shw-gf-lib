# middleware — Trace 入口 / Auth 认证 / 统一响应中间件

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `middleware/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。v0.2.0 起 Auth 的 context 透传契约有破坏性调整，旧版迁移指引见仓库 README。

## 1. 组件定位

middleware 提供三个通用 HTTP 中间件，覆盖请求处理的「进—认—出」三站：

| 中间件 | 站位 | 职责 | 协作组件 |
| --- | --- | --- | --- |
| `Trace` | 链最前 | 入口注入 traceId，保证同请求的响应与日志同源 | [tracex](tracex.md) |
| `Auth` | Trace 之后 | Bearer token 认证，session data 全量透传到 context | [session](session.md) |
| `Response` | 链最后 | 规范路由统一响应封装与异常分层分流 | [response](response.md) |

除三个 HTTP 中间件外，包内另有 TCP 入口 panic 守卫 `RecoverConnFunc(name, handler)`——包装 gtcp 连接 handler（`gtcp.NewServer` 第二参数裸函数形态），连接链上任一 panic 捕获后落 `[TCP PANIC] conn=<name>: <panic 值>` 形态错误日志（带 traceId 与堆栈）再正常返回，单连接异常只终结该连接、进程不崩；接入形态见 [docs/logging.md](logging.md) 第 2 节 TCP 形态。

边界纪律是「**机制在库、语义在项目**」：库提供认证机制（token 提取、session 校验、context 注入）与响应机制（格式、分流），不硬编码任何项目角色——角色/超管/权限语义的判定构件不进库，由项目侧基于透传数据自建（第 2 节有完整示例）。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节），gf Redis 配置就绪（`g.Redis()` 可用，Auth 依赖 session 存取）。

```go
package main

import (
	"context"

	"github.com/shyun-cc/shw-gf-lib/middleware"
	"github.com/shyun-cc/shw-gf-lib/session"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
)

// ① 项目自建会话服务：Role/TTL/IDField 由项目定义（三参数语义见 docs/session.md）
var platformSession = session.New("platform", 86400, "admin_id")

// ② 项目侧包装：把通用 Auth 绑定到具体会话服务，得到本项目角色的认证中间件
func PlatformAuth(r *ghttp.Request) {
	middleware.Auth(r, platformSession)
}

// 规范路由的请求/响应结构（项目侧 api 包定义，此处示意）
type DetailReq struct{}
type DetailRes struct {
	Id    int    `json:"id"`
	Title string `json:"title"`
}

func main() {
	s := g.Server()

	// ③ 认证路由组：Trace（最前）→ Auth → Response（最后）
	authed := s.Group("/admin", PlatformAuth)
	authed.Middleware(middleware.Trace, middleware.Response)
	authed.GET("/detail", func(ctx context.Context, req *DetailReq) (res *DetailRes, err error) {
		return &DetailRes{Id: 7, Title: "订单"}, nil
	})

	s.Run()
}
```

认证通过后，handler 里从 context 取 session 数据（**键名即 session 存储原始形态**，如 `admin_id`、`is_super_admin`，不转驼峰）：

```go
user := r.GetCtxVar(middleware.ContextKeyUser).Map()
name := user["name"]                          // 会话里存什么，这里就拿到什么
super := gconv.Bool(user["is_super_admin"])   // 原始键名直接判定
```

**需要「仅超管可访问」的端点时，在项目侧自建判定中间件**（与 `Auth` godoc 示例一致）：

```go
// MustBeSuperAdmin 项目侧自建的超管校验中间件
func MustBeSuperAdmin(r *ghttp.Request) {
	// ctx 无 user（未过 Auth）时 Map 为空 map，gconv.Bool(nil) 同样为 false
	user := r.GetCtxVar(middleware.ContextKeyUser).Map()
	if !gconv.Bool(user["is_super_admin"]) {
		_, tid := tracex.Ensure(r.Context())
		r.Response.WriteStatus(http.StatusForbidden, g.Map{
			"traceId": tid,
			"code":    403,
			"message": "仅超级管理员可执行此操作",
			"data":    g.Map{},
		})
		r.Exit()
		return
	}
	r.Middleware.Next()
}
```

挂载：`admin := s.Group("/admin", PlatformAuth, MustBeSuperAdmin)`——机制（Auth）与语义（超管判定）各自独立、自由组合。

## 3. 三中间件参数与行为语义

**`Trace(r *ghttp.Request)`**：入口调 `tracex.Ensure` 把 traceId 注入请求 ctx——已有则沿用（网关/上游注入不覆盖），没有则生成。应绑定在中间件链**最前**，是响应 traceId 与请求期间日志同值的贯穿前提（机制细节见 [docs/tracex.md](tracex.md)）。

**`Auth(r *ghttp.Request, sess *session.SessionService)`**（v0.2.0 两参签名）：

1. 读 `Authorization` 头，缺失 → 401「未登录，请先登录」；
2. 剥离 `Bearer ` 前缀得 token——前缀不存在（剥完与原值相同）或 token 为空 → 401「未登录，请先登录」；
3. `sess.Validate(ctx, token)` 校验——出错或会话不存在/过期 → 401「登录状态已过期，请重新登录」；
4. 通过：把 Validate 返回的 session data **全量 map** 原样注入 request context（key `user`，即 `ContextKeyUser` 常量），继续后续 handler。

**`Response(r *ghttp.Request)`**：先放行 `Next`，回到本层后按序判定：

1. 响应缓冲区已有内容（旧式手写出口 / 认证层已直接写 401/403）→ 跳过，不重复写；
2. HTTP 状态非 200（路由未匹配 404 等）→ 跳过，交给 `BindStatusHandler` 处理；
3. `err != nil` → `response.WriteException` 按异常分层分流（AppError 三层 / 校验失败 / 未捕获兜底）；
4. 否则 `response.WriteSuccess` 封装 `(res, nil)` 为 `{"traceId":..., "code":0, "message":"success", "data":res}`。

## 4. 契约

**Auth 的 context 透传契约（v0.2.0）**：认证成功后 context `user` 键的值是 session data 的**完整 map**——会话里存什么，下游就拿到什么。库不挑选、不改名、不转义任何字段：

- 业务字段按 session 存储原始键名可见（如 `admin_id`、`name`、`role`、`is_super_admin`）；
- session 机制键一并透传（`uuid` 即会话 token、`login_at`、`last_active_at`），多传无害，挑选才是越位；
- 所有值经 Redis Hash 往返后为字符串形态（如 `role: "2"`），判定时用 `gconv` 转换。

**认证失败的响应契约**：统一 HTTP 401 + [response](response.md) 四字段体 `{traceId, code: 401, message, data: {}}`，两种 message（未登录 / 已过期）对应两类失败原因（第 3 节步骤 1–2 与步骤 3），后续 handler 不执行。

**Response 的双形态兼容契约**：规范路由（group.Bind + api struct）与旧式函数路由（handler 内手写 `response.Success`）可共存——后者靠「缓冲区非空即跳过」识别，不会出现双写拼接体。

**库不含角色语义构件**：任何角色/权限判定（超管、角色码、权限位）都属项目决策，库导出符号中不存在这类构件；项目侧按第 2 节示例自建。

## 5. FAQ

**Q：Trace 中间件必须挂吗？**

强烈建议挂且挂最前。不挂时 response 出口仍会惰性生成 traceId（响应不缺字段），但与请求期间日志不同值，报障时对不上日志；Auth 内的 session 校验日志同样需要它同源。

**Q：Auth 为什么是包装用法（项目再包一层）而不是直接挂？**

`Auth` 需要绑定到项目自建的 `SessionService` 实例（Role/TTL/IDField 是项目参数），包一层（如 `PlatformAuth`）是最直接的绑定形态；多角色项目各包各的（`BusinessAuth`、`ClientAuth`...），互不干扰。

**Q：context 里取不到 `user` 里的驼峰键（如 `isSuperAdmin`）？**

v0.2.0 起键名即 session 存储原始形态（下划线），库不做任何改名。下游取值用 session 写入时的原始键名；旧版驼峰键的迁移对照见仓库 README。

**Q：一个端点要「登录即可」另一个要「仅超管」，怎么组合？**

登录即可：组上挂 `PlatformAuth`；仅超管：同组再加项目自建判定中间件（第 2 节 `MustBeSuperAdmin` 形态），机制与语义叠加组合。

**Q：旧式函数路由的项目能用 Response 中间件吗？**

能。handler 手写出口（如 `response.Success`）后缓冲区非空，中间件自动跳过；两种形态可在同一 server 混用，逐步迁移无阵痛。

**Q：Auth 的 401 响应会被 Response 中间件再包一层吗？**

不会。401 由 `response.Unauthorized` 直接写入并退出，Response 中间件检测到缓冲区已有内容即跳过（契约见第 4 节）。

## 6. 维护者注记

**v0.2.0 改造动机**：旧实现把单项目的 session 字段形状（挑选 + 驼峰改名）与超管判定硬编码进库，违反「库不做项目决策」。改造后库只管机制（token 提取、校验、全量透传），字段挑选、键名映射、角色判定全部下放项目侧——对照 hyperf-lib 分层（库管 session 机制、项目管语义）。全量透传的代价是机制键（`uuid` 等）也进 context，取舍为「多传无害、挑选才是越位」。

**认证链路的 ctx 纪律**：middleware 包源码不得出现 `context.Background()`——session 校验一律用 `r.Context()`，保证 session 内部日志与请求 traceId 同源（存量由测试与检查锁住）。

**测试形态**（`middleware/` 测试，外部测试包 + 真实 ghttp 本地随机端口 server）：Auth 用真 SessionService + miniredis 覆盖无头 401 / 非 Bearer 401 / 会话不存在 401（两类 message 分别断言）/ 有效 token 全量透传（含机制键、反向断言无驼峰改写键）；Response 覆盖规范路由成功封装、Application 异常 200+code、旧式手写跳过（响应体须是单个合法 JSON——双写会解析失败）；Trace 覆盖响应与日志 traceId 同源、不同请求互不相同。

**改动规矩**：Auth 透传形态（全量 map + 原始键名）与两参签名是对外契约，错误文案（「未登录，请先登录」「登录状态已过期，请重新登录」）是对外可观察行为；此类改动走 change 流程，并同步本文档、[docs/session.md](session.md) 与 README。

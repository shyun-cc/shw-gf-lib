# session — 基于 Redis Hash 的会话服务（参数化）

> 本文档面向两类读者：**项目侧接入者**读第 1–5 节（怎么用）；**库维护者**加读第 6 节（为什么这样设计）。事实来源是 `session/` 源码，本文的代码示例、签名与错误文案均与其逐字一致；若发现不一致，以源码为准并修正本文档。v0.2.0 起 Create 返回值瘦身，旧版迁移指引见仓库 README。

## 1. 组件定位

session 是基于 Redis Hash 的会话服务（对应 PHP hyperf-lib 的 Session），管「登录态的存储与校验」这一层机制：

- **机制在库**：会话键的创建、业务字段落盘、滑动过期（每次校验刷新 TTL）、单会话/按用户销毁、用户会话索引；
- **业务在项目**：`Role` / `TTL` / `IDField` 三参数由项目定义，会话里存哪些业务字段（name、role、权限位……）由项目经 `Create` 的 `data` 参数自行传入——库不枚举、不感知其形状。

它是 [middleware](middleware.md) `Auth` 的底层依赖：Auth 提取 Bearer token 后调 `Validate` 校验并把返回的全量 data 透传进 context。项目侧通常不直接面对 `Validate`（Auth 代劳），主要直接使用 `Create`（登录）与 `Destroy` / `DestroyByUserID`（登出 / 踢会话）。

## 2. 快速接入

前提：项目已完成 gf-lib 接入（见仓库 README 第 2 节），gf Redis 配置就绪（`g.Redis()` 可用）。

```go
package controller

import (
	"github.com/shyun-cc/shw-gf-lib/exception"
	"github.com/shyun-cc/shw-gf-lib/middleware"
	"github.com/shyun-cc/shw-gf-lib/response"
	"github.com/shyun-cc/shw-gf-lib/session"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/gconv"
)

// ① 项目自建会话服务工厂：三参数语义见第 3 节
var platformSession = session.New("platform", 86400, "admin_id")

// Login 登录：项目完成账号密码校验后创建会话，token 交前端保存
func Login(r *ghttp.Request) {
	// ...项目自身的账号密码校验（省略）...
	// userID 是不透明索引键文本：主键 uuidv7 的项目传 id.String()，
	// int64 主键的项目传 strconv.FormatInt(id, 10)
	adminID := "42"

	// ② 创建会话：业务字段经 data 全量落盘（键名即后续 Auth 透传的键名）；
	// IDField（admin_id）由库自动写入参数值，data 无需携带（见第 4 节契约）
	sd, err := platformSession.Create(r.Context(), adminID, map[string]interface{}{
		"name":           "张三",
		"username":       "zhangsan",
		"role":           1,
		"is_super_admin": 0,
	})
	if err != nil {
		response.WriteException(r, exception.NewInfrastructure(50001, "redis 连接失败"))
		return
	}

	// sd.UUID 即会话 token：前端保存后以 Authorization: Bearer <token> 携带，
	// 认证侧接线（Auth 中间件包装）见 docs/middleware.md
	response.Success(r, g.Map{"token": sd.UUID})
}

// Logout 登出：销毁当前会话（uuid 键经 Auth 全量透传可得）
func Logout(r *ghttp.Request) {
	user := r.GetCtxVar(middleware.ContextKeyUser).Map()
	if err := platformSession.Destroy(r.Context(), gconv.String(user["uuid"])); err != nil {
		response.WriteException(r, exception.NewInfrastructure(50001, "redis 连接失败"))
		return
	}
	response.Success(r, g.Map{})
}

// KickAll 改密码后踢掉该用户全部会话
func KickAll(r *ghttp.Request) {
	_ = platformSession.DestroyByUserID(r.Context(), "42")
	response.Success(r, g.Map{})
}
```

## 3. New 三参数语义

`session.New(role string, ttl int, idField string) *SessionService`：

| 参数 | 语义 | 示例 |
| --- | --- | --- |
| `role` | 角色标识，决定 Redis 键前缀与命名空间——不同角色（platform / business / client）的会话互不可见 | `"platform"` |
| `ttl` | 会话有效期（秒），滑动过期：`Validate` 每次成功都重置 | `86400`（24 小时） |
| `idField` | Hash 中存储用户 ID 的字段名，销毁单会话时反查 userID 清索引 | `"admin_id"` |

`SessionService` 三个字段（`Role` / `TTL` / `IDField`）均为导出字段，但应经 `New` 构造；一个项目多个角色体系就 `New` 多个实例各自持有。

## 4. 契约

**Create 返回值契约（v0.2.0 三字段）**：`Create(ctx, userID string, data map[string]interface{}) (*SessionData, error)` 返回的 `SessionData` 仅含机制层可保证的三个字段（`UUID` / `LoginAt` / `LastActiveAt`）——业务字段只落盘 Redis，不进返回值；`userID` 是不透明索引键文本（主键 uuidv7 的项目传 `id.String()`，int64 主键的项目传 `strconv.FormatInt(id, 10)`）：

```go
type SessionData struct {
	UUID         string `json:"uuid"`           // 会话 token（uuid.New() 生成）
	LoginAt      string `json:"login_at"`       // 登录时间
	LastActiveAt string `json:"last_active_at"` // 最近活跃时间
}
```

时间格式为 `2006-01-02 15:04:05`。`data` 会与自动写入的 `IDField`（值为参数 userID）、`login_at` / `last_active_at` 合并写入会话 Hash——三个自动键在 data 拷贝之后写入，data 里的同名键一律被库覆盖。

**Validate 返回值契约**：`Validate(ctx, sessionUUID string) (map[string]string, error)`——

- 返回会话 Hash 的**全量字段**（`map[string]string`，值经 Redis 往返均为字符串形态），并补写 `uuid` 与刷新后的 `last_active_at` 两个键；
- 会话不存在/已过期返回 `(nil, nil)`，**不报错**（调用方以 data==nil 判定失效）；
- 副作用：刷新 `last_active_at` 并重置 TTL（滑动过期）。

**Redis 键契约**：

| 键 | 格式 | 结构 | 用途 |
| --- | --- | --- | --- |
| 会话键 | `{role}:session:{uuid}` | Hash（data 全量 + 自动写入的 IDField + login_at + last_active_at），带 TTL | 单会话存储 |
| 用户索引 | `{role}:session:user:{userID}` | Set（该用户的全部会话 UUID），**不带 TTL** | `DestroyByUserID` 反查 |

**IDField 使用要点**：`Create` 自动把参数 `userID` 写入 Hash 的 `IDField` 字段（在 data 拷贝之后写入，data 同名键被参数值覆盖），保证 Hash 中的用户 ID 与用户索引一致——调用方无需在 `data` 里携带 `IDField` 键。`Destroy` 单会话时从 Hash 读 `IDField` 反查 userID 清索引。

**单用户多会话**：同一 userID 多次 `Create` 产生多个独立会话（多端登录共存），全部记在用户索引下；`DestroyByUserID` 一次清空。

## 5. FAQ

**Q：为什么 `Create` 返回值里没有 name / role 这些业务字段？**

v0.2.0 起返回值只承诺机制层能保证的字段——业务字段是你自己经 `data` 传进去的，登录时本来就有，没必要再从 Redis 绕一圈取回来。下游（handler 里）要业务数据走 Auth 透传的 context（见 [docs/middleware.md](middleware.md)）。

**Q：会话 token 是什么？怎么发给前端？**

即 `sd.UUID`（标准 UUID 字符串）。登录接口把它返回给前端，前端以 `Authorization: Bearer <token>` 携带，`Auth` 中间件自动完成校验与数据透传。

**Q：TTL 到期后会发生什么？**

会话键被 Redis 自动删除，此后 `Validate` 返回 `(nil, nil)`、Auth 回 401「登录状态已过期，请重新登录」。用户索引 Set 不带 TTL，其中该会话的 UUID 成为悬挂成员，无害（见维护者注记）。

**Q：改密码后怎么踢掉该用户所有登录态？**

`DestroyByUserID(ctx, userID)`——按用户索引销毁其全部会话。只踢某一端用 `Destroy(ctx, uuid)`。

**Q：不同角色（后台管理员 / C 端用户）会串会话吗？**

不会。`role` 参数进键前缀，`platform:session:*` 与 `client:session:*` 是两个命名空间；各角色的 Auth 中间件绑各自的 `SessionService` 实例（见 [docs/middleware.md](middleware.md) 包装用法）。

**Q：`Validate` 报错和会话失效怎么区分？**

`err != nil` 是 Redis 层故障（连接失败等）；`(nil, nil)` 才是会话不存在/过期。Auth 对两者都回 401，但语义不同——前者理论上该按基础设施异常处理，当前统一按过期提示，避免把 Redis 拓扑暴露给前端。

## 6. 维护者注记

**v0.2.0 瘦身动机**：旧 `SessionData` 的 Name/Username/Role/IsSuperAdmin 等业务字段是从单项目搬运的迁移化石，从未被库内读写，却诱导下游把「库返回业务数据」当契约。瘦身至 `UUID` / `LoginAt` / `LastActiveAt` 三字段后，「库只承诺机制字段、业务数据走 data 全量存取」的边界由类型系统表达。

**userID 不透明文本化**：`Create` / `DestroyByUserID` 的 `userID` 参数是字符串文本（主键 uuidv7 的项目传 `id.String()`，int64 主键的项目传 `strconv.FormatInt(id, 10)`），库不感知项目主键形态。IDField 由库在 Create 时自动写入参数值（data 同名键被覆盖），从根上消除旧版「调用方忘带 IDField 键导致索引残留」的坑。

**双结构取舍（Hash + Set）**：会话主体用 Hash（业务字段逐键存取、全量读回）；用户索引用 Set（O(1) 判定归属、SMembers 反查销毁清单）。索引不带 TTL 是 Redis Set 的天然限制——会话键过期后索引残留悬挂成员，`Destroy` 的 SRem 与 `DestroyByUserID` 的整体 Del 对不存在的成员无副作用，语义自洽；量级担忧（单用户会话数）受滑动过期与多端登录的现实规模约束。

**时间格式固定 `2006-01-02 15:04:05`**：与迁移前项目形态对齐的既定契约，改动属破坏性变更（存量会话的 last_active_at 判定受影响）。

**测试形态**（`session/` 测试，miniredis）：Create 落盘字段全集 + TTL 生效 + 索引成员 + 返回值仅机制三字段 + IDField 自动写入双场景（data 未携带时补写、携带时被参数值覆盖，userID 兼用 int64 文本 "42"/"43" 与 uuidv7 样式文本）；Validate 的 (nil, nil) 分支、字段全量返回、last_active_at 与 TTL 双刷新（miniredis 虚拟时钟 FastForward，不真 sleep）；Destroy 单会话清理与同用户其余会话不受影响；DestroyByUserID 全清且不影响他人。中间件侧的端到端认证链路测试在 `middleware/` 包（真 SessionService + miniredis）。

# pagination — 统一分页响应契约

> 面向项目侧接入者（第 1–4 节）与库维护者（加读第 5 节）。事实来源是 `pagination/` 源码，本文的代码示例、签名与行为描述均与其逐字一致；若发现不一致，以源码为准并修正本文档。

## 1. 组件定位

统一分页响应契约（对应 PHP hyperf-lib 的 AbstractOffsetPaginatedResDTO / AbstractCursorPaginatedResDTO），Go 泛型形态。两个形态覆盖两类分页场景：

- **`Offset[T]`** 页码型：items/curPage/perPage/lastPage/total 五字段，适合后台管理列表（可跳页）；
- **`Cursor[T]`** 游标型：items/perPage/nextMarker 三字段，适合信息流（不可跳页、深分页不退化）。

它与 `response` 组件正交：pagination 负责分页结构，response 负责统一响应体外壳——业务 handler 里构造 `Offset[T]` / `Cursor[T]` 后放进 `response.Success` 的 data 字段即可。

## 2. 快速接入

```go
package handler

import (
	"github.com/shyun-cc/shw-gf-lib/pagination"
	"github.com/shyun-cc/shw-gf-lib/response"
)

// 页码型：列表查询后构造五字段响应（lastPage 由 MarshalJSON 锁定计算，不可注入）
func ListUsers(r *ghttp.Request) {
	items := queryUsers(curPage, perPage) // 项目自己的查询
	total := countUsers()

	response.Success(r, pagination.NewOffset(items, curPage, perPage, total))
}

// 游标型：nextMarker 传 nil 表示末页（序列化为 JSON null）
func Feed(r *ghttp.Request) {
	items, next := queryFeed(marker, perPage) // 项目自己的游标查询

	response.Success(r, pagination.NewCursor(items, perPage, next))
}
```

序列化输出（`response.Success` 内部 `json.Marshal` 时触发）：

```json
{"items":[...],"curPage":1,"perPage":20,"lastPage":5,"total":93}
{"items":[...],"perPage":20,"nextMarker":"eyJpZCI6MTAyNH0"}
{"items":[...],"perPage":20,"nextMarker":null}
```

## 3. 契约

**Offset 五字段契约**（由 `MarshalJSON` 锁定，构造后无法绕过）：

- `lastPage` 计算公式：`perPage > 0` 时 `max(1, ceil(total/perPage))`；`perPage <= 0` 时恒 `0`；
- `items` 为 nil 时序列化为 `[]` 而非 `null`（前端不用判空）；
- 字段顺序固定 items → curPage → perPage → lastPage → total。

**Cursor 三字段契约**：

- `nextMarker` 为 `*string`，nil 序列化为 JSON `null`——前端以 `nextMarker === null` 判末尾，无须额外标志字段；
- marker 的编码格式（base64 / 业务 ID）由项目决定，组件只承载「有则继续翻、无则停止」的语义。

## 4. FAQ

**Q：为什么 lastPage 不让调用方传？**

lastPage 是 total/perPage 的派生值，开放注入就会出现「total=93、perPage=20、lastPage=100」一类的不一致脏数据。构造函数 `NewOffset` 收四个原始值，派生值在序列化时锁定计算——这是对 PHP 基类「子类不可覆盖 lastPage 计算」语义的延续。

**Q：perPage=0 或负数会怎样？**

Offset 的 lastPage 恒为 0（公式分支），不 panic 不报错。出现这种值说明上游查询参数没校验，应修调用方。

**Q：为什么 items 是泛型而不是 `[]any`？**

泛型保住类型检查与序列化性能；`[]any` 会在编译期放过字段拼写错误。

**Q：与项目里已有的分页 DTO 怎么共存？**

新接口直接用本契约；存量接口不必立刻迁移——但同一系统内两种分页响应形态并存会让前端写两套判定，建议按模块逐步收敛。

## 5. 维护者注记

**MarshalJSON 手写拼接而非结构体标签**：lastPage 是计算值没有存储字段，用 `json.Marshaler` 在序列化期计算是唯一能「锁死」它的位置。内部的 itoa/intToString 手写数字转字符串是为避免 `strconv` 依赖面（保持包零外部 import 除标准库 encoding/json 与 math）。

**对应关系**：Offset ↔ AbstractOffsetPaginatedResDTO，Cursor ↔ AbstractCursorPaginatedResDTO；PHP 侧 lastPage 由基类 getter 锁定，Go 侧由 MarshalJSON 锁定，语义等价。

**改动规矩**：五字段/三字段与字段名是对外 JSON 契约（前端依赖），改动属破坏性变更，须走 change 流程并升 minor。

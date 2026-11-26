# uuidutil — UUID 入参与 SQL 空值转换

## 定位

只处理转换，不生成主键、不强制 UUIDv7，不决定字段是否必填。依赖 google/uuid，无库内组件依赖。

## API 与契约

| API | 行为 |
|---|---|
| `type Empty string` | 字符串入参包装，避免将空串直接转换为 uuid.UUID 数组 |
| `(e Empty) UUID() uuid.UUID` | 兼容入口：空串、非法串返回 uuid.Nil |
| `(e Empty) Ptr() *uuid.UUID` | 兼容入口：空串、非法串、零 UUID 返回 nil |
| `(e Empty) Value() (driver.Value, error)` | 兼容入口：空串、非法串、零 UUID 返回 SQL NULL；有效值返回 UUID 字符串 |
| `(e Empty) MarshalJSON() ([]byte, error)` | 原字符串输出；空值输出空字符串，非法字符串也原样输出 |
| `(e Empty) Parse() (uuid.UUID, error)` | 空串返回 uuid.Nil；非空非法值返回错误 |
| `(e Empty) ParsePtr() (*uuid.UUID, error)` | 空串、零 UUID 返回 nil；非空非法值返回错误 |
| `NullUUID(id uuid.UUID) interface{}` | 零 UUID 返回 nil，否则返回 UUID 本体 |
| `NullUUIDPtr(p *uuid.UUID) interface{}` | nil 或指向零 UUID 返回 nil，否则保留原指针 |

新业务优先 Parse/ParsePtr。迁移旧项目时 UUID/Ptr/Value 保持原行为，切换到严格入口是独立行为变更。SQL Valuer 是兼容入口，写库前需要严格校验时先调用 Parse。解析格式遵循 google/uuid.Parse，也接受该函数支持的紧凑、URN 等形式；不自动 TrimSpace。必填字段由项目拒绝 uuid.Nil。

对新建零值 DTO，JSON 缺省、null、空串得到 Empty 空值；复用非零 DTO 时 JSON null 保持 Go 标准解码行为，不承诺清空旧值。PATCH 的缺省/清空/非法三态应由项目单独建模。包内测试覆盖 JSON 与 gconv.Struct 的零值 DTO 转换。

## 接入与验证

发布后导入 `github.com/shyun-cc/shw-gf-lib/uuidutil`，可先用类型别名兼容项目旧入口。仅消费发布 tag。测试位于 uuidutil/uuidstr_test.go，覆盖兼容/严格解析、JSON、GoFrame gconv 与 SQL NULL；无需数据库。

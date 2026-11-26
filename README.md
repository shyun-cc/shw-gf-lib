# shw-gf-lib

面向 GoFrame v2 应用的 Go 组件库，提供 HTTP 响应与中间件、会话、Redis 锁、数据库迁移、异步任务、对象存储，以及常见云服务和微信 API 的客户端。

## 安装

需要 Go 1.25.7 或更新版本。在应用模块中运行：

```sh
go get github.com/shyun-cc/shw-gf-lib/uuidutil@latest
```

各组件可以按需引入。例如，使用 `uuidutil` 解析可为空的 UUID 字符串：

```go
import "github.com/shyun-cc/shw-gf-lib/uuidutil"

// 空输入返回零 UUID，格式错误返回 error。
id, err := uuidutil.Empty(input).Parse()
```

`migrate` 依赖 Cgo 和 C 编译器。默认构建提供常规迁移命令，开发构建标签 `dev` 会额外启用可清库的开发命令。具体行为见 [迁移文档](docs/migrate.md)。

## 组件

| 用途 | 组件文档 |
| --- | --- |
| HTTP 与配置 | [exception](docs/exception.md)、[response](docs/response.md)、[middleware](docs/middleware.md)、[session](docs/session.md)、[settings](docs/settings.md)、[cfgsource](docs/cfgsource.md)、[pagination](docs/pagination.md) |
| 基础工具 | [cryptox](docs/cryptox.md)、[logging](docs/logging.md)、[tracex](docs/tracex.md)、[events](docs/events.md)、[redislock](docs/redislock.md)、[seqnum](docs/seqnum.md)、[uuidutil](docs/uuidutil.md) |
| 数据与任务 | [migrate](docs/migrate.md)、[worker](docs/worker.md)、[task](docs/task.md)、[rabbitmq](docs/rabbitmq.md)、[storage](docs/storage.md)、[attachment](docs/attachment.md) |
| 云服务 | [aliyun](docs/aliyun.md)、[sms](docs/sms.md)、[verifycode](docs/verifycode.md)、[express](docs/express.md)、[tencent](docs/tencent.md)、[region](docs/region.md) |
| 微信 | [wxapp](docs/wxapp.md)、[wxoa](docs/wxoa.md)、[wxpay](docs/wxpay.md)、[wecom](docs/wecom.md) |

应用通过组件的配置或构造参数注入供应商凭据。微信小程序示例见 [examples/wxapp/safe-password](examples/wxapp/safe-password)。

## 开发

在仓库根目录运行：

```sh
go vet ./...
go test ./...
```

GitHub Actions 会检查 Go 文件格式并执行这两项检查。`tests/` 是单独的模块，包含按构建标签启用的可选测试。其连接参数和运行方式见 [tests/doc.go](tests/doc.go)。

## 鸣谢与第三方版权（Acknowledgments & Licenses）

感谢以下项目及其贡献者。本库通过 Go 模块依赖使用这些项目。

| 上游 | 用途 | 许可证 |
| --- | --- | --- |
| [GoFrame v2](https://github.com/gogf/gf) 及 Redis 适配器 | HTTP、中间件、配置、日志、数据访问与 Redis | [MIT](https://github.com/gogf/gf/blob/v2.10.2/LICENSE) |
| [goose](https://github.com/pressly/goose) | 数据库迁移执行 | [MIT](https://github.com/pressly/goose/blob/v3.27.3/LICENSE) |
| [pg_query_go](https://github.com/pganalyze/pg_query_go) 与 [PostgreSQL](https://www.postgresql.org/) | 通过 CGO 编译 PostgreSQL 语法解析器并分析迁移 SQL | [BSD-3-Clause](LICENSES/pg_query_go.BSD-3-Clause.txt)、[PostgreSQL License](LICENSES/pg_query_go.PostgreSQL.txt) |
| [amqp091-go](https://github.com/rabbitmq/amqp091-go) | RabbitMQ 客户端 | [BSD-2-Clause](https://github.com/rabbitmq/amqp091-go/blob/v1.14.0/LICENSE) |
| [robfig/cron](https://github.com/robfig/cron) | 定时任务 | [MIT](https://github.com/robfig/cron/blob/v3.0.1/LICENSE) |
| [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go) | 链路追踪 | [Apache-2.0](https://github.com/open-telemetry/opentelemetry-go/blob/v1.44.0/LICENSE) |
| [google/uuid](https://github.com/google/uuid) | UUID | [BSD-3-Clause](https://github.com/google/uuid/blob/v1.6.0/LICENSE) |
| [gmsm](https://github.com/emmansun/gmsm) 和 [Go x/crypto](https://pkg.go.dev/golang.org/x/crypto) | 密码学算法实现 | [MIT](https://github.com/emmansun/gmsm/blob/v0.44.1/LICENSE)、[BSD-3-Clause](https://go.googlesource.com/crypto/+/refs/tags/v0.54.0/LICENSE) |
| [miniredis](https://github.com/alicebob/miniredis) | Redis 单元测试 | [MIT](https://github.com/alicebob/miniredis/blob/v2.38.0/LICENSE) |

对象存储签名及 URL 行为参考了[阿里云 OSS 的签名规范](https://www.alibabacloud.com/help/en/oss/developer-reference/signature-version-4-recommend)及[官方 Go SDK 的 CNAME URL 处理](https://github.com/aliyun/aliyun-oss-go-sdk/blob/master/oss/conn.go)（[MIT](https://github.com/aliyun/aliyun-oss-go-sdk/blob/master/LICENSE)）。解析器的版权声明和分发范围见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 许可证

[MIT](LICENSE)

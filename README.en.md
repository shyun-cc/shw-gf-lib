# shw-gf-lib

[简体中文](./README.md) | English | [繁體中文](./README.zh-TW.md)

A Go library for GoFrame v2 applications. It provides HTTP responses and middleware, sessions, Redis locks, database migrations, background jobs, object storage, and clients for common cloud services and WeChat APIs.

## Install

Use Go 1.25.7 or later. Run this command from your application module:

```sh
go get github.com/shyun-cc/shw-gf-lib/uuidutil@latest
```

Import only the components you need. For example, use `uuidutil` to parse a UUID string that may be empty:

```go
import "github.com/shyun-cc/shw-gf-lib/uuidutil"

// Empty input returns the zero UUID. Invalid input returns an error.
id, err := uuidutil.Empty(input).Parse()
```

The `migrate` package requires Cgo and a C compiler. The default build provides standard migration commands. The `dev` build tag also enables development commands that can clear the database. See the [migration documentation](docs/migrate.md) for details.

## Components

| Purpose | Component documentation |
| --- | --- |
| HTTP and configuration | [exception](docs/exception.md), [response](docs/response.md), [middleware](docs/middleware.md), [session](docs/session.md), [settings](docs/settings.md), [cfgsource](docs/cfgsource.md), [pagination](docs/pagination.md) |
| Utilities | [cryptox](docs/cryptox.md), [logging](docs/logging.md), [tracex](docs/tracex.md), [events](docs/events.md), [redislock](docs/redislock.md), [seqnum](docs/seqnum.md), [uuidutil](docs/uuidutil.md) |
| Data and jobs | [migrate](docs/migrate.md), [worker](docs/worker.md), [task](docs/task.md), [rabbitmq](docs/rabbitmq.md), [storage](docs/storage.md), [attachment](docs/attachment.md) |
| Cloud services | [aliyun](docs/aliyun.md), [sms](docs/sms.md), [verifycode](docs/verifycode.md), [express](docs/express.md), [tencent](docs/tencent.md), [region](docs/region.md) |
| WeChat | [wxapp](docs/wxapp.md), [wxoa](docs/wxoa.md), [wxpay](docs/wxpay.md), [wecom](docs/wecom.md) |

Applications supply provider credentials through component configuration or constructor arguments. For a WeChat Mini Program example, see [examples/wxapp/safe-password](examples/wxapp/safe-password).

## Develop

Run these commands from the repository root:

```sh
go vet ./...
go test ./...
```

GitHub Actions checks Go formatting and runs both commands. The separate `tests/` module contains optional tests enabled by build tags. See [tests/doc.go](tests/doc.go) for connection settings and instructions.

## Acknowledgments & Licenses

Thanks to the following projects and their contributors. This library uses these projects as Go module dependencies.

| Upstream | Use | License |
| --- | --- | --- |
| [GoFrame v2](https://github.com/gogf/gf) and its Redis adapter | HTTP, middleware, configuration, logging, data access, and Redis | [MIT](https://github.com/gogf/gf/blob/v2.10.2/LICENSE) |
| [goose](https://github.com/pressly/goose) | Database migration execution | [MIT](https://github.com/pressly/goose/blob/v3.27.3/LICENSE) |
| [pg_query_go](https://github.com/pganalyze/pg_query_go) and [PostgreSQL](https://www.postgresql.org/) | PostgreSQL parser compiled through Cgo to analyze migration SQL | [BSD-3-Clause](LICENSES/pg_query_go.BSD-3-Clause.txt), [PostgreSQL License](LICENSES/pg_query_go.PostgreSQL.txt) |
| [amqp091-go](https://github.com/rabbitmq/amqp091-go) | RabbitMQ client | [BSD-2-Clause](https://github.com/rabbitmq/amqp091-go/blob/v1.14.0/LICENSE) |
| [robfig/cron](https://github.com/robfig/cron) | Scheduled jobs | [MIT](https://github.com/robfig/cron/blob/v3.0.1/LICENSE) |
| [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go) | Distributed tracing | [Apache-2.0](https://github.com/open-telemetry/opentelemetry-go/blob/v1.44.0/LICENSE) |
| [google/uuid](https://github.com/google/uuid) | UUIDs | [BSD-3-Clause](https://github.com/google/uuid/blob/v1.6.0/LICENSE) |
| [gmsm](https://github.com/emmansun/gmsm) and [Go x/crypto](https://pkg.go.dev/golang.org/x/crypto) | Cryptographic algorithms | [MIT](https://github.com/emmansun/gmsm/blob/v0.44.1/LICENSE), [BSD-3-Clause](https://go.googlesource.com/crypto/+/refs/tags/v0.54.0/LICENSE) |
| [miniredis](https://github.com/alicebob/miniredis) | Redis unit tests | [MIT](https://github.com/alicebob/miniredis/blob/v2.38.0/LICENSE) |

Object storage signing and URL behavior reference the [Alibaba Cloud OSS signing specification](https://www.alibabacloud.com/help/en/oss/developer-reference/signature-version-4-recommend) and the [official Go SDK's CNAME URL handling](https://github.com/aliyun/aliyun-oss-go-sdk/blob/master/oss/conn.go) ([MIT](https://github.com/aliyun/aliyun-oss-go-sdk/blob/master/LICENSE)). See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for the parser's copyright notices and distribution scope.

## License

[MIT](LICENSE).

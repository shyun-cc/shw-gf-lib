# shw-gf-lib

[简体中文](./README.md) | [English](./README.en.md) | 繁體中文

面向 GoFrame v2 應用的 Go 元件庫，提供 HTTP 響應與中介軟體、會話、Redis 鎖、資料庫遷移、非同步任務、物件儲存，以及常見雲服務和微信 API 的用戶端。

## 安裝

需要 Go 1.25.7 或更新版本。在應用模組中執行：

```sh
go get github.com/shyun-cc/shw-gf-lib/uuidutil@latest
```

各元件可以按需引入。例如，使用 `uuidutil` 解析可為空的 UUID 字串：

```go
import "github.com/shyun-cc/shw-gf-lib/uuidutil"

// 空輸入返回零 UUID，格式錯誤返回 error。
id, err := uuidutil.Empty(input).Parse()
```

`migrate` 相依 Cgo 和 C 編譯器。預設建置提供常規遷移命令，開發建置標籤 `dev` 會額外啟用可清庫的開發命令。具體行為見 [遷移文件](docs/migrate.md)。

## 元件

| 用途 | 元件文件 |
| --- | --- |
| HTTP 與配置 | [exception](docs/exception.md)、[response](docs/response.md)、[middleware](docs/middleware.md)、[session](docs/session.md)、[settings](docs/settings.md)、[cfgsource](docs/cfgsource.md)、[pagination](docs/pagination.md) |
| 基礎工具 | [cryptox](docs/cryptox.md)、[logging](docs/logging.md)、[tracex](docs/tracex.md)、[events](docs/events.md)、[redislock](docs/redislock.md)、[seqnum](docs/seqnum.md)、[uuidutil](docs/uuidutil.md) |
| 資料與任務 | [migrate](docs/migrate.md)、[worker](docs/worker.md)、[task](docs/task.md)、[rabbitmq](docs/rabbitmq.md)、[storage](docs/storage.md)、[attachment](docs/attachment.md) |
| 雲服務 | [aliyun](docs/aliyun.md)、[sms](docs/sms.md)、[verifycode](docs/verifycode.md)、[express](docs/express.md)、[tencent](docs/tencent.md)、[region](docs/region.md) |
| 微信 | [wxapp](docs/wxapp.md)、[wxoa](docs/wxoa.md)、[wxpay](docs/wxpay.md)、[wecom](docs/wecom.md) |

應用透過元件的配置或構造引數注入供應商憑據。微信小程式示例見 [examples/wxapp/safe-password](examples/wxapp/safe-password)。

## 開發

在儲存庫根目錄執行：

```sh
go vet ./...
go test ./...
```

GitHub Actions 會檢查 Go 檔案格式並執行這兩項檢查。`tests/` 是單獨的模組，包含按建置標籤啟用的可選測試。其連線引數和執行方式見 [tests/doc.go](tests/doc.go)。

## 鳴謝與第三方版權

感謝以下專案及其貢獻者。本庫透過 Go 模組相依使用這些專案。

| 上游 | 用途 | 許可證 |
| --- | --- | --- |
| [GoFrame v2](https://github.com/gogf/gf) 及 Redis 介面卡 | HTTP、中介軟體、配置、日誌、資料訪問與 Redis | [MIT](https://github.com/gogf/gf/blob/v2.10.2/LICENSE) |
| [goose](https://github.com/pressly/goose) | 資料庫遷移執行 | [MIT](https://github.com/pressly/goose/blob/v3.27.3/LICENSE) |
| [pg_query_go](https://github.com/pganalyze/pg_query_go) 與 [PostgreSQL](https://www.postgresql.org/) | 透過 CGO 編譯 PostgreSQL 語法解析器並分析遷移 SQL | [BSD-3-Clause](LICENSES/pg_query_go.BSD-3-Clause.txt)、[PostgreSQL License](LICENSES/pg_query_go.PostgreSQL.txt) |
| [amqp091-go](https://github.com/rabbitmq/amqp091-go) | RabbitMQ 用戶端 | [BSD-2-Clause](https://github.com/rabbitmq/amqp091-go/blob/v1.14.0/LICENSE) |
| [robfig/cron](https://github.com/robfig/cron) | 定時任務 | [MIT](https://github.com/robfig/cron/blob/v3.0.1/LICENSE) |
| [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go) | 鏈路追蹤 | [Apache-2.0](https://github.com/open-telemetry/opentelemetry-go/blob/v1.44.0/LICENSE) |
| [google/uuid](https://github.com/google/uuid) | UUID | [BSD-3-Clause](https://github.com/google/uuid/blob/v1.6.0/LICENSE) |
| [gmsm](https://github.com/emmansun/gmsm) 和 [Go x/crypto](https://pkg.go.dev/golang.org/x/crypto) | 密碼學演算法實現 | [MIT](https://github.com/emmansun/gmsm/blob/v0.44.1/LICENSE)、[BSD-3-Clause](https://go.googlesource.com/crypto/+/refs/tags/v0.54.0/LICENSE) |
| [miniredis](https://github.com/alicebob/miniredis) | Redis 單元測試 | [MIT](https://github.com/alicebob/miniredis/blob/v2.38.0/LICENSE) |

物件儲存簽名及 URL 行為參考了[阿里雲 OSS 的簽名規範](https://www.alibabacloud.com/help/en/oss/developer-reference/signature-version-4-recommend)及[官方 Go SDK 的 CNAME URL 處理](https://github.com/aliyun/aliyun-oss-go-sdk/blob/master/oss/conn.go)（[MIT](https://github.com/aliyun/aliyun-oss-go-sdk/blob/master/LICENSE)）。解析器的版權宣告和散布範圍見 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 許可證

[MIT](LICENSE)。

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

## 许可证

[MIT](LICENSE)

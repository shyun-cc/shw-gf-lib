# 第三方版权声明（Third-Party Notices）

`migrate` 通过 Go 模块使用 [pg_query_go v6.2.5](https://github.com/pganalyze/pg_query_go/tree/v6.2.5)。该依赖包含 PostgreSQL 项目的解析器代码，应用在引入 `migrate` 并启用 CGO 构建时会编译该 C 代码。

| 上游作品 | 原始版权与许可证声明 |
| --- | --- |
| pg_query_go | [BSD-3-Clause，Lukas Fittl 与 Duboce Labs, Inc.（pganalyze）](LICENSES/pg_query_go.BSD-3-Clause.txt) |
| PostgreSQL 解析器 | [pg_query_go 随附的 PostgreSQL 版权与许可原文](LICENSES/pg_query_go.PostgreSQL.txt) |

在分发编译了该解析器的二进制时，随附以上两个声明。其他依赖及其版本见 `go.mod`，并适用其各自的许可证。

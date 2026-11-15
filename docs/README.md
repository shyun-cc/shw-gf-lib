# 文档站维护说明

本站是设计讨论稿；业务方案尚未确认、尚未实现。`content/` 是公开站点内容，本文说明独立构建与发布方式。

## 固定依赖

- PGSTY OINK **v1.5.1**，通过 Hugo Module 引入，版本与校验和提交在本目录 `go.mod`、`go.sum`。
- Hugo **Extended 0.168.0**、Go **1.27.3**、Git。使用 Hugo 内置 LibSass，无需 Node.js、npm 或外部 Sass。
- 文档模块与未来业务模块独立，不在仓库根目录建立业务依赖。

核验来源（2026-11-15）：[OINK 版本说明](https://github.com/pgsty/oink/releases/tag/v1.5.1)、[官方 starter 依赖](https://github.com/pgsty/oink-starter/blob/main/go.mod)、[单语言配置](https://github.com/pgsty/oink-starter/blob/main/examples/hugo.single.yaml)、[starter Pages 工作流](https://github.com/pgsty/oink-starter/blob/main/.github/workflows/github-pages.yaml)。本配置仅保留中文讨论文档，没有复制 starter 的示例业务内容。OINK 使用 Apache-2.0 许可证，主题由模块下载，保留上游许可文件；本仓库许可证未变更。

## 本地构建

安装上述固定版本并加入 PATH 后，在仓库根目录执行：

```sh
cd docs
export GOWORK=off
export HUGO_MODULE_WORKSPACE=off
go mod download github.com/pgsty/oink
go mod verify
hugo --gc --minify --environment production --printPathWarnings
```

产物为 `docs/public/`，已忽略，不提交。首次构建需要网络下载主题；保留 `go.sum` 供校验。上游 LibSass 弃用警告应保留可见，不使用 `--panicOnWarning`。预览执行 `hugo server`，访问终端报告的本地 URL。

## GitHub Pages

`.github/workflows/docs-pages.yml` 在 PR 中仅构建；主分支文档变更或主分支手动触发才上传 Pages 产物并部署。普通构建只有仓库读取权限，部署 job 使用 Pages 发布所需的 `pages: write` 与 `id-token: write`，不写仓库内容，不自动创建或修改仓库设置。

当前 `baseURL` 为项目站点地址 `https://shyun-cc.github.io/shw-gf-lib/`，包括仓库子路径。若之后使用自定义域名，应先更新配置并重新验证全部站内资源。

本稿没有启用线上站点，也没有修改 Pages 设置。审阅并合并后，维护者需在仓库 Settings → Pages 确认 Source 为 GitHub Actions，并确认环境发布规则与 Actions 可用。工作流未调用 `configure-pages` 自动创建站点或更改设置。第一次部署成功后，才将实际站点地址作为已上线链接发布。

## 内容审阅

新增页面时用“设计讨论稿／待确认／尚未实现”说明状态，使用内容相对链接或 Hugo `relref`，避免以 `/` 写死根域路径。发布前检查生成产物中是否出现私人聊天、秘密、内部标识或无授权公开的资料。公开事实与假设分开记录，不凭名称猜测产品需求。

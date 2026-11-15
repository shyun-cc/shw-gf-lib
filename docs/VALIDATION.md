# 本地验证记录

验证日期：2026-11-15。本文只记录文档站构建，不代表业务实现或线上部署通过。

- 基线：`fb27a18`（仅有 MIT LICENSE）；开始修改前已核验远端只有 `main`，没有既有 PR 或文档分支。
- 工具：Hugo Extended 0.168.0、Go 1.27.3、OINK v1.5.1。
- `go mod verify`：全部模块校验通过。
- `hugo --gc --minify --environment production --printPathWarnings`：退出码 0。Hugo 报告 Pages 19、Static files 33；产物含 13 个 HTML 文件。
- 解析所有生成 HTML 的本地 `href` 与 `src`：415 个引用存在，项目地址前缀均为 `/shw-gf-lib/`。
- 检查 CSS 的本地 `url()`：33 个资源引用存在。
- 首页正文可见，九个讨论索引／内容页进入本地搜索索引；所有搜索索引地址在产物中存在。
- 工作流 YAML 可解析，部署仅在主分支且非 PR 事件执行；PR 构建不获得 Pages 写入权限。
- `git diff --check` 通过；仅涉及文档、站点配置和 Pages 工作流。

保留的已知警告：上游 LibSass 弃用警告；OINK v1.5.1 官方仍要求使用该内置编译器。本次未抑制警告。

未完成：GitHub Actions 远程运行、浏览器交互式搜索验证、实际 Pages 部署。Pages 来源设置、环境规则与最终公开内容仍需维护者审阅；本次没有更改仓库设置，没有发布站点，也没有自动合并。

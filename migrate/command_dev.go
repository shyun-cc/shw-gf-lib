//go:build dev

// 本文件是 migrate 命令的开发构建分身（//go:build dev），仅在项目侧
// 以 -tags dev 编译时生效。
//
// 「默认即生产」契约：默认构建（无标签）编译的是 command_default.go
// 的纯增量形态；本分身额外提供 --fresh 清库重建能力，供开发/测试
// 环境快速重置数据库。与 command_default.go 的同名 NewCommand 按
// 构建标签二选一编译，项目侧挂载代码只有一份、不感知构建标签。
package migrate

import (
	"context"

	"github.com/gogf/gf/v2/os/gcmd"
)

// devRunner 开发构建命令所需的执行能力：增量迁移 + 全量清空重建。
// 方法全导出，*Migrator 天然满足（Fresh 位于 fresh_dev.go，同样仅
// dev 构建存在）；单测用 fake 实现即可验证分派行为，不触真库。
type devRunner interface {
	Up(context.Context) error
	Fresh(context.Context) error
}

// NewCommand 构造 migrate 命令（dev 构建）：解析 --fresh 参数分派
// Fresh（清库重建）或 Up（增量，行为与生产版一致）。命令执行返回的
// error 原样透传给 gcmd，不吞不改。
func NewCommand(r devRunner) *gcmd.Command {
	return &gcmd.Command{
		Name:  "migrate",
		Usage: "migrate [--fresh]",
		Brief: "执行数据库迁移（增量 up）；[--fresh] 清空重建（仅开发构建）",
		Arguments: []gcmd.Argument{
			{
				Name:   "fresh",
				Brief:  "先清空数据库（DROP SCHEMA）再从头执行全部迁移",
				Orphan: true,
			},
		},
		Func: func(ctx context.Context, parser *gcmd.Parser) error {
			// 判空：parser 为 nil（非命令行入口直接调用）
			// 时按无参数处理，走增量 up。
			if parser != nil {
				if opt := parser.GetOpt("fresh"); opt != nil {
					return r.Fresh(ctx)
				}
			}
			return r.Up(ctx)
		},
	}
}

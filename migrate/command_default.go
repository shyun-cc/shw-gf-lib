//go:build !dev

// 本文件是 migrate 命令的默认构建分身（//go:build !dev），即生产形态。
//
// 「默认即生产」契约：项目侧不加任何标签编译出的二进制就是生产形态，
// 纵深防御由编译器结构保证——本文件（以及整个默认构建）不含任何
// 清库（fresh）逻辑与参数解析，清库能力只存在于 -tags dev 构建的
// command_dev.go / fresh_dev.go。生产环境即使误传清库参数，也会被
// 当作未声明选项忽略，命令只执行普通增量 up。
//
// 与 command_dev.go 的同名 NewCommand 按构建标签二选一编译，项目侧
// 挂载代码只有一份、不感知构建标签（这是收编的核心价值）。
package migrate

import (
	"context"

	"github.com/gogf/gf/v2/os/gcmd"
)

// upRunner 默认构建命令所需的最小执行能力：增量迁移。方法全导出，
// *Migrator 天然满足；参数收窄为非导出接口，单测用 fake 即可验证
// 命令行为，不需要真数据库。
type upRunner interface {
	Up(context.Context) error
}

// NewCommand 构造 migrate 命令（默认构建 / 生产形态）：纯增量 up，
// 不识别任何参数。命令执行返回的 error 原样透传给 gcmd（由 gcmd
// 负责退出码与错误打印），不吞不改。
func NewCommand(r upRunner) *gcmd.Command {
	return &gcmd.Command{
		Name:  "migrate",
		Brief: "执行数据库迁移（增量 up）",
		Func: func(ctx context.Context, parser *gcmd.Parser) error {
			// 生产形态不解析任何参数（parser 仅由 gcmd 框架传入，
			// 此处刻意不读取），直接执行增量迁移。
			return r.Up(ctx)
		},
	}
}

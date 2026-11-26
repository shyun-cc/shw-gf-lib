//go:build dev

// 本文件实现 Migrator.Fresh（全量清空重建），仅 -tags dev 构建编译。
//
// 纵深防御核心：Fresh 方法本身位于 dev 构建文件——默认构建的
// *Migrator 没有该方法，清库代码物理不进生产二进制（CLI 层与库层
// 双重隔离）。默认构建即使被注入伪造的 --fresh 参数，也没有任何
// 代码路径能触达 DROP SCHEMA。
package migrate

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/frame/g"
)

// Fresh 全量清空重建：清空数据库 schema 后调 Up 从头应用全部迁移。
//
// 分派顺序：先方言分派、后取连接。switch 的 default 分支是纵深防御：
// 正常路径下未知方言已被 New 构造期拦截，此分支兜住「未来扩了枚举、
// 进了 New 的方言映射表（Up 可用）但 Fresh 漏实现」的失误，在触碰
// 数据库前明确拒绝，绝不静默跳过。
func (m *Migrator) Fresh(ctx context.Context) error {
	switch m.dialect {
	case DialectPostgres:
		g.Log().Info(ctx, "执行全量清空重建（--fresh）...")

		db, err := m.db()
		if err != nil {
			return fmt.Errorf("获取数据库连接失败: %w", err)
		}

		if _, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS public CASCADE"); err != nil {
			return fmt.Errorf("删除 schema 失败: %w", err)
		}
		if _, err := db.ExecContext(ctx, "CREATE SCHEMA public"); err != nil {
			return fmt.Errorf("创建 schema 失败: %w", err)
		}

		g.Log().Info(ctx, "schema 已清空，开始重建...")
		return m.Up(ctx)
	default:
		return fmt.Errorf("不支持的数据库方言（fresh）")
	}
}

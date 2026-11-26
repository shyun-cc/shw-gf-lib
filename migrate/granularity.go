// 迁移文件粒度判定（fail-closed）。
//
// 本文件实现「一个目标对象一个迁移文件」规约的机器判定：解析 goose
// 迁移文件取 Up 段 SQL，经 pg_query_go 解析为 PostgreSQL AST 后，按
// 语句节点类型提取其宿主对象（被 CREATE/ALTER/DROP/... 的那张表、那个
// 序列/类型/视图/函数/扩展/schema），要求整个 Up 段涉及的宿主对象集合
// 恰好 1 个。Down 段不参与任何判定（回滚脚本与正向粒度无关）。
//
// fail-closed 是本判定的灵魂：语句节点类型不在已映射清单内、或宿主
// 对象名提取失败时一律硬错误，绝不静默放行——宁可误拒合规文件（由
// 维护方扩映射）也不漏放不合规文件。识别清单因此刻意保守（如
// DropStmt 仅认 OBJECT_TABLE / OBJECT_SEQUENCE），扩清单必须同步补
// 对应宿主提取与测试。
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"

	pgquery "github.com/pganalyze/pg_query_go/v6"
)

// gooseAnnotationPrefix goose 注解行的前缀（trim 后以此为前缀的行是
// 注解行，如 "-- +goose Up" / "-- +goose StatementBegin"）。
const gooseAnnotationPrefix = "-- +goose"

// parseGooseFile 解析 goose 迁移文件原文，返回 Up 段纯 SQL 与全文校验和。
//
// Up 段截取规则："-- +goose Up" 注解行之后至 "-- +goose Down" 注解行或
// 文件尾；段内其余注解行（StatementBegin/StatementEnd 等）按行剔除，
// 保证交给 pg_query 的文本是纯 SQL。Down 段整体丢弃。文件无 Up 注解时
// 返回空 Up 段（由粒度判定报「空集合」，不在此处报错）。
//
// checksumHex 是含注解行在内的**原文全文** sha256 hex（64 字符）：校验
// 和锚定文件字节内容，与 Up 段截取无关。
func parseGooseFile(content string) (upSQL string, checksumHex string) {
	sum := sha256.Sum256([]byte(content))
	checksumHex = hex.EncodeToString(sum[:])

	var upLines []string
	inUp := false
	for _, line := range strings.Split(content, "\n") {
		cmd, isAnnotation := gooseAnnotationCommand(line)
		if isAnnotation {
			switch cmd {
			case "up":
				inUp = true
			case "down":
				// Down 段开始：其后内容全部丢弃，提前收尾。
				return strings.Join(upLines, "\n"), checksumHex
			}
			// 其余注解（StatementBegin/End、NO TRANSACTION 等）：剔除。
			continue
		}
		if inUp {
			upLines = append(upLines, line)
		}
	}
	return strings.Join(upLines, "\n"), checksumHex
}

// gooseAnnotationCommand 判断一行是否 goose 注解行，是则返回其命令名。
// 注解行 = trim 后以 "-- +goose" 开头且后接空白或行尾（大小写不敏感，
// "-- +gooseUp" 这类粘连写法不是注解）；命令名取注解的第一个字段并
// 小写化（"up" / "down" / "statementbegin" / ...）。取完整字段而非
// 前缀匹配，避免臆造注解（如 "UpSomething"）误判为 Up。
func gooseAnnotationCommand(line string) (cmd string, isAnnotation bool) {
	trimmed := strings.ToLower(strings.TrimSpace(line))
	if !strings.HasPrefix(trimmed, gooseAnnotationPrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, gooseAnnotationPrefix)
	if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
		return "", false
	}
	cmd = strings.TrimSpace(rest)
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		cmd = cmd[:i]
	}
	return cmd, true
}

// validateGranularity 校验单个迁移文件的粒度：Up 段涉及的宿主数据库
// 对象必须恰好 1 个。filename 仅用于错误文案定位，不参与解析。
func validateGranularity(filename, content string) error {
	upSQL, _ := parseGooseFile(content)

	tree, err := pgquery.Parse(upSQL)
	if err != nil {
		return fmt.Errorf("迁移文件解析失败: %s: %w", filename, err)
	}

	hosts := newHostSet()
	for _, raw := range tree.Stmts {
		if raw == nil || raw.Stmt == nil {
			// pg_query 正常不会产出空语句节点；防御性 fail-closed。
			return errUndeterminedHost(filename, nodeTypeName(nil))
		}

		names, ok := stmtHostNames(raw.Stmt)
		if !ok {
			// 类型在映射清单内但宿主名提取失败（如 CREATE SCHEMA 无
			// 名、DropStmt 的 removeType 未支持）——与白名单外同罪。
			return errUndeterminedHost(filename, nodeTypeName(raw.Stmt))
		}
		hosts.add(names...)
	}

	switch hosts.len() {
	case 0:
		return fmt.Errorf("迁移文件粒度不合规: %s（未包含任何针对数据库对象的语句；须一个目标对象一个文件）", filename)
	case 1:
		return nil
	default:
		return fmt.Errorf("迁移文件粒度不合规: %s（涉及多个对象: %s；须一个目标对象一个文件）",
			filename, strings.Join(hosts.ordered(), ", "))
	}
}

// errUndeterminedHost 语句类型无法判定宿主对象（白名单外节点，或已
// 识别节点上宿主名提取失败）的硬错误。文案要求项目侧反馈维护方扩映射。
func errUndeterminedHost(filename, nodeType string) error {
	return fmt.Errorf("迁移文件包含无法判定宿主对象的语句: %s（语句类型: %s；不支持该语句类型时向 gf-lib 维护方反馈）",
		filename, nodeType)
}

// stmtHostNames 提取一条语句的宿主对象名（按语句内出现序，可能多条，
// 如 DROP TABLE a, b）。第二返回值 false 表示该语句无法判定宿主：
// 节点类型在白名单外（default 分支），或虽识别类型但对象名提取不出。
//
// 三路分派：
//   - 宿主映射：提取对象名加入集合（单条语句多对象全部加入，天然触发
//     多对象拒绝）；
//   - 无宿主白名单：SET / BEGIN 等事务语句 / 纯 SELECT——忽略不进集合；
//   - 白名单之外：fail-closed 硬错误。
func stmtHostNames(stmt *pgquery.Node) (names []string, ok bool) {
	switch node := stmt.Node.(type) {
	// ---- 表 ----
	case *pgquery.Node_CreateStmt: // CREATE TABLE
		return hostsFromRangeVar(node.CreateStmt.GetRelation())
	case *pgquery.Node_AlterTableStmt: // ALTER TABLE / ALTER INDEX：宿主即 Relation
		return hostsFromRangeVar(node.AlterTableStmt.GetRelation())
	case *pgquery.Node_DropStmt: // DROP TABLE / DROP SEQUENCE（removeType 窄化见函数内）
		return hostsFromDropStmt(node.DropStmt)
	case *pgquery.Node_TruncateStmt: // TRUNCATE：表名列表（[]*Node 的 RangeVar wrapper）
		return hostsFromRangeVarNodes(node.TruncateStmt.GetRelations())

	// ---- 表的附属对象：宿主取所在表，非对象自身名 ----
	case *pgquery.Node_IndexStmt: // CREATE INDEX：宿主是索引所在表
		return hostsFromRangeVar(node.IndexStmt.GetRelation())
	case *pgquery.Node_CreateTrigStmt: // CREATE TRIGGER：宿主是触发器所在表
		return hostsFromRangeVar(node.CreateTrigStmt.GetRelation())

	// ---- DML：目标表 ----
	case *pgquery.Node_InsertStmt:
		return hostsFromRangeVar(node.InsertStmt.GetRelation())
	case *pgquery.Node_UpdateStmt:
		return hostsFromRangeVar(node.UpdateStmt.GetRelation())
	case *pgquery.Node_DeleteStmt:
		return hostsFromRangeVar(node.DeleteStmt.GetRelation())

	// ---- 序列 ----
	case *pgquery.Node_CreateSeqStmt:
		return hostsFromRangeVar(node.CreateSeqStmt.GetSequence())
	case *pgquery.Node_AlterSeqStmt:
		return hostsFromRangeVar(node.AlterSeqStmt.GetSequence())

	// ---- 类型 / 函数 / 扩展 ----
	case *pgquery.Node_CompositeTypeStmt: // CREATE TYPE ... AS (...)
		return hostsFromRangeVar(node.CompositeTypeStmt.GetTypevar())
	case *pgquery.Node_AlterEnumStmt: // ALTER TYPE ... ADD VALUE：类型名取名字链末段
		return hostsFromNameChain(node.AlterEnumStmt.GetTypeName())
	case *pgquery.Node_CreateFunctionStmt: // CREATE FUNCTION：函数名取名字链末段（无 schema 前缀时链长 1）
		return hostsFromNameChain(node.CreateFunctionStmt.GetFuncname())
	case *pgquery.Node_CreateExtensionStmt:
		if name := node.CreateExtensionStmt.GetExtname(); name != "" {
			return []string{name}, true
		}
		return nil, false

	// ---- 视图 / 物化视图 ----
	case *pgquery.Node_ViewStmt: // CREATE VIEW：宿主是视图自身
		return hostsFromRangeVar(node.ViewStmt.GetView())
	case *pgquery.Node_CreateTableAsStmt: // CREATE MATERIALIZED VIEW / CREATE TABLE AS：目标是 Into.Rel
		into := node.CreateTableAsStmt.GetInto()
		if into == nil {
			return nil, false
		}
		return hostsFromRangeVar(into.GetRel())

	// ---- 注释 / schema / 权限 ----
	case *pgquery.Node_CommentStmt: // COMMENT ON：对象名尽力提取
		return hostsFromCommentStmt(node.CommentStmt)
	case *pgquery.Node_CreateSchemaStmt:
		if name := node.CreateSchemaStmt.GetSchemaname(); name != "" {
			return []string{name}, true
		}
		return nil, false // CREATE SCHEMA AUTHORIZATION joe（无 schema 名）：无法命名宿主
	case *pgquery.Node_GrantStmt: // GRANT / REVOKE：受权对象列表尽力提取
		return hostsFromGrantStmt(node.GrantStmt)

	// ---- 无宿主白名单：忽略，不进集合 ----
	case *pgquery.Node_VariableSetStmt: // SET search_path = ... 等会话变量
		return nil, true
	case *pgquery.Node_TransactionStmt: // BEGIN / COMMIT / SAVEPOINT ...
		return nil, true
	case *pgquery.Node_SelectStmt:
		// 纯查询无宿主；但 SELECT ... INTO 会建表，目标表语义是宿主，
		// 提取不在本映射内——按 fail-closed 拒绝而非当纯查询忽略，
		// 否则它与另一对象的 DDL 同文件时会漏放行。
		if node.SelectStmt.GetIntoClause() != nil {
			return nil, false
		}
		return nil, true

	default:
		return nil, false
	}
}

// hostsFromRangeVar 提取单个 RangeVar 的对象名。
func hostsFromRangeVar(rv *pgquery.RangeVar) ([]string, bool) {
	if rv == nil || rv.GetRelname() == "" {
		return nil, false
	}
	return []string{rv.GetRelname()}, true
}

// hostsFromRangeVarNodes 提取 RangeVar wrapper 节点列表（[]*Node，元素
// 应为 RangeVar）的对象名，如 TRUNCATE t1, t2。
func hostsFromRangeVarNodes(nodes []*pgquery.Node) ([]string, bool) {
	if len(nodes) == 0 {
		return nil, false
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		name, ok := hostsFromRangeVar(n.GetRangeVar())
		if !ok {
			return nil, false
		}
		names = append(names, name[0])
	}
	return names, true
}

// hostsFromNameChain 提取名字链（[]*Node，元素应为 String 节点）的末段。
// DROP/函数名/类型名等限定名链取末段（对象自身名），与 RangeVar 只取
// relname 不含 schema 的口径一致。链空或含非 String 节点即提取失败。
func hostsFromNameChain(chain []*pgquery.Node) ([]string, bool) {
	if len(chain) == 0 {
		return nil, false
	}
	last := ""
	for _, item := range chain {
		s := item.GetString_()
		if s == nil || s.GetSval() == "" {
			return nil, false
		}
		last = s.GetSval()
	}
	return []string{last}, true
}

// hostsFromDropStmt 提取 DROP 语句的对象名列表。removeType 仅认
// OBJECT_TABLE / OBJECT_SEQUENCE（映射清单保守起步），其余（VIEW/
// INDEX/SCHEMA/...）视为未支持——fail-closed，由反馈扩映射。
func hostsFromDropStmt(stmt *pgquery.DropStmt) ([]string, bool) {
	switch stmt.GetRemoveType() {
	case pgquery.ObjectType_OBJECT_TABLE, pgquery.ObjectType_OBJECT_SEQUENCE:
	default:
		return nil, false
	}
	objects := stmt.GetObjects()
	if len(objects) == 0 {
		return nil, false
	}
	names := make([]string, 0, len(objects))
	for _, obj := range objects {
		name, ok := hostsFromNode(obj)
		if !ok {
			return nil, false
		}
		names = append(names, name[0])
	}
	return names, true
}

// hostsFromGrantStmt 提取 GRANT/REVOKE 的受权对象名列表。对象形态随
// objtype 变化（TABLE→RangeVar、SCHEMA/DATABASE→String、TYPE→List、
// ALL TABLES IN SCHEMA→schema 名的 String），统一交 hostsFromNode
// 尽力提取；列表为空或任一对象提取失败即 fail-closed。
func hostsFromGrantStmt(stmt *pgquery.GrantStmt) ([]string, bool) {
	objects := stmt.GetObjects()
	if len(objects) == 0 {
		return nil, false
	}
	names := make([]string, 0, len(objects))
	for _, obj := range objects {
		name, ok := hostsFromNode(obj)
		if !ok {
			return nil, false
		}
		names = append(names, name[0])
	}
	return names, true
}

// hostsFromCommentStmt 提取 COMMENT ON 的宿主对象名。列注释
// （COMMENT ON COLUMN t.c）的 object 名链是 [表限定名..., 列]，宿主
// 是被注释列所属的表（列是表的附属，与索引跟随宿主表同口径）——
// 取剔除末段（列）后的名链末段；其余对象类型交 hostsFromNode
// 尽力提取。
func hostsFromCommentStmt(stmt *pgquery.CommentStmt) ([]string, bool) {
	if stmt.GetObjtype() == pgquery.ObjectType_OBJECT_COLUMN {
		obj := stmt.GetObject()
		if obj == nil {
			return nil, false
		}
		items := obj.GetList().GetItems()
		// 合法列注释名链至少两段（表.列），限定形态 [schema, 表, 列]。
		if len(items) < 2 {
			return nil, false
		}
		return hostsFromNameChain(items[:len(items)-1])
	}
	return hostsFromNode(stmt.GetObject())
}

// hostsFromNode 从单个 Node 尽力提取对象名，覆盖 PG 对象引用的常见
// AST 形态：String（裸名字，SCHEMA/DATABASE/EXTENSION 等）、List
// （限定名链，取末段）、RangeVar（关系）、TypeName（类型名链取末段）、
// ObjectWithArgs（函数等带参对象，取 objname 链末段）。形态之外失败。
func hostsFromNode(n *pgquery.Node) ([]string, bool) {
	if n == nil {
		return nil, false
	}
	switch v := n.Node.(type) {
	case *pgquery.Node_String_:
		if sval := v.String_.GetSval(); sval != "" {
			return []string{sval}, true
		}
		return nil, false
	case *pgquery.Node_List:
		return hostsFromNameChain(v.List.GetItems())
	case *pgquery.Node_RangeVar:
		return hostsFromRangeVar(v.RangeVar)
	case *pgquery.Node_TypeName:
		return hostsFromNameChain(v.TypeName.GetNames())
	case *pgquery.Node_ObjectWithArgs:
		return hostsFromNameChain(v.ObjectWithArgs.GetObjname())
	default:
		return nil, false
	}
}

// nodeTypeName 取语句节点的 protobuf 消息类型名（如 "DoStmt"），用于
// 白名单外错误的文案。实现上取 oneof wrapper 类型名（*Node_DoStmt）
// 去掉 "Node_" 前缀，与 protobuf 消息名严格一致。
func nodeTypeName(n *pgquery.Node) string {
	if n == nil || n.Node == nil {
		return "<nil>"
	}
	return strings.TrimPrefix(reflect.TypeOf(n.Node).Elem().Name(), "Node_")
}

// hostSet 宿主对象名集合：保持首次出现序去重。对象名按 SQL 原样
// （大小写敏感、不做 PG 小写化归一）——判定口径只需「同名即同对象」，
// 迁移 SQL 由 goose 项目侧书写，同一文件内混用大小写引用同一对象的
// 写法不属于合规迁移该迁就的形态。
type hostSet struct {
	seen map[string]struct{}
	list []string
}

func newHostSet() *hostSet {
	return &hostSet{seen: make(map[string]struct{})}
}

func (s *hostSet) add(names ...string) {
	for _, name := range names {
		if _, ok := s.seen[name]; ok {
			continue
		}
		s.seen[name] = struct{}{}
		s.list = append(s.list, name)
	}
}

func (s *hostSet) len() int { return len(s.list) }

func (s *hostSet) ordered() []string { return s.list }

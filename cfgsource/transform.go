package cfgsource

import (
	"context"
	"sort"
	"strings"

	"github.com/gogf/gf/v2/frame/g"
)

// buildOverlayTree 将 Driver 的扁平点分键集合拼装为嵌套树：
// "oss.bucket" → {"oss": {"bucket": "..."}}，无点分纯名直接落顶层。
//
// 算法：键按字节序排序后依次插入树，保证结果与 map 迭代顺序无关。同一路径
// 既被浅键占为叶值又被深键展开为子树时，保留深键丢弃浅键并记 warning
// （同一对冲突仅告警一次）：
//   - 正向：插入深键（"a.b.c"）时前缀路径已被浅键（"a.b"）叶值占据——
//     丢弃浅键值、原位转为子树继续插入；
//   - 反向：插入浅键（"a.b"）时该路径已是深键子树——整键丢弃。
func buildOverlayTree(ctx context.Context, flat map[string]string) map[string]any {
	tree := make(map[string]any, len(flat))
	warned := make(map[string]bool, len(flat))
	keys := make([]string, 0, len(flat))
	for key := range flat {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		insertFlatKey(ctx, tree, "", key, strings.Split(key, "."), flat[key], warned)
	}
	return tree
}

// insertFlatKey 将单个扁平键的值沿 segments 逐级插入树节点 node。
// path 为 node 对应的完整键路径（根为空串），fullKey 为正在插入的原始扁平键，
// 两者用于告警定位与冲突去重。
func insertFlatKey(ctx context.Context, node map[string]any, path string, fullKey string, segments []string, value string, warned map[string]bool) {
	head := segments[0]
	childPath := head
	if path != "" {
		childPath = path + "." + head
	}
	if len(segments) == 1 {
		// 叶值落点已是深键子树（反向冲突）：整键丢弃，保留深键。
		if _, isSub := node[head].(map[string]any); isSub {
			warnConflict(ctx, warned, fullKey, childPath)
			return
		}
		node[head] = value
		return
	}
	child, isSub := node[head].(map[string]any)
	if !isSub {
		if node[head] != nil {
			// 前缀路径被浅键叶值占据（正向冲突）：丢弃浅值，原位转子树继续，保留深键。
			warnConflict(ctx, warned, childPath, fullKey)
		}
		child = make(map[string]any)
		node[head] = child
	}
	insertFlatKey(ctx, child, childPath, fullKey, segments[1:], value, warned)
}

// warnConflict 键冲突告警：浅键与同名子树二选一，保留深键丢弃浅键。
// shallow 为被丢弃的浅键、deep 为保留的深键（或子树前缀路径），
// warned 以冲突路径去重，同一对冲突每轮 transform 仅告警一次。
func warnConflict(ctx context.Context, warned map[string]bool, shallow string, deep string) {
	if warned[shallow] {
		return
	}
	warned[shallow] = true
	g.Log().Warningf(ctx, "cfgsource 键冲突: %q 与子树 %q 同名，保留深键丢弃浅键", shallow, deep)
}

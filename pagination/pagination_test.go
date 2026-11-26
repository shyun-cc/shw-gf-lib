package pagination

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOffset_FieldSet Offset JSON 字段恰为五字段契约
func TestOffset_FieldSet(t *testing.T) {
	p := NewOffset([]int{1, 2}, 2, 9, 100)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, field := range []string{`"items"`, `"curPage"`, `"perPage"`, `"lastPage"`, `"total"`} {
		if !strings.Contains(s, field) {
			t.Fatalf("缺字段 %s: %s", field, s)
		}
	}
	// 恰为五字段（数逗号+1）
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 5 {
		t.Fatalf("应恰五字段: %v", m)
	}
}

// TestOffset_LastPageComputation lastPage 三态：常规、perPage=0、单页
func TestOffset_LastPageComputation(t *testing.T) {
	cases := []struct {
		total, perPage, want int
	}{
		{100, 9, 12}, // ceil(100/9)=12
		{100, 0, 0},  // perPage=0 → 0
		{0, 10, 1},   // 空数据至少 1 页
		{5, 10, 1},   // 单页
		{10, 10, 1},  // 恰满一页
		{11, 10, 2},  // 溢出一行
	}
	for _, c := range cases {
		p := NewOffset([]int{}, 1, c.perPage, c.total)
		if got := p.LastPage(); got != c.want {
			t.Errorf("total=%d perPage=%d: lastPage=%d want %d", c.total, c.perPage, got, c.want)
		}
	}

	// 序列化值一致
	p := NewOffset([]int{}, 1, 9, 100)
	raw, _ := json.Marshal(p)
	if !strings.Contains(string(raw), `"lastPage":12`) {
		t.Fatalf("序列化 lastPage 应为 12: %s", raw)
	}
}

// TestCursor_FieldSetAndNullMarker Cursor 三字段与 nextMarker null 语义
func TestCursor_FieldSetAndNullMarker(t *testing.T) {
	// 有下一页
	marker := "eyJpZCI6MTAwfQ"
	c := NewCursor([]string{"a"}, 20, &marker)
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("应恰三字段: %v", m)
	}
	if m["nextMarker"] != marker {
		t.Fatalf("nextMarker 应带值: %v", m["nextMarker"])
	}

	// 末尾：nil → JSON null（字段存在且为 null，非缺字段）
	end := NewCursor([]string{"z"}, 20, nil)
	raw2, _ := json.Marshal(end)
	if !strings.Contains(string(raw2), `"nextMarker":null`) {
		t.Fatalf("末尾 nextMarker 应为 null: %s", raw2)
	}
}

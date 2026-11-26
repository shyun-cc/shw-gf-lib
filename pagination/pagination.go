// Package pagination 统一分页响应契约（对应 PHP AbstractOffsetPaginatedResDTO / AbstractCursorPaginatedResDTO）。
//
// Offset：items/curPage/perPage/lastPage/total 五字段，lastPage 基类锁定计算；
// Cursor：items/perPage/nextMarker 三字段，nextMarker 为 null 即末尾。
package pagination

import (
	"encoding/json"
	"math"
)

// Offset 偏移量分页响应（页码型）
type Offset[T any] struct {
	Items   []T `json:"items"`
	CurPage int `json:"curPage"`
	PerPage int `json:"perPage"`
	Total   int `json:"total"`
}

// NewOffset 构造（lastPage 由 MarshalJSON 锁定计算，不可注入）
func NewOffset[T any](items []T, curPage, perPage, total int) Offset[T] {
	return Offset[T]{Items: items, CurPage: curPage, PerPage: perPage, Total: total}
}

// LastPage 锁定计算：perPage>0 时 max(1, ceil(total/perPage))；perPage=0 时 0
func (p Offset[T]) LastPage() int {
	if p.PerPage <= 0 {
		return 0
	}
	last := int(math.Ceil(float64(p.Total) / float64(p.PerPage)))
	if last < 1 {
		return 1
	}
	return last
}

// MarshalJSON 输出恰为五字段契约（lastPage 计算值）
func (p Offset[T]) MarshalJSON() ([]byte, error) {
	items := p.Items
	if items == nil {
		items = []T{}
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return []byte(`{"items":` + string(raw) +
		`,"curPage":` + itoa(p.CurPage) +
		`,"perPage":` + itoa(p.PerPage) +
		`,"lastPage":` + itoa(p.LastPage()) +
		`,"total":` + itoa(p.Total) + `}`), nil
}

// Cursor 游标分页响应
type Cursor[T any] struct {
	Items      []T     `json:"items"`
	PerPage    int     `json:"perPage"`
	NextMarker *string `json:"nextMarker"` // nil → JSON null 即末尾
}

// NewCursor 构造；nextMarker 传 nil 表示末尾
func NewCursor[T any](items []T, perPage int, nextMarker *string) Cursor[T] {
	return Cursor[T]{Items: items, PerPage: perPage, NextMarker: nextMarker}
}

func itoa(n int) string {
	return json.Number(intToString(n)).String()
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

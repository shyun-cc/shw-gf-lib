// Package uuidutil 可空 uuid 落库/入参转换。
package uuidutil

import (
	"database/sql/driver"
	"encoding/json"

	"github.com/google/uuid"
)

// Empty 字符串形态的 UUID 入参包装，避免将空串直接转换为 UUID 数组。
// 对新建零值 DTO，JSON 缺省/null/空串都保持空值；复用 DTO 遵循 Go 标准 JSON 语义。
// UUID/Ptr/Value 为历史兼容入口，会将非法串当空值。
// 新业务应使用 Parse/ParsePtr 区分非法输入，必填校验由调用方执行。
type Empty string

// UUID 转本体 uuid.UUID（空/非法 → uuid.Nil）。
func (e Empty) UUID() uuid.UUID {
	if e == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(string(e))
	if err != nil {
		return uuid.Nil
	}
	return id
}

// Ptr 转指针（空 → nil——可空外键语义）。
func (e Empty) Ptr() *uuid.UUID {
	if e == "" {
		return nil
	}
	id := e.UUID()
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// Value driver.Valuer（uuid.Nil → NULL——配合可空列）。
func (e Empty) Value() (driver.Value, error) {
	id := e.UUID()
	if id == uuid.Nil {
		return nil, nil
	}
	return id.String(), nil
}

// MarshalJSON 出口按字符串直出（空 → 空串）。
func (e Empty) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(e))
}

// NullUUIDPtr *uuid.UUID → interface{}（nil 指针/uuid.Nil → nil=SQL NULL）。
func NullUUIDPtr(p *uuid.UUID) interface{} {
	if p == nil || *p == uuid.Nil {
		return nil
	}
	return p
}

// NullUUID uuid.UUID → interface{}（uuid.Nil → nil=SQL NULL）。
func NullUUID(id uuid.UUID) interface{} {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// Parse 空串返回 uuid.Nil；非空非法值返回错误，不静默视为清空。
// 使用 google/uuid.Parse 的格式兼容范围；是否必填、是否限 UUIDv7 由项目校验。
func (e Empty) Parse() (uuid.UUID, error) {
	if e == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(string(e))
}

// ParsePtr 空串或零 UUID 返回 nil；非空非法值返回错误。
func (e Empty) ParsePtr() (*uuid.UUID, error) {
	id, err := e.Parse()
	if err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, nil
	}
	return &id, nil
}

package uuidutil

import (
	"encoding/json"
	"testing"

	"github.com/gogf/gf/v2/util/gconv"
	"github.com/google/uuid"
)

func TestEmptyConversion(t *testing.T) {
	valid := "019bd055-8617-7000-8000-000000000001"
	want := uuid.MustParse(valid)
	for _, tc := range []struct {
		input string
		empty bool
		bad   bool
	}{{"", true, false}, {uuid.Nil.String(), true, false}, {"invalid", true, true}, {valid, false, false}} {
		t.Run(tc.input, func(t *testing.T) {
			e := Empty(tc.input)
			id := e.UUID()
			if (id == uuid.Nil) != tc.empty {
				t.Fatal(id)
			}
			if (e.Ptr() == nil) != tc.empty {
				t.Fatal(e.Ptr())
			}
			value, err := e.Value()
			if err != nil || (value == nil) != tc.empty {
				t.Fatalf("%v %v", value, err)
			}
			if !tc.empty && (id != want || value != valid) {
				t.Fatal(id, value)
			}
			parsed, err := e.Parse()
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			ptr, err := e.ParsePtr()
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			if !tc.bad {
				if parsed != id || (ptr == nil) != tc.empty {
					t.Fatal(parsed, ptr)
				}
				if ptr != nil && *ptr != want {
					t.Fatal(*ptr)
				}
			}
		})
	}
}
func TestInputConversion(t *testing.T) {
	for _, raw := range []string{`{}`, `{"id":""}`, `{"id":null}`, `{"id":"invalid"}`, `{"id":"019bd055-8617-7000-8000-000000000001"}`} {
		var input struct {
			ID Empty `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatal(err)
		}
		var converted struct {
			ID Empty `json:"id"`
		}
		if err := gconv.Struct(data, &converted); err != nil {
			t.Fatal(err)
		}
		if converted.ID != input.ID {
			t.Fatalf("JSON and gconv disagree: %q %q", converted.ID, input.ID)
		}
	}
	b, err := json.Marshal(Empty(""))
	if err != nil || string(b) != `""` {
		t.Fatalf("%s %v", b, err)
	}
}
func TestNullUUID(t *testing.T) {
	zero := uuid.Nil
	id := uuid.MustParse("019bd055-8617-7000-8000-000000000001")
	if NullUUID(zero) != nil || NullUUIDPtr(nil) != nil || NullUUIDPtr(&zero) != nil {
		t.Fatal("zero must be SQL NULL")
	}
	if NullUUID(id) != id || NullUUIDPtr(&id) != &id {
		t.Fatal("valid UUID must be retained")
	}
}

package task

import (
	"strings"
	"testing"
)

func TestValidSplitBatch(t *testing.T) {
	if err := validBatch(SplitBatch{Children: []ChildSpec{{Key: "a", Payload: []byte(`{}`)}, {Key: "b"}}, Cursor: []byte(`{"after":"b"}`)}); err != nil {
		t.Fatal(err)
	}
	bad := []SplitBatch{
		{Children: []ChildSpec{{Key: "a"}, {Key: "a"}}},
		{Children: []ChildSpec{{Key: ""}}},
		{Children: []ChildSpec{{Key: strings.Repeat("k", 257)}}},
		{Children: []ChildSpec{{Key: "a", Payload: []byte("{")}}},
		{Cursor: []byte("not-json")},
		{Children: make([]ChildSpec, 1001)},
	}
	for i, b := range bad {
		if validBatch(b) == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

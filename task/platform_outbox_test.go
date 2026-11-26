package task

import (
	"errors"
	"testing"
)

func TestDispatchMessageRoundTrip(t *testing.T) {
	m := DispatchMessage{Version: DispatchProtocol, Namespace: "ns", TaskID: "018f3c2e-0000-7000-8000-000000000001", Generation: 3}
	got, err := DecodeDispatch(m.Encode())
	if err != nil || got != m {
		t.Fatal(got, err)
	}
	if m.MessageID() != "018f3c2e-0000-7000-8000-000000000001:3" {
		t.Fatal(m.MessageID())
	}
	bad := [][]byte{
		[]byte("{"),
		DispatchMessage{Version: 2, Namespace: "ns", TaskID: m.TaskID, Generation: 1}.Encode(),
		DispatchMessage{Version: 1, TaskID: m.TaskID, Generation: 1}.Encode(),
		DispatchMessage{Version: 1, Namespace: "ns", TaskID: "x", Generation: 1}.Encode(),
		DispatchMessage{Version: 1, Namespace: "ns", TaskID: m.TaskID}.Encode(),
		make([]byte, 5000),
	}
	for i, b := range bad {
		if _, err := DecodeDispatch(b); !errors.Is(err, ErrPoisonMessage) {
			t.Fatalf("case %d accepted: %v", i, err)
		}
	}
}

func TestClassOfDefault(t *testing.T) {
	if classOf(Definition{}) != DefaultClass || classOf(Definition{Class: "batch"}) != "batch" {
		t.Fatal("class fallback")
	}
}

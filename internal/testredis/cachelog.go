package testredis

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"
)

// CaptureLog captures synchronous output from the actual default GoFrame logger.
// Callers must not run in parallel because the logger is process-global.
func CaptureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	logger := g.Log()
	previous := logger.GetConfig()
	t.Cleanup(func() {
		if err := logger.SetConfig(previous); err != nil {
			t.Errorf("restore logger configuration: %v", err)
		}
	})
	var output bytes.Buffer
	config := glog.DefaultConfig()
	config.Writer = &output
	config.Flags = 0
	config.HeaderPrint = false
	config.LevelPrint = false
	config.StdoutPrint = false
	config.StStatus = 0
	if err := logger.SetConfig(config); err != nil {
		t.Fatalf("capture logger output: %v", err)
	}
	return &output
}

// RejectCacheSET rejects only writes to key, keeping lock and cache-read commands
// operational. The real GoFrame Redis adapter still constructs the command error.
func RejectCacheSET(t *testing.T, mr *miniredis.Miniredis, key string) *atomic.Int32 {
	t.Helper()
	var rejected atomic.Int32
	mr.Server().SetPreHook(func(peer *server.Peer, command string, args ...string) bool {
		if strings.EqualFold(command, "SET") && len(args) > 0 && args[0] == key {
			rejected.Add(1)
			peer.WriteError("ERR credential-cache-write-failure")
			return true
		}
		return false
	})
	t.Cleanup(func() { mr.Server().SetPreHook(nil) })
	return &rejected
}

// RequireSafeWarning checks the entire emitted warning, including the absence of
// credentials, Redis command arguments and the underlying driver error text.
func RequireSafeWarning(t *testing.T, output, warning string, forbidden ...string) {
	t.Helper()
	for _, value := range append(forbidden, "SET", " EX ", "7000", "credential-cache-write-failure") {
		if strings.Contains(output, value) {
			t.Errorf("cache warning contains forbidden value %q", value)
		}
	}
	if strings.TrimSpace(output) != warning {
		t.Errorf("cache warning = %q, want only %q", output, warning)
	}
}

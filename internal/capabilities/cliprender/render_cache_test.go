package cliprender

import (
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestLogCacheLookupFailureSkipsMisses pins CR-19-15: a plain miss is the
// normal path and stays silent, while a broken cache produces exactly one debug
// line so the fail-open degradation is visible.
func TestLogCacheLookupFailureSkipsMisses(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	log := zap.New(core)

	logCacheLookupFailure(log, "handler", strings.Repeat("a", 64), ErrCacheMiss)
	logCacheLookupFailure(log, "handler", strings.Repeat("a", 64), nil)
	if logs.Len() != 0 {
		t.Fatalf("a cache miss must not log, got %v", logs.All())
	}

	boom := errors.New("media DB unavailable")
	logCacheLookupFailure(log, "worker", strings.Repeat("b", 64), boom)
	if logs.Len() != 1 {
		t.Fatalf("a real cache error must log once, got %d entries", logs.Len())
	}
	entry := logs.All()[0]
	if entry.Level != zap.DebugLevel {
		t.Fatalf("cache error logged at %v, want DEBUG", entry.Level)
	}
	fields := entry.ContextMap()
	if fields["scope"] != "worker" {
		t.Fatalf("scope = %v, want worker", fields["scope"])
	}
	if fields["fingerprint"] != strings.Repeat("b", 16) {
		t.Fatalf("fingerprint = %v, want the 16-char prefix", fields["fingerprint"])
	}
}

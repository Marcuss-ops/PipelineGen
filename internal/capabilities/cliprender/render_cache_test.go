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

// TestRenderCacheDDLOwnsOnlyTheWholeClipTable pins CR-19-16: the boot schema
// creates the whole-clip cache (plus its asset index) and nothing else. The
// segmented chunk table had no producer and no reader, so recreating it at
// every boot was pure drift.
func TestRenderCacheDDLOwnsOnlyTheWholeClipTable(t *testing.T) {
	if !strings.Contains(renderCacheDDL, "clip_render_cache (") {
		t.Fatal("DDL must create clip_render_cache")
	}
	if !strings.Contains(renderCacheDDL, "idx_clip_render_cache_asset") {
		t.Fatal("DDL must create the clip_render_cache asset index")
	}
	if strings.Contains(renderCacheDDL, "chunk") {
		t.Fatalf("DDL must not mention the removed chunk cache:\n%s", renderCacheDDL)
	}
}

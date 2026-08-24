package defaults

import "testing"

// TestDefaultVideoConfig_RoundTrip anchors the canonical HC-7 SSOT.
// A regression here is observable in EVERY downstream consumer
// (the script-generation pipeline + /api/scripts responses); the
// round-trip check keeps the SSOT stable per Check 39 anti-reintro.
func TestDefaultVideoConfig_RoundTrip(t *testing.T) {
	cfg := DefaultVideoConfig()

	// ChunkDuration was a hard-coded literal
	// (internal/platform/config/video.go:64) before HC-7 — any drift
	// here breaks the video-assembly cadence and is the regression
	// surface that justified pkg/defaults/video.go as the SSOT.
	if cfg.ChunkDuration != 25 {
		t.Fatalf("ChunkDuration: got %d, want 25", cfg.ChunkDuration)
	}

	// EffectsDir was a magic string in the stock pipeline
	// (internal/capabilities/assets/providers/stock/...) — same drift
	// class as ChunkDuration: silently break the path lookup.
	if cfg.EffectsDir != "effects/" {
		t.Fatalf("EffectsDir: got %q, want %q", cfg.EffectsDir, "effects/")
	}

	// ParentFieldName was a literal duplicated in the script-history
	// HTTP transport (DRIFT-23-4). Keeping it stable means readers
	// (and historical API consumers) keep working.
	if cfg.ParentFieldName != "parent_id" {
		t.Fatalf("ParentFieldName: got %q, want %q", cfg.ParentFieldName, "parent_id")
	}
}

// TestDefaultVideoConfig_NotZero guards against accidentally
// returning a zero-value VideoConfig (e.g. if the function body
// is reduced to `return VideoConfig{}` during a refactor). A
// zero-value config would silently disable video assembly without
// any diagnostic — the previous ChunkDuration: 25 + EffectsDir:
// "effects/" + ParentFieldName: "parent_id" combinations would
// match the missing boundary marker Check 39 catches.
func TestDefaultVideoConfig_NotZero(t *testing.T) {
	cfg := DefaultVideoConfig()
	if cfg == (VideoConfig{}) {
		t.Fatalf("DefaultVideoConfig returned a zero-value VideoConfig; regression in SSOT")
	}
}

// TestDefaultVideoConfig_ReturnsCopyPerCall documents that two
// consecutive calls do NOT share mutable state. The two returned
// structs must be independent (Go passes VideoConfig by value,
// so this is trivially true). Anchors a behavioural invariant
// for the type-keyed layout: a future "return *VideoConfig" refactor
// would break this test, and is the kind of regression the test
// surface is meant to expose.
func TestDefaultVideoConfig_ReturnsCopyPerCall(t *testing.T) {
	a := DefaultVideoConfig()
	a.ChunkDuration = 99

	b := DefaultVideoConfig()
	if b.ChunkDuration != 25 {
		t.Fatalf("leak across calls: b.ChunkDuration = %d, want 25", b.ChunkDuration)
	}
}

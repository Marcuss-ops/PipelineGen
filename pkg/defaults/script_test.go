package defaults

import "testing"

// TestDefaultScriptConfig_RoundTrip anchors the canonical
// DRIFT-DEFAULTS-SCRIPT SSOT. A regression here breaks every
// script-generation entry point (single-script, batch, regenerate,
// lessons.estimateChapterDuration).
func TestDefaultScriptConfig_RoundTrip(t *testing.T) {
	cfg := DefaultScriptConfig()

	if cfg.WordsPerMinute != 140 {
		t.Fatalf("WordsPerMinute: got %d, want 140", cfg.WordsPerMinute)
	}
	if cfg.DefaultDuration != 600 {
		t.Fatalf("DefaultDuration: got %d, want 600", cfg.DefaultDuration)
	}
	if cfg.DefaultLanguage != "it" {
		t.Fatalf("DefaultLanguage: got %q, want %q", cfg.DefaultLanguage, "it")
	}
	if cfg.DefaultTemplate != "documentary" {
		t.Fatalf("DefaultTemplate: got %q, want %q", cfg.DefaultTemplate, "documentary")
	}
	if cfg.DefaultTone != "documentary" {
		t.Fatalf("DefaultTone: got %q, want %q", cfg.DefaultTone, "documentary")
	}
}

// TestDefaultScriptConfig_NotZero guards against accidentally
// returning a zero-value ScriptConfig. A zero-value config would
// silently collapse WordsPerMinute to 0 (every duration estimate
// would compute 0 or trigger a divide-by-zero), DefaultDuration to
// 0 (every script would target 0 seconds), and the language /
// template / tone strings to "" (LLM would fail to route).
func TestDefaultScriptConfig_NotZero(t *testing.T) {
	cfg := DefaultScriptConfig()
	if cfg == (ScriptConfig{}) {
		t.Fatalf("DefaultScriptConfig returned a zero-value ScriptConfig; regression in SSOT")
	}
}

// TestDefaultScriptConfig_ReturnsCopyPerCall documents that two
// consecutive calls do NOT share mutable state.
func TestDefaultScriptConfig_ReturnsCopyPerCall(t *testing.T) {
	a := DefaultScriptConfig()
	a.WordsPerMinute = 200
	a.DefaultLanguage = "es"

	b := DefaultScriptConfig()
	if b.WordsPerMinute != 140 {
		t.Fatalf("leak across calls: b.WordsPerMinute = %d, want 140", b.WordsPerMinute)
	}
	if b.DefaultLanguage != "it" {
		t.Fatalf("leak across calls: b.DefaultLanguage = %q, want %q", b.DefaultLanguage, "it")
	}
}

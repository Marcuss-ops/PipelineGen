package defaults

import "testing"

// TestDefaultMediaConfig_RoundTrip anchors the canonical
// DRIFT-DEFAULTS-MEDIA SSOT. A regression here breaks every
// search/curate limit-bound check (QDRANT-004 spec) and the score
// floor.
func TestDefaultMediaConfig_RoundTrip(t *testing.T) {
	cfg := DefaultMediaConfig()

	if cfg.SearchDefaultLimit != 20 {
		t.Fatalf("SearchDefaultLimit: got %d, want 20", cfg.SearchDefaultLimit)
	}
	if cfg.SearchMaxLimit != 100 {
		t.Fatalf("SearchMaxLimit: got %d, want 100", cfg.SearchMaxLimit)
	}
	if cfg.CurateDefaultLimit != 10 {
		t.Fatalf("CurateDefaultLimit: got %d, want 10", cfg.CurateDefaultLimit)
	}
	if cfg.CurateMaxLimit != 50 {
		t.Fatalf("CurateMaxLimit: got %d, want 50", cfg.CurateMaxLimit)
	}
	if cfg.DefaultScore != 0.50 {
		t.Fatalf("DefaultScore: got %v, want 0.50", cfg.DefaultScore)
	}
}

// TestDefaultMediaConfig_NotZero guards against accidentally
// returning a zero-value MediaConfig (e.g. if the function body is
// reduced to `return MediaConfig{}` during a refactor). A
// zero-value config would silently disable the search/curate
// limit bounds AND collapse DefaultScore to 0 (which would drop
// every hit pre-hydration).
func TestDefaultMediaConfig_NotZero(t *testing.T) {
	cfg := DefaultMediaConfig()
	if cfg == (MediaConfig{}) {
		t.Fatalf("DefaultMediaConfig returned a zero-value MediaConfig; regression in SSOT")
	}
}

// TestDefaultMediaConfig_ReturnsCopyPerCall documents that two
// consecutive calls do NOT share mutable state.
func TestDefaultMediaConfig_ReturnsCopyPerCall(t *testing.T) {
	a := DefaultMediaConfig()
	a.SearchDefaultLimit = 999
	a.DefaultScore = 0.99

	b := DefaultMediaConfig()
	if b.SearchDefaultLimit != 20 {
		t.Fatalf("leak across calls: b.SearchDefaultLimit = %d, want 20", b.SearchDefaultLimit)
	}
	if b.DefaultScore != 0.50 {
		t.Fatalf("leak across calls: b.DefaultScore = %v, want 0.50", b.DefaultScore)
	}
}

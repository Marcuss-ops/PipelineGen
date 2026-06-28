package defaults

import "testing"

// TestDefaultYouTubeConfig_RoundTrip anchors the canonical
// DRIFT-DEFAULTS-YOUTUBE SSOT. A regression here breaks every
// channel-monitor upsert (channels.Default policy) and the
// "general" fallback string at 5+ call sites.
func TestDefaultYouTubeConfig_RoundTrip(t *testing.T) {
	cfg := DefaultYouTubeConfig()

	if cfg.FallbackCategory != "general" {
		t.Fatalf("FallbackCategory: got %q, want %q", cfg.FallbackCategory, "general")
	}
	if cfg.MaxSegments != 2 {
		t.Fatalf("MaxSegments: got %d, want 2", cfg.MaxSegments)
	}
	if cfg.MaxClipDuration != 60 {
		t.Fatalf("MaxClipDuration: got %d, want 60", cfg.MaxClipDuration)
	}
	if cfg.MinSemanticScore != 60 {
		t.Fatalf("MinSemanticScore: got %d, want 60", cfg.MinSemanticScore)
	}
	if cfg.MaxVideosPerRun != 3 {
		t.Fatalf("MaxVideosPerRun: got %d, want 3", cfg.MaxVideosPerRun)
	}
	if cfg.CheckInterval != "7d" {
		t.Fatalf("CheckInterval: got %q, want %q", cfg.CheckInterval, "7d")
	}
	if cfg.Priority != 2 {
		t.Fatalf("Priority: got %d, want 2", cfg.Priority)
	}
	if cfg.PlaylistEnd != -1 {
		t.Fatalf("PlaylistEnd: got %d, want -1", cfg.PlaylistEnd)
	}
}

// TestDefaultYouTubeConfig_NotZero guards against accidentally
// returning a zero-value YouTubeConfig. A zero-value config would
// silently collapse FallbackCategory to "" (every classifier call
// returns empty), MaxSegments to 0 (channel monitor produces no
// segments), and PlaylistEnd to 0 (different SQL semantics than the
// legacy -1 "use global" sentinel).
func TestDefaultYouTubeConfig_NotZero(t *testing.T) {
	cfg := DefaultYouTubeConfig()
	if cfg == (YouTubeConfig{}) {
		t.Fatalf("DefaultYouTubeConfig returned a zero-value YouTubeConfig; regression in SSOT")
	}
}

// TestDefaultYouTubeConfig_ReturnsCopyPerCall documents that two
// consecutive calls do NOT share mutable state.
func TestDefaultYouTubeConfig_ReturnsCopyPerCall(t *testing.T) {
	a := DefaultYouTubeConfig()
	a.FallbackCategory = "other"
	a.MaxSegments = 99

	b := DefaultYouTubeConfig()
	if b.FallbackCategory != "general" {
		t.Fatalf("leak across calls: b.FallbackCategory = %q, want %q", b.FallbackCategory, "general")
	}
	if b.MaxSegments != 2 {
		t.Fatalf("leak across calls: b.MaxSegments = %d, want 2", b.MaxSegments)
	}
}

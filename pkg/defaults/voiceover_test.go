package defaults

import "testing"

// TestDefaultVoiceoverConfig_RoundTrip anchors the canonical
// DRIFT-DEFAULTS-VOICEOVER SSOT. A regression here breaks every
// voiceover BatchRequest construction (filename template, default
// language, default strategy).
//
// The 3 user-facing fields are checked. The request-ID format
// (literal "vo_" prefix + 6-hex random suffix) is intentionally
// NOT covered here — that is an internal detail of
// voiceover/types.go::buildRequestID, not a user-facing default.
func TestDefaultVoiceoverConfig_RoundTrip(t *testing.T) {
	cfg := DefaultVoiceoverConfig()

	if cfg.DefaultFilenameTemplate != "{slug}_{lang}.mp3" {
		t.Fatalf("DefaultFilenameTemplate: got %q, want %q", cfg.DefaultFilenameTemplate, "{slug}_{lang}.mp3")
	}
	if cfg.DefaultStrategy != "verify" {
		t.Fatalf("DefaultStrategy: got %q, want %q", cfg.DefaultStrategy, "verify")
	}
	if cfg.DefaultLanguage != "en" {
		t.Fatalf("DefaultLanguage: got %q, want %q", cfg.DefaultLanguage, "en")
	}
}

// TestDefaultVoiceoverConfig_NotZero guards against accidentally
// returning a zero-value VoiceoverConfig (e.g. if the function body
// is reduced to `return VoiceoverConfig{}` during a refactor). A
// zero-value config would silently default every BatchRequest to
// empty strings and disable voiceover generation without any
// diagnostic.
func TestDefaultVoiceoverConfig_NotZero(t *testing.T) {
	cfg := DefaultVoiceoverConfig()
	if cfg == (VoiceoverConfig{}) {
		t.Fatalf("DefaultVoiceoverConfig returned a zero-value VoiceoverConfig; regression in SSOT")
	}
}

// TestDefaultVoiceoverConfig_ReturnsCopyPerCall documents that two
// consecutive calls do NOT share mutable state. Go passes the
// struct by value, so this is trivially true; the test anchors the
// invariant against a future "return *VoiceoverConfig" refactor.
func TestDefaultVoiceoverConfig_ReturnsCopyPerCall(t *testing.T) {
	a := DefaultVoiceoverConfig()
	a.DefaultLanguage = "es"
	a.DefaultStrategy = "replace"

	b := DefaultVoiceoverConfig()
	if b.DefaultLanguage != "en" {
		t.Fatalf("leak across calls: b.DefaultLanguage = %q, want %q", b.DefaultLanguage, "en")
	}
	if b.DefaultStrategy != "verify" {
		t.Fatalf("leak across calls: b.DefaultStrategy = %q, want %q", b.DefaultStrategy, "verify")
	}
}

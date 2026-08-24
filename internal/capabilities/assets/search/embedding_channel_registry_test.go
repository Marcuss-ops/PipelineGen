// Package search — TDD contract tests for the canonical
// EmbeddingChannelRegistry port surface
// (internal/capabilities/assets/search/ports.go). The contract is the
// godlike/06 SSOT for the multi-channel embedding vocabulary; the
// godlike/07 typed-error contract is locked pinning the 3 sentinels.
//
// PR-EMBEDDING-CHANNEL-REGISTRY (July 2026, deadline 2026-08-01):
// the semantic backend (search_backend_semantic.go) consumes this
// port instead of inlining 5-model switch logic. New channel
// encoders (SigLIP-text for cross-modal visual, CLAP-text for audio,
// per PR-CROSS-MODAL-TEXT-TO-VISUAL) plug in at composition root
// without backend changes.
package assets

import (
	"errors"
	"strings"
	"testing"
)

// ── Canonical channel names — godlike/06 SSOT ────────────────

// TestChromaticChannelNames pins the closed set of canonical channel
// names the registry MUST recognize. Per godlike/06 one-canonical-
// owner-per-fact: changing these names is a wire-level break (Qdrant
// vector names match these constants 1:1). Editing the test alone
// without coordinating across the codebase MUST fail.
func TestChromaticChannelNames(t *testing.T) {
	want := []struct {
		constant, value string
	}{
		{ChannelText, "text"},
		{ChannelTranscript, "transcript"},
		{ChannelVisual, "visual"},
		{ChannelAudio, "audio"},
		{ChannelSparse, "bm25_text"},
	}
	for _, w := range want {
		if w.constant != w.value {
			t.Errorf("chromatic channel constant drift: %s != %s (godlike/06 SSOT violation)",
				w.constant, w.value)
		}
	}
}

// TestCanonicalChannelNames pins CanonicalChannelNames() to the closed
// 5-channel set. Same-content-as-consts assertion. The function MUST
// return all 5 canonical channels in canonical semantic-of-source order
// (text-first).
func TestCanonicalChannelNames(t *testing.T) {
	got := CanonicalChannelNames()
	want := []string{ChannelText, ChannelTranscript, ChannelVisual, ChannelAudio, ChannelSparse}
	if len(got) != len(want) {
		t.Fatalf("CanonicalChannelNames: want %d channels, got %d (%v)", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("CanonicalChannelNames[%d]: want %q, got %q", i, w, got[i])
		}
	}
}

// TestIsKnownChannelExhaustive explores the full closed set:
// every canonical name returns true, every other input returns
// false (including the godlike/07 programming-error case: an empty
// channel name is NOT a known channel — it must surface
// ErrChannelUnknown at registry-call time, not silently default).
func TestIsKnownChannelExhaustive(t *testing.T) {
	for _, name := range CanonicalChannelNames() {
		if !IsKnownChannel(name) {
			t.Errorf("IsKnownChannel(%q) = false, want true (canonical channel MUST be known)", name)
		}
	}

	// Empty string is NOT known — registry MUST return ErrChannelUnknown
	// at call time rather than silently defaulting.
	if IsKnownChannel("") {
		t.Error("IsKnownChannel(\"\") = true, want false (empty is not a known channel; registry fails closed)")
	}

	// Off-vocabulary inputs (case-sensitive matching; godlike/06 SSOT):
	// "TEXT" / "Text" / "Textual" / "transcripts" / "vision" / "audio_query"
	// / "bm25" / "BM25_TEXT" MUST all return false. The registry MUST
	// route mismatches through ErrChannelUnknown so operator dashboards
	// surface the misconfiguration cleanly.
	off := []string{
		"TEXT", "Text", "Textual",
		"transcripts", // plural off-by-one common typo
		"vision",      // near-miss for visual
		"audio_query", // vocabulary drift
		"bm25",        // drops the _text suffix
		"BM25_TEXT",   // case mismatch on the wire name
		"unknown",
	}
	for _, name := range off {
		if IsKnownChannel(name) {
			t.Errorf("IsKnownChannel(%q) = true, want false (off-vocabulary MUST fail closed)", name)
		}
	}
}

// ── Typed-error contract — godlike/07 typed errors ───────────

// TestSentinelsAreDistinct pins that the 3 typed sentinels are
// different errors.Is probes (no shared-implementation cross-talk).
// godlike/07 requires the error chain to be unique per failure mode
// so callers can dispatch on errors.Is without ambiguity.
func TestSentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrChannelUnknown, ErrChannelNotConfigured) {
		t.Error("ErrChannelUnknown MUST not be errors.Is-equivalent to ErrChannelNotConfigured (godlike/07 typed-error contract)")
	}
	if errors.Is(ErrChannelNotConfigured, ErrChannelNotApplicable) {
		t.Error("ErrChannelNotConfigured MUST not be errors.Is-equivalent to ErrChannelNotApplicable (godlike/07 typed-error contract)")
	}
	if errors.Is(ErrChannelUnknown, ErrChannelNotApplicable) {
		t.Error("ErrChannelUnknown MUST not be errors.Is-equivalent to ErrChannelNotApplicable (godlike/07 typed-error contract)")
	}
}

// TestSentinelMessages pin the documented channel error message
// surface so operator dashboards' log-scrapers can match keywords
// without depending on the struct payload. The "search:" prefix
// is the convention used by every other sentinel in this package
// (ErrAlreadyRegistered, ErrFrozen, ErrNilBackend) — error
// consistency matters for log-grep scripts.
func TestSentinelMessages(t *testing.T) {
	cases := []struct {
		sentinel error
		keyword  string
	}{
		{ErrChannelUnknown, "unknown embedding channel"},
		{ErrChannelNotConfigured, "channel recognized but no adapter wired"},
		{ErrChannelNotApplicable, "channel does not support text-query encoding"},
	}
	for _, c := range cases {
		if !strings.Contains(c.sentinel.Error(), c.keyword) {
			t.Errorf("sentinel %v: message %q does not contain %q (log-scraper compat broken)",
				c.sentinel, c.sentinel.Error(), c.keyword)
		}
	}
}

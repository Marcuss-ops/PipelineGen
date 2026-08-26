// Package dto — TDD coverage for the language-helpers surface. Phase 1c
// Commit 2/4 (June 2026). All tests use testify/assert + require to match
// the existing metadata_test.go style for consistency across the dto
// package.
//
// Coverage shape:
//   - NormalizeLanguages (6 tests): lowercase + trim + dedupe + order
//     preservation + case-insensitive dedupe.
//   - BuildMetadataLanguages: only caller-supplied languages are returned.
package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeLanguages_EmptyInputReturnsEmpty pins the canonical
// nil-safe no-allocation contract — empty input must produce empty
// output (not [""] and not [nil]).
func TestNormalizeLanguages_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := NormalizeLanguages([]string{})
	assert.Empty(t, got, "empty input must return empty output")
}

// TestNormalizeLanguages_TrimsWhitespace pins the trim step — entries
// that are pure whitespace MUST be dropped from the output (the
// invariant that the canonical BuildMetadataLanguages prepend pattern
// depends on).
//
// Use distinct non-control ASCII tokens in the assertion path so that
// the trim step's behavior is unambiguous.
func TestNormalizeLanguages_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	in := []string{
		"  en  ", // leading/trailing spaces → "en"
		"\t",     // pure tab character → ""
		" ",      // pure space character → ""
		"  fr  ", // leading/trailing spaces → "fr"
	}
	got := NormalizeLanguages(in)
	assert.Equal(t, []string{"en", "fr"}, got, "pure-whitespace entries are dropped after trim")
}

// TestNormalizeLanguages_Lowercases input — Phase 1c Commit 2/4
// user-spec delta from the pre-Phase-1c impl (which did trim+dedupe
// only). Locks the lowercase canonical form so callers can pass
// user-style uppercase variants and still get ISO 639-1 output.
func TestNormalizeLanguages_Lowercases(t *testing.T) {
	t.Parallel()
	in := []string{"EN", "It", "FR", "es"}
	got := NormalizeLanguages(in)
	assert.Equal(t, []string{"en", "it", "fr", "es"}, got, "lowercase invariant")
}

// TestNormalizeLanguages_Dedupes pins the dedupe invariant that
// BuildMetadataLanguages' prepend-then-normalize idiom depends on —
// ["en", "fr", "en"] must collapse to ["en", "fr"] so the
// canonical "English always first" contract produces no duplicate.
func TestNormalizeLanguages_Dedupes(t *testing.T) {
	t.Parallel()
	in := []string{"en", "fr", "en", "it", "fr"}
	got := NormalizeLanguages(in)
	assert.Equal(t, []string{"en", "fr", "it"}, got, "dedupe invariant")
}

// TestNormalizeLanguages_PreservesOrder pins the order-preservation
// invariant — input order is the canonical output order after
// transforms. The user's spec pinned this implicitly: "with English
// always first" is satisfied by the BuildMetadataLanguages prepend
// pattern, and the dedupe step must NOT reorder.
func TestNormalizeLanguages_PreservesOrder(t *testing.T) {
	t.Parallel()
	in := []string{"fr", "it", "en", "de", "fr"}
	got := NormalizeLanguages(in)
	assert.Equal(t, []string{"fr", "it", "en", "de"}, got, "order preservation")
}

// TestNormalizeLanguages_CaseInsensitiveDuplicates pins the
// case-insensitive dedupe: "EN" and "en" must collapse to a single
// "en" entry (this was the canonical scenario that motivated the
// lowercase folding step in Commit 2/4 — operators passing
// user-sensitive-language-toggle variants were getting duplicate
// downstream metadata generation tasks).
func TestNormalizeLanguages_CaseInsensitiveDuplicates(t *testing.T) {
	t.Parallel()
	in := []string{"EN", "en", "En", "eN", "EY", "ey"}
	got := NormalizeLanguages(in)
	assert.Equal(t, []string{"en", "ey"}, got, "case-insensitive dedupe")
}

// TestBuildMetadataLanguages_EmptyPayloadRemainsEmpty pins the fail-closed
// contract: omitted languages must not trigger an invented English job.
func TestBuildMetadataLanguages_EmptyPayloadRemainsEmpty(t *testing.T) {
	t.Parallel()
	got := BuildMetadataLanguages([]string{})
	require.Empty(t, got, "empty payload must yield no languages")
}

// TestBuildMetadataLanguages_PreservesCallerOrder pins that the output is
// exactly the normalized caller order; no implicit English entry is added.
func TestBuildMetadataLanguages_PreservesCallerOrder(t *testing.T) {
	t.Parallel()
	got := BuildMetadataLanguages([]string{"fr", "it"})
	assert.Equal(t, []string{"fr", "it"}, got, "only caller languages are retained")
}

// TestBuildMetadataLanguages_CollapsesENToLowercaseEn pins the
// user-supplied uppercase "EN" → canonical-output lowercase "en"
// invariant — without it, the metadata generator would attempt to
// translate English twice (once for the prepended + once for the user
// "EN") and emit duplicate VideoMetadata rows.
func TestBuildMetadataLanguages_CollapsesENToLowercaseEn(t *testing.T) {
	t.Parallel()
	got := BuildMetadataLanguages([]string{"EN", "fr"})
	require.Len(t, got, 2, "EN must collapse with the prepended en (no duplicate)")
	assert.Equal(t, []string{"en", "fr"}, got, "EN → en and fr in canonical form")
}

// TestBuildMetadataLanguages_DedupesDuplicateEN pins the
// caller-supplied-duplicate invariant — ["en", "en", "fr"] must
// collapse to ["en", "fr"] so the downstream metadata generator does
// not emit two redundant English VideoMetadata rows.
func TestBuildMetadataLanguages_DedupesDuplicateEN(t *testing.T) {
	t.Parallel()
	got := BuildMetadataLanguages([]string{"en", "en", "fr"})
	assert.Equal(t, []string{"en", "fr"}, got, "duplicate en must be deduped")
}

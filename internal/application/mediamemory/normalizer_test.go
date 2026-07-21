// Package mediamemory — normalizer_test.go pins the canonical
// PhraseFingerprint equality contract surface-equivalent phrases
// MUST produce the same SHA256 fingerprint) and the 6-step
// normalization pipeline (trim → NFC → casefold → whitespace
// collapse → terminal-punct-strip → fingerprint).
//
// godlike/06 SSOT: this test file is the canonical regression pin
// for normalizer.go. Drift from the expected canonical form (e.g.
// a refactor that makes "Maya" lowercase only on the first
// character) surfaces here as a cache-fragmentation failure at
// production reading time. CI gates on `go test ./internal/...`.
//
// godlike/07 NO-FAKE-AVAILABILITY: the test asserts ERRORS via
// errors.Is, never via string-match. A wrapped ErrInvalidPhrase
// from Normalize returns true here; a wrapped-but-renamed sentinel
// returns false (forward-prevention).
package mediamemory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPhraseFingerprintEquality is the canonical Fase 1.3 pin:
// surface-equivalent phrases MUST produce the same PhraseFingerprint.
//
// The two surface forms
//
//	"I Maya costruirono grandi città"
//	"i maya costruirono grandi città"
//
// share the same SHA256(language + normalized_phrase +
// visual_intent_version) because the canonical normalizer applies
// locale-invariant case-fold + whitespace collapse + terminal-
// punctuation strip BEFORE the hash. Failure here means the
// VisualResolver's Level 0 cache will miss across surface forms
// that should always match.
func TestPhraseFingerprintEquality(t *testing.T) {
	n := NewDefaultNormalizer("")
	ctx := context.Background()

	upper, err := n.Normalize(ctx, "I Maya costruirono grandi città", "it")
	require.NoError(t, err)
	lower, err := n.Normalize(ctx, "i maya costruirono grandi città", "it")
	require.NoError(t, err)

	assert.Equal(t,
		upper.PhraseFingerprint, lower.PhraseFingerprint,
		"PhraseFingerprint MUST match across case variants (canonical SSOT)",
	)
	assert.Equal(t,
		upper.NormalizedText, lower.NormalizedText,
		"NormalizedText MUST match across case variants",
	)
	// CanonicalText preserves the original casing for audit trail
	// (godlike/06 SSOT: the canonical SQL row keeps the surface
	// form so operators can grep / debug by what was input).
	assert.NotEqual(t,
		upper.CanonicalText, lower.CanonicalText,
		"CanonicalText preserves surface-form casing for audit",
	)
}

// TestPhraseFingerprintLanguageIsolation: same surface text but
// different language produces a DIFFERENT fingerprint. The
// language field is part of the SSOT hash input, so a
// Gi-Celsius-vs-English false-positive is impossible.
func TestPhraseFingerprintLanguageIsolation(t *testing.T) {
	n := NewDefaultNormalizer("")
	c, err := n.Normalize(context.Background(), "hello", "en")
	require.NoError(t, err)
	i, err := n.Normalize(context.Background(), "hello", "it")
	require.NoError(t, err)
	assert.NotEqual(t, c.PhraseFingerprint, i.PhraseFingerprint,
		"language is part of the SSOT hash; cross-language collisions must NOT happen",
	)
}

// TestPhraseFingerprintVisualIntentVersionsIsolated: a custom
// intent version produces a DIFFERENT fingerprint even when text +
// language are identical. Bumping the version invalidates the
// Level 0 cache cleanly (forward-pointer to Phase-2 embedding
// versioning + cache migration).
func TestPhraseFingerprintVisualIntentVersionsIsolated(t *testing.T) {
	v1 := NewDefaultNormalizer("v1")
	v2 := NewDefaultNormalizer("v2")
	c1, err := v1.Normalize(context.Background(), "I Maya costruirono grandi città", "it")
	require.NoError(t, err)
	c2, err := v2.Normalize(context.Background(), "I Maya costruirono grandi città", "it")
	require.NoError(t, err)
	assert.NotEqual(t, c1.PhraseFingerprint, c2.PhraseFingerprint,
		"visual_intent_version is part of the SSOT hash; cache invalidates cleanly across version bumps",
	)
}

// TestNormalizeTrimsSurroundingWhitespace ensures TrailingLeading
// whitespace does NOT bleed into the fingerprint.
func TestNormalizeTrimsSurroundingWhitespace(t *testing.T) {
	n := NewDefaultNormalizer("")
	a, err := n.Normalize(context.Background(), "  maya civilization  ", "en")
	require.NoError(t, err)
	b, err := n.Normalize(context.Background(), "maya civilization", "en")
	require.NoError(t, err)
	assert.Equal(t, a.PhraseFingerprint, b.PhraseFingerprint)
	assert.Equal(t, a.NormalizedText, b.NormalizedText)
}

// TestNormalizeNFCCollapsesDiacritics: the canonical precomposed
// form `é` (U+00E9) and the decomposed form `e\u0301` must produce
// the same fingerprint (Unicode NFC canonicalization).
func TestNormalizeNFCCollapsesDiacritics(t *testing.T) {
	n := NewDefaultNormalizer("")
	a, err := n.Normalize(context.Background(), "caf\u00e9", "fr") // é precomposed
	require.NoError(t, err)
	b, err := n.Normalize(context.Background(), "cafe\u0301", "fr") // e + combining acute
	require.NoError(t, err)
	assert.Equal(t, a.PhraseFingerprint, b.PhraseFingerprint,
		"NFC canonicalization should collapse é forms to the same fingerprint",
	)
	assert.Equal(t, "caf\u00e9", b.NormalizedText,
		"Output of Normalize should be NFC-canonical precomposed form",
	)
}

// TestNormalizeCollapsesInternalWhitespace.
func TestNormalizeCollapsesInternalWhitespace(t *testing.T) {
	n := NewDefaultNormalizer("")
	got, err := n.Normalize(context.Background(), "hello\n\tworld   today", "en")
	require.NoError(t, err)
	assert.Equal(t, "hello world today", got.NormalizedText,
		"runs of any whitespace collapse to single ASCII space; tabs/newlines/NBSP all coalesce",
	)
}

// TestNormalizeStripsTerminalPunctuation: trailing . , ; : ! ?
// removed; internal punctuation preserved.
func TestNormalizeStripsTerminalPunctuation(t *testing.T) {
	n := NewDefaultNormalizer("")
	cases := []struct{ in, want string }{
		{"hello.", "hello"},
		{"hello!!", "hello"},
		{"hello?", "hello"},
		{"hello,", "hello"},
		{"hello:", "hello"},
		{"hello.", "hello"},
		{"hello world.", "hello world"},
	}
	for _, c := range cases {
		got, err := n.Normalize(context.Background(), c.in, "en")
		require.NoError(t, err, c.in)
		assert.Equal(t, c.want, got.NormalizedText, c.in)
	}
}

// TestNormalizePreservesInternalPunctuation: "u.s.a." types stay
// intact (the canonical normalizer is conservative in Phase 1.x;
// a future NormalizationStrategy may collapse them).
func TestNormalizePreservesInternalPunctuation(t *testing.T) {
	n := NewDefaultNormalizer("")
	got, err := n.Normalize(context.Background(), "u.s.a.", "en")
	require.NoError(t, err)
	assert.Equal(t, "u.s.a", got.NormalizedText,
		"trailing '.' stripped, internal '.' preserved",
	)
}

// TestNormalizeLanguageLowercasedTrimmed: language field is
// canonical lowercase trimmed.
func TestNormalizeLanguageLowercasedTrimmed(t *testing.T) {
	n := NewDefaultNormalizer("")
	got, err := n.Normalize(context.Background(), "phrase", "  EN  ")
	require.NoError(t, err)
	assert.Equal(t, "en", got.Language,
		"language is canonical lowercase trimmed (the SSOT key)",
	)
}

// TestNormalizeEmptyPhraseReturnsErrInvalidPhrase.
func TestNormalizeEmptyPhraseReturnsErrInvalidPhrase(t *testing.T) {
	n := NewDefaultNormalizer("")
	cases := []string{"", "   ", "\n\n\t"}
	for _, in := range cases {
		_, err := n.Normalize(context.Background(), in, "en")
		require.Error(t, err, in)
		assert.True(t, errors.Is(err, ErrInvalidPhrase),
			"empty/trailing-whitespace phrase MUST wrap ErrInvalidPhrase (got %T: %v)", err, err)
	}
}

// TestNormalizeEmptyLanguageReturnsErrInvalidPhrase.
func TestNormalizeEmptyLanguageReturnsErrInvalidPhrase(t *testing.T) {
	n := NewDefaultNormalizer("")
	cases := []string{"", "  ", "\t"}
	for _, lang := range cases {
		_, err := n.Normalize(context.Background(), "valid phrase", lang)
		require.Error(t, err, lang)
		assert.True(t, errors.Is(err, ErrInvalidPhrase),
			"empty language MUST wrap ErrInvalidPhrase (got %T: %v)", err, err)
	}
}

// TestNormalizeEmptyAfterStripReturnsErrInvalidPhrase: a phrase
// composed entirely of stripped terminal punctuation (e.g. "...")
// cannot be normalized to empty — godlike/07 fail-closed.
func TestNormalizeEmptyAfterStripReturnsErrInvalidPhrase(t *testing.T) {
	n := NewDefaultNormalizer("")
	_, err := n.Normalize(context.Background(), "...", "en")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidPhrase),
		"phrase of purely terminal punctuation MUST wrap ErrInvalidPhrase after strip")
}

// TestFingerprintDirect: Fingerprint() called with the same
// (language, normalizedText) inputs must produce the same SHA256
// regardless of how the caller obtained the normalizedText.
// (Forward-pointer to VisualResolver::Level 0 cache lookup path.)
func TestFingerprintDirect(t *testing.T) {
	n := NewDefaultNormalizer("")
	a := n.Fingerprint("it", "hello world")
	b := n.Fingerprint("it", "hello world")
	require.Equal(t, a, b)
	// SHA256 hex output has exactly 64 chars.
	assert.Equal(t, 64, len(a), "SHA256 hex output is 64 chars")
	// Same inputs precisely map to one output (determinism).
	assert.NotEqual(t,
		n.Fingerprint("en", "hello world"), a,
		"language drift must break sha256",
	)
}

// TestNewDefaultNormalizerEmptyFallsBackToCanonicalVersion: the
// constructor pins an empty intentVersion to the canonical
// VisualIntentVersion constant.
func TestNewDefaultNormalizerEmptyFallsBackToCanonicalVersion(t *testing.T) {
	constA := NewDefaultNormalizer("")
	constB := NewDefaultNormalizer("v1")
	gotA, err := constA.Normalize(context.Background(), "hello", "en")
	require.NoError(t, err)
	gotB, err := constB.Normalize(context.Background(), "hello", "en")
	require.NoError(t, err)
	assert.Equal(t, gotA.PhraseFingerprint, gotB.PhraseFingerprint,
		"empty version arg MUST fall back to VisualIntentVersion SSOT",
	)
	assert.True(t, strings.HasPrefix(gotA.PhraseFingerprint, "") /* sanity */)
	// visual_intent_version is non-empty.
	assert.NotEmpty(t, VisualIntentVersion)
}

// TestNormalizeAllCapsLowercased: locale-invariant case-fold.
func TestNormalizeAllCapsLowercased(t *testing.T) {
	n := NewDefaultNormalizer("")
	got, err := n.Normalize(context.Background(), "FOO BAR", "en")
	require.NoError(t, err)
	assert.Equal(t, "foo bar", got.NormalizedText)
}

// TestNormalizeMixedCaseMayaCanonical: a torture test combining
// casing + trailing period + multiple spaces.
func TestNormalizeMixedCaseMayaCanonical(t *testing.T) {
	n := NewDefaultNormalizer("")
	cases := []string{
		"I Maya costruirono grandi città",
		"i Maya Costruirono Grandi Città",
		"  I   Maya   costruirono   grandi   città.  ",
		"i maya costruirono grandi città.",
		"I maya costruirono grandi città",
	}
	var firstFp string
	for i, c := range cases {
		got, err := n.Normalize(context.Background(), c, "it")
		require.NoError(t, err, "case %d: %q", i, c)
		if i == 0 {
			firstFp = got.PhraseFingerprint
			continue
		}
		assert.Equal(t, firstFp, got.PhraseFingerprint,
			"all canonical-equivalent surface forms MUST yield the same fingerprint; case %d (%q) breaks equality",
			i, c,
		)
	}
}

// Compile-time assertion: defaultNormalizer is not nil
// (catches accidental typo on the wrapper path).
var _ Normalizer = NewDefaultNormalizer("")

// Package mediamemory — normalizer.go is the canonical SSOT for the
// phrase-fingerprint and normalization surface.
//
// godlike/06 SSOT (Phase 1.x hard rule from the architecture doc):
// two surface-equivalent phrases MUST produce the same
// PhraseFingerprint. Concretely:
//
//	"I Maya costruirono grandi città"
//	"i maya costruirono grandi città"
//
// both yield SHA256(language + normalized_phrase + visual_intent_version).
// This is the Level 0 hot path that VisualResolver consults before
// any fan-out to Qdrant or external providers.
//
// godlike/06 SSOT (one canonical owner per fact): the canonical
// normalization algorithm lives in this file. Any code that needs
// to canonicalize a phrase imports Normalizer.Normalize here —
// no parallel implementations exist (no maya_normalizer.go,
// no boxing_normalizer.go, ...).
//
// godlike/07 NO-FAKE-AVAILABILITY: invalid input (empty phrase,
// unsupported language) wraps ErrInvalidPhrase via %w so callers
// probe via errors.Is.
package mediamemory

import (
	"context"
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// VisualIntentVersion is the canonical version stamp of the
// visual-intent schema. The PhraseFingerprint incorporates it so
// re-indexing on a new visual-intent version does NOT COLLIDE with
// old fingerprints (forward-pointer to Phase 2 embedding versioning).
//
// bump this constant whenever the VisualIntent wire shape changes.
const VisualIntentVersion = "v1"

// ── Normalizer port ────────────────────────────────────────────────

// Normalizer is the canonical port that produces (concept,
// fingerprint) from raw phrase text. Concrete impl lives in this
// file (default canon-normalizer). Composition root may register
// additional NormalizationStrategy implementations via the
// StrategyRegistry when a project requires a custom preprocessor
// (e.g. whisper-de-transcribed text).
type Normalizer interface {
	// Normalize produces a MediaConcept-shaped record populated with
	// text-only fields. ConceptType / CreatedAt / UpdatedAt are
	// filled by the caller (ConceptRepository.Upsert) once it has
	// validated uniqueness.
	Normalize(ctx context.Context, text, language string) (MediaConcept, error)

	// Fingerprint is the canonical SHA256-based hash. Exposed
	// because VisualResolver needs the fingerprint separately when
	// pre-checking the Level 0 cache.
	Fingerprint(language, normalizedText string) string
}

// ── Default implementation (skeleton) ─────────────────────────────

// defaultNormalizer is the canonical canon-normalizer. Phase 1.x
// wires it; advanced text pre-processing (transcript cleanup,
// OCR error correction, ...) is layered behind NormalizationStrategy
// in registry.go so a project can plug in a custom preprocessor
// without touching this file.
type defaultNormalizer struct {
	intentVersion string
}

// NewDefaultNormalizer returns the canonical implementation.
// intentVersion is normally VisualIntentVersion; tests may pin
// it to detect regressions.
func NewDefaultNormalizer(intentVersion string) Normalizer {
	if intentVersion == "" {
		intentVersion = VisualIntentVersion
	}
	return &defaultNormalizer{intentVersion: intentVersion}
}

// Compile-time assertion: defaultNormalizer satisfies Normalizer.
var _ Normalizer = (*defaultNormalizer)(nil)

// Normalize applies the canonical normalization pipeline:
//
//  1. trim surrounding whitespace (asymmetric tolerance: internal
//     whitespace is preserved — see godlike/06 SSOT note);
//  2. canonical Unicode NFC (multi-byte composition, prevents
//     "café" vs "cafe\u0301" mismatch);
//  3. lowercase fold (locale-invariant);
//  4. collapse runs of internal whitespace to a single space
//     (the SSOT for "phrase equality");
//  5. drop terminal punctuation (. , ; : ! ?) so an unpunctuated
//     surface phrase still matches a punctuated one;
//  6. compute PhraseFingerprint.
//
// godlike/07 NO-FAKE-AVAILABILITY: empty text → ErrInvalidPhrase.
// Empty language → ErrInvalidPhrase. The fingerprint is computed
// from BOTH inputs so cross-language collisions are impossible.
func (n *defaultNormalizer) Normalize(_ context.Context, text, language string) (MediaConcept, error) {
	text = strings.TrimSpace(text)
	language = strings.TrimSpace(strings.ToLower(language))

	if text == "" {
		return MediaConcept{}, wrapInvalid("phrase is empty after TrimSpace")
	}
	if language == "" {
		return MediaConcept{}, wrapInvalid("language is empty (required for fingerprint SSOT)")
	}

	normalized := canonicalizeWhitespace(lowercaseNFC(text))
	normalized = stripTerminalPunctuation(normalized)

	if normalized == "" {
		return MediaConcept{}, wrapInvalid("phrase is empty after stripping terminal punctuation")
	}

	return MediaConcept{
		Language:          language,
		CanonicalText:     text,
		NormalizedText:    normalized,
		PhraseFingerprint: n.Fingerprint(language, normalized),
		// ConceptType / IDs / Timestamps are filled by the caller.
	}, nil
}

// Fingerprint returns SHA256(language + ":" + normalizedText + ":" +
// intentVersion). The ":" delimiter is part of the SSOT (forward-
// pointer to a clipview-style shared constant if a sister package
// starts needing the same hash).
func (n *defaultNormalizer) Fingerprint(language, normalizedText string) string {
	sum := digest.SHA256Bytes([]byte(language + ":" + normalizedText + ":" + n.intentVersion))
	return sum
}

// ── Helpers (lowercase helpers, exported for sibling tests) ───────

// lowercaseNFC applies Unicode NFC + casefold.
func lowercaseNFC(s string) string {
	s = norm.NFC.String(s)
	return strings.Map(lowerRune, s)
}

// lowerRune maps an uppercase rune to lowercase. Locale-invariant
// (we deliberately do NOT use strings.ToLower which respects
// locale — this is a SSOT for cross-language equality).
func lowerRune(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.To(unicode.LowerCase, r)
	}
	return r
}

// canonicalizeWhitespace collapses runs of whitespace to a single
// ASCII space. Tabs, newlines, NBSP (U+00A0) are all collapsed.
func canonicalizeWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return b.String()
}

// stripTerminalPunctuation removes trailing ASCII punctuation
// (. , ; : ! ?). Internal punctuation is preserved so
// "U.S.A." stays "u.s.a." (a future preprocessor may collapse it
// via NormalizationStrategy, but the canonical normalizer keeps
// it conservative in Phase 1.x).
func stripTerminalPunctuation(s string) string {
	for len(s) > 0 {
		r := rune(s[len(s)-1])
		switch r {
		case '.', ',', ';', ':', '!', '?':
			s = s[:len(s)-1]
			continue
		default:
			return s
		}
	}
	return s
}

// wrapInvalid is a tiny helper so call-sites stay one line each.
func wrapInvalid(reason string) error {
	return errors.Join(ErrInvalidPhrase, errors.New(reason))
}

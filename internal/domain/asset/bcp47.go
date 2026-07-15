// Package asset — bcp47.go: canonical BCP-47 language-tag normalization
// + supported-languages whitelist (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b,
// July 2026).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The canonical BCP-47 normalization rules live here (Normalize + IsSupported).
//     Handlers MUST call these helpers and NEVER re-derive the rules inline
//     (audit 2026-07-11 §2.c: a Whisper-side normalization that uppercase'd
//     regions inconsistently caused "en-US" vs "EN-us" duplicates in
//     asset_text_tracks before the helper was extracted).
//   - The SupportedLanguages list (it, en, pl, ru, de, es, pt-BR, fr, tr, id)
//     is the canonical project-wide list of "languages we materialize
//     translations for" — used by the Fase 3 TextTrackMaterializer job AND by
//     the YouTube acquisition chain (acquire only these from Subtitles/Whisper).
//   - ErrLanguageUndeterminable is the SOLE typed error for "the chain
//     exhausted without surfacing a language and the policy requires
//     certainty". The Fase 4 video pipeline + Fase 5 backfill CLI
//     errors.As-probe this.
//
// godlike/07 no-fake-availability: every variant returned by Normalize is
// either a fully-formed BCP-47 (lang or lang-region) or the literal
// "und" marker — the resolver never silently substitutes "en" for an
// empty/unknown input.
package asset

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/localeutil"
)

// ErrLanguageUndeterminable is the typed error returned when the language-
// detection chain exhausts without surfacing a language AND the policy
// (`media.multilingual.require_language_certainty: true`) demands
// certainty. The text-track pipeline surfaces this BEFORE any DB write
// so a clip with an un-determined language can never silently land in
// the wrong language bucket.
//
// Callers (Fase 4 video pipeline, Fase 5 backfill CLI) errors.As-probe
// this to distinguish "backfill pending" from generic writer failures.
type ErrLanguageUndeterminable struct {
	AssetID string
	Reason  string
}

// Error renders the typed error message. Stable format — operator
// dashboards grep on the "language undeterminable" prefix.
func (e *ErrLanguageUndeterminable) Error() string {
	return "language undeterminable: asset=" + e.AssetID + " reason=" + e.Reason
}

// IsLanguageUndeterminable is the canonical probe used by callers.
func IsLanguageUndeterminable(err error) bool {
	var target *ErrLanguageUndeterminable
	return errors.As(err, &target)
}

// SupportedLanguages is the canonical project-wide list of languages the
// pipeline materializes translations for. Order is significant: it is the
// preferredLanguages priority order when the resolver probes YouTube
// subtitles (top-to-bottom in `media.subtitles --sub-langs` CSV).
//
// godlike/06 SSOT: this list is the SINGLE canonical list. Tests + CLI + admin
// backfill + TextTrackMaterializer job + YouTube acquisition chain all
// reference this constant. Adding/removing a language is a deliberate
// semantic change requiring a documentation update.
var SupportedLanguages = []string{
	"it",
	"en",
	"pl",
	"ru",
	"de",
	"es",
	"pt-BR",
	"fr",
	"tr",
	"id",
}

// IsSupported reports whether the input language is in the project
// whitelist. Used by the Fase 3 materializer to skip non-supported
// languages (the resolver still surfaces them — only the materializer
// gates translation jobs on IsSupported).
func IsSupported(code string) bool {
	if code == "" {
		return false
	}
	for _, s := range SupportedLanguages {
		if s == code {
			return true
		}
	}
	return false
}

// Normalize parses an arbitrary locale string into its canonical
// BCP-47 form. The output is ALWAYS one of:
//
//   - A fully-formed BCP-47 tag: `<lang>` or `<lang>-<region>` (e.g. "en",
//     "en-US", "pt-BR"). Lower-case language + upper-case region.
//   - The literal `"und"` (BCP-47 "undetermined") when the input is empty
//     OR whitespace-only. This is the ONLY collapse for unknown/empty
//     input — the resolver NEVER defaults to "en".
//
// The function REJECTS (returns an error) malformed inputs that cannot be
// reduced to a valid BCP-47 (lang, lang) pair:
//
//   - 3-letter ISO 639-2 codes ("eng", "por", "ita")
//   - 3-letter regions ("en-USA", "es-ESP")
//   - 3-part scripts ("zh-Hans", "en-US-CA")
//   - Digit-only inputs ("123")
//   - Mixed case only ("EnUs") — accepted (lower-cased + upper-cased region).
//   - Locale-CLDR suffixes ("en_US.UTF-8") — rejected.
//   - The full-language name ("portuguese", "english") — rejected.
//
// The `localeutil.Parse` regex `^([a-zA-Z]{2})(?:[_-]([a-zA-Z]{2}))?$` is
// the load-bearing gate; the trim+lowercase+uppercase normalization is
// the canonical output shape. The function is hot-path safe: O(len(input))
// with at most one allocation per call.
//
// godlike/06 SSOT: this is the canonical BCP-47 entry point. The
// `pkg/localeutil.Parse` regex is the regex of record; the canonical
// output shape is BCP-47 (hyphen, lowercase lang, uppercase region).
func Normalize(code string) (string, error) {
	// Empty / whitespace-only collapses to "und" — NOT an error. The
	// resolver treats "und" as a known undetermined signal that the
	// chain failed to surface a real language; downstream code (Fase 3
	// materializer) gates translations on IsSupported("und")==false.
	// godlike/07 no-fake-availability: the empty check is a TRIMMED
	// comparison so "   ", "\t", "\n", and "\t\n  " all collapse to
	// "und" (the localeutil regex below would otherwise reject
	// whitespace-only input with "invalid locale format: %q" which
	// would propagate as a typed error to the resolver, contradicting
	// the SSOT contract that empty/unknown is the canonical
	// undetermined signal — not a malformed-locale error).
	if strings.TrimSpace(code) == "" {
		return "und", nil
	}
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): strict
	// BCP-47 enforcement. Underscore is NOT a valid BCP-47
	// separator (BCP-47 strictly uses HYPHEN-MINUS U+002D).
	// POSIX locales use underscore, but that's a non-BCP-47
	// convention. The user spec mandates rejection of mixed
	// variants like "pt_br", "en_US", "POR" (3-letter), and
	// "portuguese" (full name). The first two of those are
	// underscore-separated; we reject them here. The other two
	// are rejected by the localeutil regex below.
	if strings.Contains(code, "_") {
		return "", fmt.Errorf("bcp47.Normalize: underscore separator not allowed in BCP-47 (use hyphen instead): %q", code)
	}
	// localeutil.Parse trims whitespace internally and rejects any
	// 3-letter/multi-part input. The empty case is already handled
	// above; this is the strict path.
	parsed, err := localeutil.Parse(code)
	if err != nil {
		// Reject with a stable error message so callers (resolver
		// priority 5 / Fase 3 materializer) can map to typed
		// errors via errors.As. The wrapped error is the
		// canonical "invalid locale format" sentinel from
		// localeutil.
		return "", fmt.Errorf("bcp47.Normalize: %w", err)
	}
	return parsed.BCP47, nil
}

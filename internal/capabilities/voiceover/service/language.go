// Package voiceover — language.go (PR-VO-TYPED-PRIMITIVES, July 2026).
//
// Typed envelope for the BCP-47 language code that every
// voiceover item carries (one language per item, per the P0.3
// items-model recovery of June 2026).
//
// PR-VO-TYPED-PRIMITIVES (July 2026) closes the audit-flagged
// primitive-obsession on Language across 14+ raw-string sites in
// command.go, task.go, types.go, result.go, parent_state.go,
// filename.go, ports.go, persistence/repository.go, jobs/fanout.go,
// process.go, stages.go, usecase.go, and the API boundary
// (api/assets/voiceover/types.go).
//
// JSON wire compat: type Language string serialises the
// underlying BCP-47 string byte-for-byte. The "language" /
// "languages" JSON tags on every field work without changes.
//
// DB wire compat: SQLite TEXT column binding of a Language
// value is byte-equivalent to binding the underlying string —
// the voiceovers.language column does not need a migration.

package voiceover

import (
	"fmt"
	"strings"
)

// Language is the typed envelope for a BCP-47-style language code
// (alphanumeric + hyphens; the canonical gate is LanguageCodeValid
// at validation.go). Defined type (NOT alias) so the type system
// catches the audit-flagged primitive-obsession at every
// assignment site. JSON wire shape + SQLite column shape are
// byte-equivalent with the pre-PR-VO-TYPED-PRIMITIVES string
// field.
type Language string

// EmptyLanguage is the canonical zero value. Use this (NOT the
// string-literal "") for typed comparison so the audit-pin
// discipline catches a future drift on the empty-marker.
const EmptyLanguage Language = ""

// ErrInvalidLanguage is the typed sentinel ParseLanguage returns
// when the input fails the canonical BCP-47 alphanumeric+hyphen
// gate. Callers can route via errors.Is for the typed error
// contract (godlike/07).
var ErrInvalidLanguage = fmt.Errorf("voiceover.Language: invalid code (only alphanumeric + hyphens allowed)")

// ParseLanguage is the canonical strict constructor for the
// Language typed envelope. Trims surrounding whitespace, then
// applies the canonical LanguageCodeValid gate (validation.go).
// Returns ErrInvalidLanguage on empty / invalid input.
//
// Use this at every TRUST BOUNDARY (HTTP handler, job payload
// unmarshal, internal fanout where the input is raw-string
// from a wire). Internal voiceover-internal sites that already
// hold a typed Language value do NOT need to re-validate.
func ParseLanguage(s string) (Language, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return EmptyLanguage, fmt.Errorf("ParseLanguage: empty code: %w", ErrInvalidLanguage)
	}
	if !isLanguageCodeValid(trimmed) {
		return EmptyLanguage, fmt.Errorf("ParseLanguage: %q: %w", trimmed, ErrInvalidLanguage)
	}
	return Language(trimmed), nil
}

// MustParseLanguage is the panic-on-invalid convenience constructor
// for sites where the input is already validated upstream (test
// fixtures, compile-time-known constants). Production call sites
// MUST use ParseLanguage + handle the error.
func MustParseLanguage(s string) Language {
	lang, err := ParseLanguage(s)
	if err != nil {
		panic(fmt.Sprintf("MustParseLanguage: %v", err))
	}
	return lang
}

// String returns the underlying string form. Useful for fmt
// interpolation + the many "lang=%s" log lines scattered through
// the voiceover pipeline.
func (l Language) String() string { return string(l) }

// IsEmpty returns true when l is the canonical zero value.
func (l Language) IsEmpty() bool { return l == EmptyLanguage }

// isLanguageCodeValid is the canonical BCP-47-style alphanumeric +
// hyphen gate. Mirrors the pre-PR-VO-TYPED-PRIMITIVES
// LanguageCodeValid (validation.go) — kept as a package-private
// helper so the canonical gate logic lives in one place (the
// exported LanguageCodeValid remains for back-compat with the
// existing validation tests).
//
// The two functions are intentionally identical byte-for-byte; the
// exported name preserves the test surface and the lowercase alias
// makes the canonical gate discoverable from the typed-envelope
// file.
func isLanguageCodeValid(code string) bool {
	if code == "" {
		return false
	}
	for _, r := range code {
		if r == '-' {
			continue
		}
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

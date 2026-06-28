// Package localeutil — BCP-47 / compact locale string parser.
//
// PR-VO-B3 (June 2026) — replaces the case-sensitive, BCP-47-only
// routing logic that was previously hard-wired into voiceover.process
// and a handful of ad-hoc call sites. The new design is a leaf package
// (zero imports from internal/) so other features can opt in without
// pulling voiceover onto their dependency graph.
//
// Parsing rules (per spec):
//
//   - Regex `^([a-zA-Z]{2})(?:[_-]([a-zA-Z]{2}))?$` — case-insensitive
//     through the ToLower/ToUpper normalization below.
//   - Accepts both hyphen and underscore separators (en-US, en_US).
//   - Accepts a 2-letter language with no region (en → en, "en").
//   - Rejects 3-letter ISO 639-2 codes (eng), 3-letter regions
//     (en-USA), 3-part scripts (zh-Hans, en-US-CA), digits-only,
//     underscore-CLDLR suffixes (en_US.UTF-8), and empty input.
//
// Canonicalization (single source of truth — NEVER reformat outside
// this function):
//
//   - Language code is always lower-case (Unicode lowercase via
//     strings.ToLower).
//   - Region code is always upper-case (Unicode uppercase via
//     strings.ToUpper).
//   - Compact form: `<lang><region>` (no separator) — e.g. enUS,
//     en, ptBR. Used as the canonical key for cache lookups, dedupe
//     hashes, and any internal id composition.
//   - BCP-47 form: `<lang>-<region>` (hyphen separator) when a region
//     is present, `<lang>` alone when not. Used for human-readable
//     output (UI labels, log lines, manifest exports).
//
// The parser NEVER throws a panic and NEVER returns a zero-value
// ParsedLocale without an error. Call sites can rely on either
// `parsed.Compact == ""` xor `err == nil` to detect failure.
package localeutil

import (
	"fmt"
	"regexp"
	"strings"
)

// localeRe matches `^([a-zA-Z]{2})(?:[_-]([a-zA-Z]{2}))?$` exactly.
// The unicode-aware regular expression character class would NOT
// broaden the design — the spec is ASCII-only for the language/region
// pair. Future script subtags (BCP-47 extended) require a separate
// PR because they break the compact form's column-shared semantics.
var localeRe = regexp.MustCompile(`^([a-zA-Z]{2})(?:[_-]([a-zA-Z]{2}))?$`)

// ParsedLocale contains the canonical encodings of a locale.
//
// Compact and BCP47 are NEVER independently computed by callers — every
// consumer should call Parse() once and reuse the result. The struct
// shape exists so log lines / id composition can use Compact while
// UI labels use BCP47 from the SAME parse.
type ParsedLocale struct {
	// Compact is the column-stable `<lang><region>` form (lowercase
	// language + uppercase region, no separator). Empty when the
	// input had no region. e.g. "enUS", "en", "ptBR".
	Compact string
	// BCP47 is the human-readable `<lang>-<region>` form (hyphen
	// separator, lowercase language + uppercase region). When no
	// region was provided, BCP47 equals Compact (without the
	// hyphen). e.g. "en-US", "en", "pt-BR".
	BCP47 string
}

// String returns the BCP-47 form for logging and error messages. It
// deliberately does NOT return the Compact form because log/UI streams
// are human-targeted; the Compact form is reserved for id composition.
func (p ParsedLocale) String() string {
	return p.BCP47
}

// Parse normalizes an arbitrary locale string into its canonical
// encodings. Returns a non-nil error (with the offending input quoted)
// if the input does not match the regex.
//
// Whitespace is trimmed before matching — `"  en-US  "` and `"en-US"`
// produce the same output. Whitespace inside the locale (e.g.
// `"en - US"`) always fails.
//
// Performance: localeRe.FindStringSubmatch is O(len(input)) and the
// function does no allocation beyond the two strings.To* calls and a
// struct literal. Safe to call from request hot paths.
func Parse(locale string) (ParsedLocale, error) {
	matches := localeRe.FindStringSubmatch(strings.TrimSpace(locale))
	if matches == nil {
		return ParsedLocale{}, fmt.Errorf("invalid locale format: %q", locale)
	}

	lang := strings.ToLower(matches[1])
	if matches[2] == "" {
		return ParsedLocale{Compact: lang, BCP47: lang}, nil
	}

	region := strings.ToUpper(matches[2])
	return ParsedLocale{
		Compact: lang + region,
		BCP47:   lang + "-" + region,
	}, nil
}

// IsValid reports whether the input would Parse without error. Useful
// for handler-side preflight checks where a hard 400 response is
// preferred over a runtime error.
func IsValid(locale string) bool {
	return localeRe.MatchString(strings.TrimSpace(locale))
}

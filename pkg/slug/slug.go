// Package slug provides canonical title → slug transformation
// for Drive folder names, asset slugs, and any other filesystem-
// or URL-safe identifier derived from user-supplied text.
//
// godlike/06 SSOT (one canonical owner per fact): this package is
// the SOLE canonical owner of the title-slug convention. Both
// pkg/stockparser/parser.go::deriveSlug (the parser-side surface)
// and internal/capabilities/assets/providers/stock/stockpipeline/step_publish.go::slugifyTitle
// (the stock-pipeline surface) route through SlugifyTitle so
// identical inputs produce byte-equivalent slugs at both call
// sites. The pre-PR-SLUG-HELPER-EXTRACT implementations diverged
// subtly: the parser used pathutil.SafeFolderName (which REPLACES
// unsafe chars with underscores) and the stock pipeline STRIPPED
// unsafe chars entirely. The stock-pipeline convention won the
// godlike/06 SSOT resolution per the user diagnostic
// "round-7-broner-barcolla" (no underscore escape artifacts) and
// the parser's caller is migrated to match.
//
// godlike/07 NO-FAKE-AVAILABILITY: the function NEVER returns
// "untitled" (the pathutil.SafeFolderName all-whitespace fallback).
// Empty / whitespace / pure-unsafe-char inputs collapse to ""
// so callers can fall through to their own canonical fallback
// (time-range literal for the parser, start-end literal for the
// stock pipeline). The user diagnostic "Round 7 - Broner
// barcolla" → "round-7-broner-barcolla" is the canonical happy
// path; edge cases (empty, whitespace-only, all-unsafe-chars)
// are documented in the test surface.
//
// Leaf rule: this package MUST NOT import anything from
// internal/. It uses only stdlib (strings, unicode) so callers
// in both pkg/stockparser (leaf) and internal/application/.../
// stockpipeline (non-leaf) can depend on it without introducing
// import cycles.
package slug

import (
	"strings"
	"unicode"
)

// SlugifyTitle returns the canonical lowercase hyphen-separated
// slug for a user-supplied title. The convention matches the
// user diagnostic "Round 7 - Broner barcolla" →
// "round-7-broner-barcolla" (no escape artifacts).
//
// Pipeline (5 steps, applied in order):
//
//  1. Strip filesystem-unsafe chars ENTIRELY (NOT replace with
//     underscore). Drops the char entirely so the resulting slug
//     doesn't carry escape artifacts like "_" or "_official_".
//     Kept chars: unicode letter, unicode digit, ASCII hyphen,
//     ASCII underscore, ASCII space. All others (colon, paren,
//     period, slash, ampersand, etc.) are dropped.
//  2. ToLower — lowercase convention per Drive naming.
//  3. space-to-hyphen — single replacement.
//  4. collapse-consecutive-hyphens — " - " (round-dash-round) →
//     "---" after step 3, collapsed to a single "-". Loop until
//     no "--" substring remains; a single pass is sufficient for
//     all realistic inputs but the loop handles pathological edge
//     cases (e.g. a title that contains the literal "----").
//  5. trim-leading-trailing-hyphens — no edge artifacts.
//
// godlike/07 NO-FAKE-AVAILABILITY: returns "" for empty /
// whitespace-only / pure-unsafe-char inputs (NOT "untitled" or
// any other placeholder). Callers MUST handle the empty case
// themselves; the parser falls back to the time-range literal
// and the stock pipeline falls back to the start-end literal.
//
// godlike/07 typed-error contract: never returns an error. The
// transformation is total over all unicode strings.
//
// Byte-equivalence contract: the parser and the stock-pipeline
// MUST produce identical output for identical input. The TDD
// surface in slug_test.go asserts this contract on 10+ titles
// including canonical examples (Round 7 - Broner barcolla),
// edge cases (empty, whitespace, all-unsafe-chars), and
// multi-byte unicode (Spanish / Italian / accented chars).
func SlugifyTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		// godlike/07: empty / whitespace-only inputs return ""
		// (not "untitled") so callers can route to their own
		// canonical fallback. This is a deliberate divergence
		// from pathutil.SafeFolderName which returns "untitled".
		return ""
	}
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == ' ' {
			b.WriteRune(r)
		}
		// else: strip entirely (the pathutil.SafeFolderName
		// "replace with underscore" semantic would carry
		// escape artifacts into the slug).
	}
	slug := strings.ToLower(b.String())
	if slug == "" {
		// Pure-unsafe-char input (e.g. "!!!", ":::", "///") —
		// after step 1, nothing remains. Return "" so the
		// caller falls through to its own canonical fallback.
		return ""
	}
	slug = strings.ReplaceAll(slug, " ", "-")
	// Collapse any "---+" run to a single "-". Loop until no
	// "--" substring remains; a single pass is sufficient for
	// all realistic inputs (the longest realistic input is
	// "   " → "---" → 1 pass), but the loop handles pathological
	// cases (e.g. a title that contains the literal "----").
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return strings.Trim(slug, "-")
}

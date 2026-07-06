// Package metadata — metadata extraction helpers.
//
// metadata_extraction.go owns the metadata-building helpers:
// BuildFallbackSearchText, parseClipTimestamps, atoiOrZero.
// Extracted from service.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package metadata

import (
	"regexp"
	"strings"
)

// ── Search text builder ─────────────────────────────────────────────────────

// BuildFallbackSearchText concatenates title + summary +
// topics + transcript_excerpt into a 1KB-bounded
// semantic-search surface. The 1024-byte cap is the
// canonical limit (matches the legacy search_text column
// width); the function trims at a word boundary so the
// final token is never cut mid-word.
//
// STATUS (June 2026, Commit 4): this helper is intentionally
// exported per the verdict's "5 canonical helpers" rule, but
// it currently has no production caller (its consumers are the
// in-package tests). The intended future call site is the
// writer's search-text backfill (post-write save path in
// usecase/metadata_service.go::EnrichClip's language-stamping
// fallback branch), planned for Commit 5. Keeping the export
// is preferred over unexporting because:
//
//  1. The verdict's spec lists this helper as one of the 5
//     canonical surfaces the sub-package MUST materialise.
//  2. Lowercasing to buildFallbackSearchText would force a
//     rename when the future commit wires it, churning the
//     test surface for no functional win.
//  3. A 5-line TDD lock-in guards the truncation behaviour
//     — the helper is used, just not in production YET.
//
// If the future commit never materialises, deprecate the
// helper formally with a deprecation ID in
// architecture/deprecations.yaml and remove the export.
func BuildFallbackSearchText(title, summary string, topics []string, transcript string) string {
	const maxBytes = 1024
	var sb strings.Builder
	if title != "" {
		sb.WriteString("Title: ")
		sb.WriteString(title)
		sb.WriteString("\n")
	}
	if summary != "" {
		sb.WriteString("Summary: ")
		sb.WriteString(summary)
		sb.WriteString("\n")
	}
	if len(topics) > 0 {
		sb.WriteString("Topics: ")
		sb.WriteString(strings.Join(topics, ", "))
		sb.WriteString("\n")
	}
	if transcript != "" {
		excerpt := transcript
		if len(excerpt) > 400 {
			excerpt = excerpt[:400]
		}
		sb.WriteString("Transcript: ")
		sb.WriteString(excerpt)
	}
	out := sb.String()
	if len(out) <= maxBytes {
		return out
	}
	// Trim at the last space before the cap so the final
	// token isn't cut mid-word. If no space exists (e.g.
	// the entire string is a single very long word), hard-trim.
	trimmed := out[:maxBytes]
	if idx := strings.LastIndex(trimmed, " "); idx > maxBytes-128 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}

// ── Timestamp parsing ───────────────────────────────────────────────────────

// parseClipTimestamps extracts (startSec, endSec) from a
// canonical clipID using a regex anchored on the "yt_"
// prefix. The legacy underscore-split heuristic failed on
// clipIDs whose name contained underscores (e.g. when the
// segment name is "How_we_work" → split-on-'_' returns 6
// parts and reads the wrong fields). The regex pin to
// "yt_" + videoID + "_<startSec>_" + "<endSec>_" is robust.
//
// Returns (0, 0) on parse failure (zero-value safe — the
// caller is the write path, which treats 0 as "no
// coords" and skips the duration stamp).
func parseClipTimestamps(clipID string) (startSec, endSec int) {
	if clipID == "" {
		return 0, 0
	}
	// Match the canonical yt_<videoID>_<start>_<end>[_<policy>]
	// shape. videoID may contain underscores (11-char YouTube
	// IDs don't, but be defensive), so we anchor on the
	// trailing "_<digits>_<digits>" pair.
	re := regexp.MustCompile(`yt_[^_]+(?:_[^_]+)*_(\d+)_(\d+)(?:_|$)`)
	m := re.FindStringSubmatch(clipID)
	if len(m) != 3 {
		return 0, 0
	}
	startSec = atoiOrZero(m[1])
	endSec = atoiOrZero(m[2])
	if startSec < 0 {
		startSec = 0
	}
	if endSec < 0 {
		endSec = 0
	}
	return startSec, endSec
}

// atoiOrZero is a parse-int helper that returns 0 on
// failure. The regex guarantees the captured group is a
// digit sequence; this helper exists so a malformed
// clipID doesn't trigger a panic.
func atoiOrZero(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

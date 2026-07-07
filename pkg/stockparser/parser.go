// Package stockparser parses human-readable timestamp blocks into
// typed ClipSpec-aligned records the stock pipeline can consume.
//
// The parser is the SOLE owner of the wire-format conversion
// between the user's pasted timestamp text and the canonical
// `[]stockpipeline.ClipSpec` shape required by the stock
// orchestrator's explicit planner. Per godlike/06 SSOT, callers
// MUST NOT re-implement the regex/parsing logic in their own
// layer — they invoke ParseTimestampClipSpecs and convert the
// returned []ParsedClipSpec to Clips via a 1-line mapper
// (kept in the caller's package so pkg/stockparser stays leaf).
//
// godlike/07 contract:
//   - Empty / unparseable text returns nil (NOT an empty slice and
//     NOT an error). The parser is best-effort by design — a
//     caller handing us a malformed block must still be able to
//     surface its own validation, not be hijacked into the parser's
//     failure contract. errors.Is is unnecessary: there are NO
//     typed errors — the function is total.
//   - Malformed-but-matching timestamp ranges are preserved with
//     StartSec=0 + EndSec=0 (the downstream stock planner's
//     validation rejects zero-durations, surfacing the failure to
//     the operator). godlike/07 NO-FAKE-AVAILABILITY: the parser
//     MUST NOT pretend a line wasn't there.
//
// Leaf rule: this package MUST NOT import anything from
// internal/. It uses only stdlib + the canonical pkg/textutil
// `ParseTimestamp` atomic helper + pkg/pathutil for slug
// derivation. The Title field on ParsedClipSpec is the raw
// captured text (no title-casing) — callers format as they
// please.
package stockparser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ParsedClipSpec is the leaf output of the timestamp-block parser.
// It carries the per-clip fields that downstream code wires into
// the canonical stock ClipSpec (Title, Description, Round, Tags,
// Category, Slug, StartSec, EndSec, SourceURL). New fields can be
// added here without breaking the import cycle — pkg/stockparser
// stays free of internal/ imports.
//
// godlike/06 SSOT: this is the parser output SHAPE; the stock
// ClipSpec conversion lives at the call site (the api/handler seam
// or the stockpipeline orchestrator caller) so the two package
// surfaces stay separate.
//
// Description / Tags / Category are CALLER-POPULATED fields: the
// parser is strictly a timestamp-block shape recognizer. The
// stock pipeline caller attaches the per-run metadata (e.g.
// `in.Metadata.Category` or `in.Metadata.Tags`) at the api/handler
// seam so the parser stays free of run-context dependencies.
type ParsedClipSpec struct {
	Title       string   // Raw title captured from "Round N - Title" prefix; caller may title-case
	Description string   // Caller-populated (e.g. from run-level metadata)
	Round       int      // 1-based round number when the user prefixed the line with "Round N"; 0 otherwise
	Tags        []string // Caller-populated
	Category    string   // Caller-populated
	Slug        string   // Filesystem-safe slug derived from Title via pathutil.SafeFolderName; falls back to the time range literal when Title is empty/whitespace
	StartSec    float64  // Clip start in seconds (atomic HH:MM:SS / MM:SS parse via pkg/textutil)
	EndSec      float64  // Clip end in seconds
	SourceURL   string   // The URL passed through verbatim by the caller (parser is agnostic to source identity)
}

// parseTimestampRangeRe matches a single timestamp-range token on
// a line. Square brackets are required (the canonical user-pasted
// format). Hyphen-minus, en-dash, and em-dash are all accepted as
// the inner separator so rich-text pastes don't break the parser.
//
// Capture groups:
//
//	1: start timestamp string (e.g. "00:00:32")
//	2: end timestamp string (e.g. "00:03:51")
var parseTimestampRangeRe = regexp.MustCompile(`\[(\d{1,2}:\d{2}(?::\d{2})?)\]\s*[-–—]\s*\[(\d{1,2}:\d{2}(?::\d{2})?)\]`)

// roundPrefixRe matches an optional "Round N" prefix on the same
// line OR on a preceding line (case-insensitive). Capture groups:
//
//	1: round number ("7", "12")
//	2: optional title remainder after the round prefix
//
// Supported prefixes: round, turno (Italian), asalto (Spanish),
// manche (French). German "Runde" is intentionally NOT supported
// (boxing is rare in Germany; the user's domain is the Italian
// /Spanish /French boxing canon). Caller can extend by wrapping
// the package in a domain-specific alias.
var roundPrefixRe = regexp.MustCompile(`(?i)^\s*(?:round|turno|asalto|manche|round\s+no\.?)\s*(\d+)\s*[-:.]?\s*(.*?)\s*$`)

// ParseTimestampClipSpecs parses a multi-line timestamp block
// into a slice of ParsedClipSpec. Returns nil for empty /
// unparseable text. The canonical example is
//
//	[00:00:32] - [00:03:51] Round 1 - La fase di studio
//	[00:04:07] - [00:05:45] Round 2 - Il posizionamento
//	[00:16:33] - [00:17:28] Round 7 - Broner barcolla
//
// but the parser also accepts variants:
//   - Round on a separate line immediately preceding the timestamp
//     block (multi-line form).
//   - Bare timestamps without a Round prefix (Round = 0).
//   - En-dash / em-dash separators (`–`, `—`).
//   - MM:SS (no hours) for short clips (e.g. `[00:32] - [01:27]`).
//   - CRLF line endings (\r\n) — TrimSpace handles the \r.
//
// Constraints (godlike/07 NO-FAKE-AVAILABILITY):
//   - One range per line: lines with multiple `[..] - [..]` tokens
//     yield only the FIRST range. The remainder is ignored. This
//     is a documented limitation — a future PR can extend via
//     FindAllStringIndex if multi-range lines become common.
//   - Title-casing is NOT applied: the captured Title is the raw
//     user-pasted text minus the "Round N -" prefix. Callers
//     wanting "Title Case" formatting do it themselves (keeps
//     pkg/stockparser free of presentation concerns).
//   - Tags / Description / Category are caller-populated; the
//     parser only sets Title, Round, Slug, StartSec, EndSec,
//     SourceURL.
func ParseTimestampClipSpecs(text, url string) []ParsedClipSpec {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	out := make([]ParsedClipSpec, 0, len(lines))

	// pendingRound + pendingTitle carry context from the most
	// recently-seen "Round N - Title" line so a subsequent line
	// containing only the timestamp range inherits that context.
	// This handles the multi-line form demonstrated in the user's
	// pasted example ("Round 7 - Broner barcolla\n[00:16:33] ...")
	// without a regex look-behind (which Go regex doesn't support).
	var pendingRound int
	var pendingTitle string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Single-line form: "Round N - Title" on a line WITHOUT
		// a timestamp range. Carry context forward.
		if !parseTimestampRangeRe.MatchString(trimmed) {
			if m := roundPrefixRe.FindStringSubmatch(trimmed); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
					pendingRound = n
					pendingTitle = strings.TrimSpace(m[2])
				}
			}
			continue
		}
		m := parseTimestampRangeRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		startSec := parseTimestampAtom(m[1])
		endSec := parseTimestampAtom(m[2])
		if endSec <= startSec {
			// Malformed range: preserve the line but mark
			// StartSec=0 + EndSec=0 so the stock planner's
			// validation catches it downstream; we don't
			// silently drop the line (godlike/07
			// no-fake-availability: the caller must SEE that
			// we saw a malformed timestamp).
			startSec, endSec = 0, 0
		}

		// Resolve round + title with line-local override:
		// "Round N - Title [HH:MM:SS] - [HH:MM:SS]" on the
		// SAME line wins over the carry-forward pending values.
		lineRound := pendingRound
		lineTitle := pendingTitle
		if lineLocal := extractRoundAndTitleBeforeRange(trimmed); lineLocal != nil {
			lineRound = lineLocal.Round
			lineTitle = lineLocal.Title
		}
		// Consume pending values (one-shot carry-forward).
		pendingRound = 0
		pendingTitle = ""

		slug := deriveSlug(lineTitle, m[1], m[2])

		out = append(out, ParsedClipSpec{
			Title:     lineTitle,
			Round:     lineRound,
			Slug:      slug,
			StartSec:  startSec,
			EndSec:    endSec,
			SourceURL: url,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseTimestampAtom wraps pkg/textutil.ParseTimestamp for the
// (HH:MM:SS) and (MM:SS) atomic forms. On parse error (e.g.
// "1::2") the result is 0 — the caller decides whether to surface
// a typed error; we don't elevate a single bad atom into a
// per-line drop because the parser's contract is best-effort.
func parseTimestampAtom(ts string) float64 {
	secs, err := textutil.ParseTimestamp(ts)
	if err != nil {
		return 0
	}
	return float64(secs)
}

// deriveSlug returns the canonical Drive-leaf-safe slug for a
// clip. Priority order:
//  1. SafeFolderName(Title) → lowercase → spaces-to-hyphens
//     when Title is non-empty (after TrimSpace — guards against
//     the "untitled" leak when the user pastes "  " as a title).
//     The lowercase+hyphen convention mirrors the user's
//     diagnostic example ("round-01-la-fase-di-studio") and the
//     Drive-folder-naming convention (lowercase + hyphens > mixed
//     case + spaces).
//  2. time-range literal "HH-MM-SS_to_HH-MM-SS" (colons replaced
//     with hyphens so the leaf is operator-readable on Drive).
func deriveSlug(title, startAtom, endAtom string) string {
	if strings.TrimSpace(title) != "" {
		// SafeFolderName strips filesystem-unsafe chars (/: etc).
		// ToLower + space-to-hyphen normalizes the convention.
		slug := strings.ToLower(pathutil.SafeFolderName(title))
		return strings.ReplaceAll(slug, " ", "-")
	}
	return strings.ReplaceAll(
		fmt.Sprintf("%s_to_%s", startAtom, endAtom),
		":", "-",
	)
}

// roundTitleCarry is the line-local override parsed from the same
// line that contains a timestamp range (i.e. when the user pastes
// "Round 7 - Broner barcolla [00:16:33] - [00:17:28]" all on one
// line).
type roundTitleCarry struct {
	Round int
	Title string
}

// extractRoundAndTitleBeforeRange scans the text BEFORE the
// timestamp-range token (within the same line) for a "Round N -
// Title" prefix. Returns nil when no such prefix exists.
func extractRoundAndTitleBeforeRange(line string) *roundTitleCarry {
	loc := parseTimestampRangeRe.FindStringIndex(line)
	if loc == nil {
		return nil
	}
	prefix := strings.TrimSpace(line[:loc[0]])
	if prefix == "" {
		return nil
	}
	m := roundPrefixRe.FindStringSubmatch(prefix)
	if m == nil {
		return nil
	}
	round, err := strconv.Atoi(m[1])
	if err != nil || round <= 0 {
		return nil
	}
	return &roundTitleCarry{
		Round: round,
		Title: strings.TrimSpace(m[2]),
	}
}

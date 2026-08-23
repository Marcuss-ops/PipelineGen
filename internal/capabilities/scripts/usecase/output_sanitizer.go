// Package scripts — output_sanitizer.go (PR-CS-1, FASE 4, DoD #7).
//
// SanitizeScriptOutput is the canonical post-processing stripper
// for raw Gemma output. It is INVOKED ONCE per generation right
// after the jsonextract.Scanner produces a ModelScriptOutputV1
// (engine_generate.go: cache-hit + fresh paths) and BEFORE the
// result flows to persistence (PersistenceProcessor) or to the
// memory-cache write.
//
// The function is intentionally a pure string → string transform:
//
//   - no surrounding state, no logger, no metrics.
//   - idempotent (running it twice on already-clean text is a
//     no-op; this matters because cache-hits may replay sanitized
//     output and the cache-write path also sees sanitized output).
//   - deterministic regex list — NO LLM-as-judge.
//
// Allowed prose flows through untouched. The stripper only removes
// pattern-recognition tokens the LLM bled through from the prompt
// (segment markers, labelled directive lines, JSON keys) and
// Markdown residue (fences, comment headers).
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// post-processing sanitizer. Anything that needs to scrub non-prose
// artefacts from a script-generation result MUST route through
// SanitizeScriptOutput. Persisted scripts MUST not contain
// SEGMENT N / Topic: / Source text: / clip_id lines — that is the
// DoD-driven contract this function enforces.
package usecase

import (
	"regexp"
	"strings"
)

// SanitizeScriptOutput rimuove artefatti non desiderati dall'output
// LLM di Gemma, applicato DOPO la decodifica del modello, PRIMA
// del QA. DoD #7 — output deve contenere solo testo continuo.
// Removes:
//
//   - Linee `SEGMENT n` (con o senza numero).
//   - Linee `Topic:` / `Source text:` / `Target words:` residue
//     del prompt.
//   - Qualsiasi occorrenza di clip_id, accepted_clip_ids,
//     segnaposto schema_version, specscene, JSON literal.
//   - Markdown code fences (```).
//   - Comment header lines starting with `# `.
//
// Mantiene intatto il testo continuo. Idempotente.
//
// Behaviour (deterministic — runs in O(n) on input length, no LLM):
//
//  1. Trim leading/trailing whitespace globally.
//  2. Split per newline, drop a line matching
//     `(?i)^[\s#]*seg(e|i)ment\s*\d*\s*$`.
//  3. Drop a line matching
//     `(?i)^[\s#]*(topic|source\s*text|target\s*words)\s*:`.
//  4. Drop a line matching `(?i)^[\s#]*clip_id\s*:` OR that
//     contains `accepted_clip_ids` / `specscene` /
//     `schema_version` as a substring.
//  5. Drop a line that is solely a Markdown fence (``` exactly
//     or 3+ backticks with optional whitespace) OR a comment
//     header (`# ` prefix).
//  6. Collapse 3+ consecutive newlines (2+ blank lines) into at
//     most 1 blank line (single paragraph separator).
//  7. Trim each surviving line.
//  8. Re-join with `\n` and trim global whitespace once more
//     (catches leading/trailing empty lines produced by the
//     line scan).
//
// NON modify semantic text. Entity / date / name / score / quote
// tokens are NEVER touched.
func SanitizeScriptOutput(raw string) string {
	// Step 1: global trim. Empty input stays empty.
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Steps 2-5 + 7: split, drop artefacts, trim each survivor.
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			// Preserve an empty line at this stage — step 6
			// collapses runs.
			kept = append(kept, "")
			continue
		}
		if shouldDropLine(ln) {
			continue
		}
		kept = append(kept, ln)
	}

	// Step 6: collapse 2+ consecutive blanks into 1.
	collapsed := collapseBlankLines(kept)

	// Step 8: re-join + final trim.
	out := strings.Join(collapsed, "\n")
	out = strings.TrimSpace(out)
	return out
}

// ── Patterns (precompiled once for hot-path performance) ────────────

var (
	// SEGMENT n marker line, with optional leading `# ` / spaces.
	// Allows "SEGMENT", "Segment", and the typo "segiment" /
	// "segement" (typo resilience — the (?:e|i)? makes the
	// middle letter OPTIONAL so the canonical "segment" spelling
	// matches alongside the legacy typo variants).
	reSegmentMarker = regexp.MustCompile(`(?i)^[\s#]*seg(?:e|i)?ment\s*\d*\s*$`)

	// Labelled directive lines emitted by the engine prompt
	// (Topic:, Source text:, Target words:). The whitespace
	// allowance inside `source\s*text` covers variant
	// capitalisations and ACII whitespace oddities.
	reLabelledDirective = regexp.MustCompile(`(?i)^[\s#]*(topic|source\s*text|target\s*words)\s*:`)

	// clip_id: line as JSON key. Whole-line drop (label only).
	reClipIDKey = regexp.MustCompile(`(?i)^[\s#]*clip_id\s*:`)

	// Tokens that must NEVER appear in persisted prose. Any line
	// containing one of these substrings is dropped entirely
	// (they are token/identifier bleed-through, not prose content).
	reBannedToken = regexp.MustCompile(`(?i)(?:accepted_clip_ids|specscene|schema_version)`)

	// Markdown fence: a line consisting of ≥ 3 backticks with
	// optional surrounding whitespace.
	reMarkdownFence = regexp.MustCompile(`(?i)^\s*` + "`" + `{3,}\s*$`)

	// Comment header: line starting with `# ` (hash + space)
	// OR a bare `#` line. Conservative — only drops lines whose
	// leading token is a hash, so headings in prose that legitimately
	// contain `#` are preserved (the spec is "commento tecnico
	// residuo", i.e. only shapes that look like code-comments).
	reCommentHeader = regexp.MustCompile(`^\s*#\s*$|^\s*#\s+\S`)
)

// shouldDropLine returns true when the (already-trimmed) line
// matches one of the pattern slots in steps 2-5.
func shouldDropLine(line string) bool {
	if reSegmentMarker.MatchString(line) {
		return true
	}
	if reLabelledDirective.MatchString(line) {
		return true
	}
	if reClipIDKey.MatchString(line) {
		return true
	}
	if reMarkdownFence.MatchString(line) {
		return true
	}
	if reCommentHeader.MatchString(line) {
		return true
	}
	if reBannedToken.MatchString(line) {
		return true
	}
	return false
}

// collapseBlankLines walks lines and emits at most one blank
// line before each non-blank content line. This collapses 2+
// consecutive blanks (3+ newlines in the joined output) into a
// single separator.
//
// Trailing blanks are silently dropped (the caller's final
// strings.TrimSpace handles any residual whitespace).
func collapseBlankLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	blankStreak := 0
	for _, ln := range lines {
		if ln == "" {
			blankStreak++
			continue
		}
		if blankStreak > 0 {
			// Emit exactly one blank line before resuming prose.
			out = append(out, "")
		}
		out = append(out, ln)
		blankStreak = 0
	}
	return out
}

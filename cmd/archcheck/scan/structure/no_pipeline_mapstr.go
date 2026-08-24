// Package scan — Step 6 per-check (July 2026,
// typed-carrier purity): the GenerateOneUseCase 4-phase pipeline
// must not use `map[string]any` for state that flows between the
// orchestrator (generate_one_usecase.go) and any of the four
// phase carriers:
//
//	PhasePreparer      -> generation_prepare.go
//	PhaseEngine        -> generation_engine.go
//	PhasePostprocessor -> generation_postprocess.go
//	PhaseFinalizer     -> generation_finalize.go
//
// Pipeline state must flow through the strongly-typed carriers
// (PreparedGeneration / GeneratedDraft / ProcessedGeneration /
// FinalizeInputs / GenerationResult). A `map[string]any` typed
// field inside a pipeline-carrier file is a type-erasure smell:
// it is impossible to reason about the data flow as compile-time
// invariants, and it is the same shape that produced the
// diagnostic's "PipelineState map[string]any" anti-pattern.
//
// EXEMPTIONS:
//   - `tracker.TrackEvent(\"…\", \"…\", map[string]any{...})` inline
//     metadata — that is the observability surface, NOT a
//     pipeline carrier. We exempt any line containing
//     `.TrackEvent(` (the canonical call-site shape, including
//     `tracker.TrackEvent(`).
//
// godlike/06 SSOT NOTE — semantic-duplicate flag: this file is a
// deliberately-named MIRROR of percheck_pipeline_map_carrier_ban.go,
// sharing the same target file list + same regex + same
// exemptions. The duplication is intentional (per the operator's
// explicit Step 6 instruction to add this rule at the canonical
// percheck path). Both rules emit SeverityError on the same line,
// same snippet, same MatchedRule, so a violation surfaces TWICE in
// the report unless one is demoted to a WARN. Operators looking to
// consolidate should retire this file in favor of
// percheck_pipeline_map_carrier_ban.go (the older gate).
//
// scanner policy:
//   - target list: exactly 4 pipeline-carrier files (mirrors
//     percheck_pipeline_map_carrier_ban.go).
//   - skip the scanner's own package prefix (cmd/archcheck/scan)
//     so the regex self-match inside the scanner's docs/comments/
//     strings doesn't trip the gate.
//   - skip `_test.go` files.
//   - exempt `tracker.TrackEvent(...)` call sites.
//   - comment-only matches → WARN residue bucket (godlike/07
//     NO_FAKE_AVAILABILITY migration window).
//
// matched rule_id: `percheck_no_pipeline_mapstr`.
package structure

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// noPipelineMapStrSkipPathPrefixes is the scanner's own
// package exemption.
var noPipelineMapStrSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// noPipelineMapStrTargets is the EXACT set of files the gate
// scans. The four pipeline carriers (Preparer, EngineRunner,
// Postprocessor, Finalizer) live together in these files.
// Mirrors percheck_pipeline_map_carrier_ban.go's target list
// (intentional godlike/06 SSOT semantic-duplicate flag).
var noPipelineMapStrTargets = []string{
	"internal/application/scripts/usecase/generation_prepare.go",
	"internal/application/scripts/usecase/generation_engine.go",
	"internal/application/scripts/usecase/generation_postprocess.go",
	"internal/application/scripts/usecase/generation_finalize.go",
}

// noPipelineMapStrRule is the rule-family id.
const noPipelineMapStrRule = "percheck_no_pipeline_mapstr"

// noPipelineMapStrNote is the violation note.
const noPipelineMapStrNote = "forbidden `map[string]any` literal inside a GenerateOneUseCase pipeline-carrier file (typed-carrier purity gate, July 2026; Step 6 per-check). Pipeline state must flow through typed carriers (PreparedGeneration, GeneratedDraft, ProcessedGeneration, FinalizeInputs, GenerationResult); `map[string]any` is the type-erasure idiom previously guarded against. The ONLY allowed `map[string]any` usage in these files is the inline metadata argument of `tracker.TrackEvent(...)` (observability surface, NOT a pipeline carrier). Replace with a typed struct field (e.g. `type PostprocessTimings struct { EntitiesMs int64; MetadataMs int64; ... }`) or extend the existing carrier type. NOTE: this rule is a semantic duplicate of percheck_pipeline_map_carrier_ban.go (same target list + same regex); if both fire on the same line, prioritize the older gate's Note in the operator-facing report."

// noPipelineMapStrRe matches the literal `map[string]any`
// token. The regex is a literal substring match — no
// disambiguation between type-assertion vs literal-init
// syntax — because the issue is the symbol's presence on a
// non-exempt line.
var noPipelineMapStrRe = regexp.MustCompile(`map\[string\]any`)

// noPipelineMapStrTrackEventRe detects the canonical
// TrackEvent call-site shape. A line containing
// `.TrackEvent(` is observability metadata and exempt.
var noPipelineMapStrTrackEventRe = regexp.MustCompile(`\.TrackEvent\s*\(`)

// noPipelineMapStrWarn emits the WARN residue entry.
func noPipelineMapStrWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, noPipelineMapStrRule+" "+label+" "+msg)
}

// ScanNoPipelineMapStr walks the gate's pre-canned target
// file set and emits a violation for any non-exempt
// `map[string]any` reference inside them.
//
// productionOnly=true silences the comment-only WARN bucket.
func ScanNoPipelineMapStr(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol

	for _, target := range noPipelineMapStrTargets {
		full := filepath.Join(root, target)
		relPath := filepath.ToSlash(target)

		// The scanner's own path is exempt so the regex
		// self-mentions inside the package's docs/comments
		// don't trip the gate.
		if hasAnyPathPrefix(relPath, noPipelineMapStrSkipPathPrefixes) {
			continue
		}
		// _test.go targets skip.
		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}
		scanNoPipelineMapStrFile(full, relPath, r, productionOnly)
	}
}

// scanNoPipelineMapStrFile opens a single .go file and
// emits percheck_no_pipeline_mapstr violations for any line
// containing `map[string]any` unless the line is a
// TrackEvent call. Comment-only matches → WARN residue
// bucket. productionOnly=true silences the WARN bucket.
func scanNoPipelineMapStrFile(path, relPath string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		// Target file absence is non-fatal (a partial
		// repository checkout can omit the file; the gate
		// produces zero violations in that case).
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")

		// TrackEvent call-site: exempt.
		if noPipelineMapStrTrackEventRe.MatchString(line) {
			continue
		}

		// Comment-only matches → WARN residue bucket.
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			noPipelineMapStrRe.MatchString(line) {
			commentOnly++
			continue
		}

		if !noPipelineMapStrRe.MatchString(line) {
			continue
		}

		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromNoPipelineMapStrRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        noPipelineMapStrRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "map_str_any_in_pipeline_carrier",
			Note:        noPipelineMapStrNote + " | snippet: " + truncateNoPipelineMapStr(line),
		})
	}
	if commentOnly > 0 && !productionOnly {
		noPipelineMapStrWarn(r, "no-pipeline-mapstr-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// pkgFromNoPipelineMapStrRel extracts the package
// identifier from a repo-relative file path.
func pkgFromNoPipelineMapStrRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// truncateNoPipelineMapStr bounds the snippet surface at
// 120 chars to keep report JSON size stable.
func truncateNoPipelineMapStr(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

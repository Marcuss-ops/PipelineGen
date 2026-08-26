// Package scan — Step 9.c forward-prevention gate (July 2026,
// typed-carrier purity): the GenerateOneUseCase 4-phase
// pipeline must use strongly-typed carriers (PreparedGeneration,
// GeneratedDraft, ProcessedGeneration, FinalizeInputs,
// GenerationResult) for state that flows between the
// orchestrator and the phase carriers. `map[string]any` is the
// forbidden idiom for those carriers.
//
// A `map[string]any` typed field inside a pipeline carrier is
// a type-erasure smell: it makes it impossible to reason about
// the data flux as compile-time invariants, and it is the same
// shape that produced the diagnostic's "PipelineState map[string]any"
// anti-pattern (refactor: reduce GenerateOneUseCase to a
// 4-phase orchestrator, July 2026).
//
// EXEMPTIONS (necessary by design):
//   - `tracker.TrackEvent("event-type", "message", map[string]any{...})`
//     inlined metadata — that is observability surface, not a
//     pipeline carrier. We exempt ANY line containing
//     `.TrackEvent(` (the canonical call-site shape, including
//     `tracker.TrackEvent(`).
//
// scanner policy (mirrors percheck_upsert_points_sole_owner
// precedent):
//   - the gate's target surface is exactly four files:
//     1. internal/capabilities/scripts/usecase/generation_prepare.go
//     2. internal/capabilities/scripts/usecase/generation_engine.go
//     3. internal/capabilities/scripts/usecase/generation_postprocess.go
//     4. internal/capabilities/scripts/usecase/generation_finalize.go
//   - skip the scanner's own package prefix
//     (cmd/archcheck/scan) so the regex self-match inside the
//     scanner's docs/comments/strings doesn't trip the gate.
//   - exempt `_test.go` files.
//   - exempt comment-only matches → WARN residue bucket
//     (godlike/07 NO_FAKE_AVAILABILITY migration window).
//     productionOnly=true silences the WARN bucket.
//
// matched rule_id: `percheck_pipeline_map_carrier_ban`.
package boundaries

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

// pipelineMapCarrierBanSkipPathPrefixes is the scanner's
// own package exemption.
var pipelineMapCarrierBanSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// pipelineMapCarrierBanTargets is the EXACT set of files
// the gate scans. The four pipeline carriers (Preparer,
// EngineRunner, Postprocessor, Finalizer) live together in
// these files; the orchestrator carries their results but
// does NOT own typed fields beyond `GenerationTimings` and
// `GenerationResult`. Mapping the gate to a tight, finite
// file list keeps the rule deterministic and reviewable.
var pipelineMapCarrierBanTargets = []string{
	"internal/capabilities/scripts/usecase/generation_prepare.go",
	"internal/capabilities/scripts/usecase/generation_engine.go",
	"internal/capabilities/scripts/usecase/generation_postprocess.go",
	"internal/capabilities/scripts/usecase/generation_finalize.go",
}

// pipelineMapCarrierBanRule is the rule-family id.
const pipelineMapCarrierBanRule = "percheck_pipeline_map_carrier_ban"

// pipelineMapCarrierBanNote is the violation note.
const pipelineMapCarrierBanNote = "forbidden `map[string]any` literal inside a GenerateOneUseCase pipeline-carrier file (typed-carrier purity gate, July 2026). Pipeline state must flow through typed carriers (PreparedGeneration, GeneratedDraft, ProcessedGeneration, FinalizeInputs, GenerationResult); `map[string]any` is the type-erasure idiom previously guarded against. The ONLY allowed `map[string]any` usage in these files is the inline metadata argument of `tracker.TrackEvent(...)` (observability surface, NOT a pipeline carrier). Replace with a typed struct field (e.g. `type PostprocessTimings struct { EntitiesMs int64; MetadataMs int64; ... }`) or extend the existing carrier type. Test-fixture residue callers are documented in migrations/api/archcheck-strict-baseline.json (godlike/07 NO-FAKE-AVAILABILITY migration window)."

// pipelineMapCarrierBanRe matches the literal `map[string]any`
// token. The regex is a literal substring match — no
// disambiguation between type-assertion vs literal-init
// syntax — because the issue is the symbol's presence on a
// non-exempt line.
var pipelineMapCarrierBanRe = regexp.MustCompile(`map\[string\]any`)

// pipelineMapCarrierBanTrackEventRe detects the
// canonical TrackEvent call-site shape. A line containing
// `.TrackEvent(` is observability metadata and exempt.
var pipelineMapCarrierBanTrackEventRe = regexp.MustCompile(`\.TrackEvent\s*\(`)

// pipelineMapCarrierBanWarn emits the WARN residue entry.
func pipelineMapCarrierBanWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, pipelineMapCarrierBanRule+" "+label+" "+msg)
}

// ScanPipelineMapCarrierBan walks the gate's pre-canned
// target file set and emits a violation for any non-exempt
// `map[string]any` reference inside them.
func ScanPipelineMapCarrierBan(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol

	for _, target := range pipelineMapCarrierBanTargets {
		full := filepath.Join(root, target)
		relPath := filepath.ToSlash(target)

		// The scanner's own path is exempt so the regex
		// self-mentions inside the package's docs/comments
		// don't trip the gate.
		if hasAnyPathPrefix(relPath, pipelineMapCarrierBanSkipPathPrefixes) {
			continue
		}
		// _test.go targets skip.
		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}
		scanPipelineMapCarrierBanFile(full, relPath, r, productionOnly)
	}
}

// scanPipelineMapCarrierBanFile opens a single .go file and
// emits percheck_pipeline_map_carrier_ban violations for any
// line containing `map[string]any` unless the line is a
// TrackEvent call. Comment-only matches → WARN residue
// bucket. productionOnly=true silences the WARN bucket.
func scanPipelineMapCarrierBanFile(path, relPath string, r *report.Report, productionOnly bool) {
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
		if pipelineMapCarrierBanTrackEventRe.MatchString(line) {
			continue
		}

		// Comment-only matches → WARN residue bucket.
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			pipelineMapCarrierBanRe.MatchString(line) {
			commentOnly++
			continue
		}

		if !pipelineMapCarrierBanRe.MatchString(line) {
			continue
		}

		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromPipelineMapCarrierBanRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        pipelineMapCarrierBanRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "map_str_any_in_pipeline_carrier",
			Note:        pipelineMapCarrierBanNote + " | snippet: " + truncatePipelineMapCarrierBan(line),
		})
	}
	if commentOnly > 0 && !productionOnly {
		pipelineMapCarrierBanWarn(r, "pipeline-map-carrier-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// pkgFromPipelineMapCarrierBanRel extracts the package
// identifier from a repo-relative file path.
func pkgFromPipelineMapCarrierBanRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// truncatePipelineMapCarrierBan bounds the snippet surface
// at 120 chars to keep report JSON size stable.
func truncatePipelineMapCarrierBan(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

// Package scan — Check 78 (PR-DIAGNOSI-FINALE rule 1, July 2026):
// AssetBinder cannot import or reference SceneSynthesizer.
//
// scan/percheck_assetbinder_no_scenesynthesizer.go pins the
// godlike/06 SSOT that the SceneAssetBinder is the SOLE owner of
// the scene-binding responsibility, independent of the Scene-
// Synthesizer side. The canonical AssetBinder lives at
// `internal/capabilities/scripts/scene/binder.go` and the canonical
// SceneSynthesizer lives at `internal/capabilities/scripts/scene/
// synthesizer.go`. Synthesizing scenes via the Synthesizer from
// inside the binder is a godlike/06 SSOT drift: the two
// responsibilities (SHAPE the scene vs BIND the metadata) are
// intentionally separate. Routing binder through Synthesizer
// either re-runs synthesis (idempotent cost) OR overwrites the
// already-shaped scene.Text with synthesized prose (silent P0 #2
// invariant regression — same scene content might come from two
// sources).
//
// This gate is the forward-prevention fence for the SSOT: any
// production-code reference to SceneSynthesizer (as a type, a
// function call, or an import path) from inside the canonical
// binder file surfaces as a CI build failure. The Synthesizer
// itself remains a legitimate callable for the SceneAssetBinder's
// pre-binder orchestration (if such orchestration ever needs it),
// just NOT from inside the binder file.
//
// scanner policy (mirrors percheck_assetbinder_ssot precedent):
//   - scan scope: ONLY the canonical binder file
//     (internal/capabilities/scripts/scene/binder.go). Other
//     files in the scene/ package may legitimately reference
//     the Synthesizer.
//   - skip `_test.go` files (regression-guard surface
//     legitimately constructs Synthesizer stubs).
//   - skip the scan's own package (cmd/archcheck/scan/**).
//   - comment-only references to `SceneSynthesizer` are
//     residue-accounted as WARN (godlike/07).
//
// matched rule_id: `percheck_assetbinder_no_scenesynthesizer`.
package scan

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

// assetBinderSynthesizerScopePath is the canonical binder file
// whose scope this gate inspects. Only this file MUST be free of
// any SceneSynthesizer reference (import or symbol).
const assetBinderSynthesizerScopePath = "internal/capabilities/scripts/scene/binder.go"

// assetBinderSynthesizerForbiddenPatterns are the literal
// substrings the gate trips on. Both represent the SceneSynthesizer
// godlike/06 SSOT violation surface:
//   - "SceneSynthesizer" is the struct name (any reference
//     whether type, decl, or method invocation trips).
//   - "/scripts/scene/synthesizer"  is the canonical package
//     path (import "github.com/Marcuss-ops/PipelineGen/internal
//     /application/scripts/scene/synthesizer" trips).
var assetBinderSynthesizerForbiddenPatterns = []struct {
	pattern string
	desc    string
}{
	{"SceneSynthesizer", "reference to canonical SceneSynthesizer struct (synthesize.NewSceneSynthesizer, *SceneSynthesizer, etc.)"},
	{"/scripts/scene/synthesizer\"", "import of canonical scene/synthesizer package"},
}

// assetBinderSynthesizerForbiddenPatternRe is the union-match
// regex for fast line filtering. If a line matches this regex
// it MIGHT be a violation; the per-pattern loop below confirms
// the exact match.
var assetBinderSynthesizerForbiddenPatternRe = regexp.MustCompile(`SceneSynthesizer|/scripts/scene/synthesizer`)

// assetBinderSynthesizerRule is the rule-family id the scanner
// emits. Per the percheck_assetbinder_ssot precedent, the rule id
// mirrors the file basename (snake_case + descriptive suffix).
const assetBinderSynthesizerRule = "percheck_assetbinder_no_scenesynthesizer"

// assetBinderSynthesizerNote is the violation Note for any scene-
// synthesizer reference inside the canonical binder file. The
// message references the godlike/06 SSOT + the binder/synthesizer
// boundary so the migration path is auditable inline.
const assetBinderSynthesizerNote = "forbidden SceneSynthesizer reference inside the canonical SceneAssetBinder (PR-DIAGNOSI-FINALE rule 1, July 2026); godlike/06 SSOT requires the binder (internal/capabilities/scripts/scene/binder.go) to be the SOLE owner of scene-binding mutations. Synthesizer residence in the scene/ package is legitimate for pre-binder orchestration (e.g. prose-fallback synthesis of N scenes from M clips), but the binder MUST NOT call back into the Synthesizer (re-runs synthesis / overwrites already-shaped scene.Text — silent P0 #2 invariant regression). Route scene-synthesis through a typed port if the binder legitimately needs synthesized scenes."

// assetBinderSynthesizerWarn is the residue-emitter for comment-
// only references. Mirrors percheck_assetbinder_ssot's residue-
// accounting discipline.
func assetBinderSynthesizerWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, assetBinderSynthesizerRule+" "+label+" "+msg)
}

// ScanAssetBinderNoSynthesizer opens ONLY the canonical binder
// file and audits each line for SceneSynthesizer references.
// Inside the canonical binder, any SceneSynthesizer reference is
// a godlike/06 SSOT drift and trips the gate as SeverityError.
// Comment-only references are residue-accounted as WARN rather
// than violated (descriptive prose in surviving doc-strings does
// NOT trip CI on resubmission).
//
// Scope-boundary note: the gate's scope is ONLY the canonical
// binder file. Other files in the scene/ package (e.g.
// synthesizer.go, scene_planner.go) ARE allowed to reference
// SceneSynthesizer — that is the legitimate use of the struct.
// The gate prevents ROUND-TRIPPING (binder -> synthesizer ->
// binder) which is the violation class.
//
// productionOnly mode: silences the comment-only WARN bucket so
// the operator-facing "zero production-code hits" claim
// (PR-P12-PERCHECK-BASELINE-ZERO pattern) is auditable via
// len(r.Violations) == 0. The per-file commentOnly counter is
// still incremented so the audit lane stays residue-honest.
func ScanAssetBinderNoSynthesizer(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol
	path := filepath.Join(root, assetBinderSynthesizerScopePath)
	f, err := os.Open(path)
	if err != nil {
		// The canonical file IS the SSOT. If it cannot be
		// opened the operator MUST investigate. Surface a
		// violation rather than silently passing.
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/capabilities/scripts/scene",
			File:        assetBinderSynthesizerScopePath,
			Line:        0,
			Rule:        assetBinderSynthesizerRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_file_unreadable",
			Note:        assetBinderSynthesizerNote + " | cannot open canonical binder file: " + err.Error(),
		})
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

		// Skip the import line that imports the synthesizer
		// package ONLY when accompanied by the documented
		// allowlist marker `// SYNTHESIZER_ALLOWED_HERE: <pkg>`
		// — a future-proofing seam. Today NO file uses this
		// marker; the allowlist is documented for future use.
		if strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*") {
			if assetBinderSynthesizerForbiddenPatternRe.MatchString(line) {
				commentOnly++
			}
			continue
		}

		// No fast preprocessing: each pattern is matched
		// exactly so the violation Note carries the precise
		// matched-substring description.
		for _, p := range assetBinderSynthesizerForbiddenPatterns {
			if !strings.Contains(line, p.pattern) {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				Package:     "internal/capabilities/scripts/scene",
				File:        assetBinderSynthesizerScopePath,
				Line:        lineNo,
				Rule:        assetBinderSynthesizerRule,
				Severity:    string(report.SeverityError),
				MatchedRule: "binder_calls_synthesizer",
				Note: assetBinderSynthesizerNote +
					" | file: " + assetBinderSynthesizerScopePath +
					" | line: " + trimmed +
					" | forbidden pattern: " + p.pattern +
					" | description: " + p.desc,
			})
		}
	}
	if commentOnly > 0 && !productionOnly {
		assetBinderSynthesizerWarn(r, "binder-synthesizer-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) to SceneSynthesizer in "+assetBinderSynthesizerScopePath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability; replace or remove before next sweep)")
	}
}

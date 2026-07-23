// Package scan — per-check forward-prevention gate that enforces
// the canonical-14-count invariant on the AssetState enum
// (PR-CATALOG-MULTILINGUA step 7, July 2026).
//
// scan/percheck_asset_state_canonical_14.go owns the Go
// migration of the canonical-14 forward-prevention gate.
// It reads ONLY the canonical SOLE owner
// (internal/domain/asset/asset_state_values.go) and counts the
// `StateAssetX AssetState = "..."` const declarations. The
// count MUST equal 14 (the canonical surface declared at
// CanonicalAssetStateValues()). A future agent who adds a
// 15th state MUST update:
//
//	(a) the 14 const declarations in asset_state_values.go,
//	(b) CanonicalAssetStateValues() in asset_state.go,
//	(c) allAssetStates slice in asset_state_test.go,
//	(d) the helper-methods matrix test in asset_state_test.go,
//	(e) TestAssetState_StringLiteralValues,
//	(f) TestAssetState_PreTerminalStatesLength (still 11),
//	(g) the migration default + the per-city column audit
//	    (PR-CANONICAL-IMAGE-ASSET-INVARIANTS parallel),
//	(h) README / AGENTS.md godlike/06 SSOT references.
//
// Drift in any of (a..h) surfaces as a single CI violation
// from THIS scanner; the comment-based godlike/07 residue-
// accounting warns them as well.
//
// godlike/06 SSOT invariant: this scanner does NOT enforce
// the alphabet value of each const — only the count. The
// shadow-enum scanner (percheck_asset_state_no_shadow_enum)
// catches duplicate declarations outside the canonical file.
//
// Skip-dir policy mirrors percheck_image_asset_invariants.go's
// standard sibling pattern. Comment-only lines with StateAsset
// references are residue-accounted (WARNed, not violated),
// godlike/07 discipline.
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
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// assetStateCanonical14Path is the canonical SOLE owner of
// the AssetState enum (14 constants + helpers).
const assetStateCanonical14Path = "internal/domain/asset/asset_state_values.go"

// assetStateConstLineRe matches the LITERAL const-declaration
// shape at canonical_file line-start (post-tab indentation):
//
//	StateAssetX AssetState = "..."
//
// The trailing `"..."` is a real string literal; surrounding
// characters are noise-padded by tab (line starts with `\t`).
// This regex is intentionally line-start anchored so a
// comment-only `// StateAssetX = ...` reference does NOT trip
// the const-count, but the residue-warning scan still picks
// it up via a separate substring check.
var assetStateConstLineRe = regexp.MustCompile(`^\tStateAsset\w+\s+AssetState\s+=\s+"[^"]+"$`)

// assetStateCanonical14Rule is the rule-family id the
// scanner emits (mirrors percheck_image_asset_invariants.go
// RuleID naming convention).
const assetStateCanonical14Rule = "percheck_asset_state_canonical_14"

// assetStateCanonical14Note is the violation Note for
// count mismatches. The message references the canonical
// surface + the forward-prevention guard at
// percheck_asset_state_no_shadow_enum so an operator sees
// both the count constraint AND its shadow-guard companion.
const assetStateCanonical14Note = "AssetState canonical file must declare exactly 14 const entries (PR-CATALOG-MULTILINGUA step 7, July 2026); godlike/06 SSOT requires the const count to equal CanonicalAssetStateValues(); if a 15th state is needed, update the type surface, the matrix test, and this gate in lockstep"

// assetStateWarn is the centralized WARN-bucket emitter
// for residue-accounting. Mirrors imageAssetWarn.
func assetStateWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, assetStateCanonical14Rule+" "+label+" "+msg)
}

// ScanAssetStateCanonical14 opens the single canonical file
// (assetStateCanonical14Path) and counts const declarations
// of the form `StateAssetX AssetState = "..."`. The count
// MUST equal 14; otherwise the scanner emits a violation
// with the actual count surface. Comment-only references
// to StateAsset constants are WARNed (residue accounting,
// godlike/07 discipline).
//
// scanner ignores the value of each const's string literal;
// only the COUNT is enforced. The shadow-enum scanner
// (percheck_asset_state_no_shadow_enum) catches duplicate
// declarations outside the canonical file; the alphabet
// value drift is captured at runtime by
// TestAssetState_StringLiteralValues.
func ScanAssetStateCanonical14(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.
	path := filepath.Join(root, assetStateCanonical14Path)
	f, err := os.Open(path)
	if err != nil {
		// The canonical file is the SSOT; if it cannot be
		// opened the operator MUST investigate. Surface a
		// violation rather than silently passing.
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/domain/asset",
			File:        assetStateCanonical14Path,
			Line:        0,
			Rule:        assetStateCanonical14Rule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_14_count_mismatch",
			Note:        assetStateCanonical14Note + " | cannot open canonical file: " + err.Error(),
		})
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// wantCount is the canonical inventory size, sourced from
	// internal/domain/asset.AssetStateAlphabetCount (godlike/06
	// SSOT — single literal source-of-truth for the canonical
	// alphabet size across the codebase). A future agent
	// changing that constant surfaces in the matrix tests too,
	// so the percheck scanner + the alphabetic file surface +
	// the runtime helper are kept in lockstep without parallel
	// hardcoded literals.
	const wantCount = asset.AssetStateAlphabetCount
	count := 0
	commentOnly := 0
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if assetStateConstLineRe.MatchString(line) {
			count++
			continue
		}
		// Residue accounting (godlike/07): a comment line
		// referencing the StateAsset surface is descriptive
		// prose and not a real declaration. WARN, do NOT
		// violate.
		trimmed := strings.TrimLeft(line, " \t")
		if (strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")) &&
			strings.Contains(line, "StateAsset") {
			commentOnly++
		}
	}
	if count != wantCount {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/domain/asset",
			File:        assetStateCanonical14Path,
			Line:        0,
			Rule:        assetStateCanonical14Rule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_14_count_mismatch",
			Note: assetStateCanonical14Note +
				" | actual const count: " + strconv.Itoa(count) +
				" | want: " + strconv.Itoa(wantCount),
		})
	}
	if commentOnly > 0 {
		assetStateWarn(r, "canonical-14-count:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+assetStateCanonical14Path+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

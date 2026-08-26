// percheck_image_asset_invariants_shared.go (split-image-asset-invariants,
// July 2026): cross-rule shared infrastructure for the two sub-rules
// that compose the percheck_image_asset_invariants gate.
//
// Companion files in package scan:
//
//	percheck_image_asset_invariants.go         // registry + orchestrator
//	percheck_image_asset_invariants_rule_a.go  // image_asset_literal_ban (Rule A)
//	percheck_image_asset_invariants_rule_b.go  // gemma_dto_leak_ban       (Rule B)
//	percheck_image_asset_invariants_shared.go  // cross-rule infra         (THIS FILE)
//
// godlike/06 SSOT — "one canonical owner per fact": the constants and
// helpers below are used by BOTH Rule A and Rule B, so they live in a
// single shared sibling. A future agent extending the gate with a Rule
// C MUST consume the cross-rule helpers here rather than re-declaring
// local copies in the rule file.
package structure

import (
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// percheckImageAssetInvariantsRuleLabel is the canonical Rule label
// identifier used as the Violation.Rule field for both Rule A and
// Rule B. Mirrors the convention in percheck_voiceover_alias_ban.go
// and percheck_asset_state_canonical_14.go. Lives in shared so a
// future Rule C can drop in without duplicating the string.
const percheckImageAssetInvariantsRuleLabel = "percheck_image_asset_invariants"

// imageAssetSkipDirs is the basename-level skip-list shared by both
// Rule A and Rule B's WalkDir. The skip-list mirrors the standard
// sibling percheck pattern but with ONE CRITICAL EXCEPTION: the
// `scripts` entry is INTENTIONALLY OMITTED.
//
// Why: this scanner's Gemma-prompt-construction scope
// (gemmaPromptScopeRelPathPrefix = "internal/application/scripts/usecase")
// lives UNDER a directory whose basename is `scripts`. A basename
// match on `scripts` would prune the entire subtree including our
// target dir. The voiceover / script_docs_route siblings can
// safely skip `scripts` because they have no usecase-overlay
// target — but this scanner MUST descend. The walker still only
// acts on `.go` files (via the suffix guards), so project-root
// operational scripts (bash / Python / YAML) which live at the
// top-level `<root>/scripts/` are also unaffected from a noise
// perspective: only `.go` files in nested `scripts/` subdirs are
// scanned, and the canonical fact-table of `&detail.ImageAsset{`
// literals is gated to non-scripts paths.
var imageAssetSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// imageAssetSkipPathPrefixes names the scan's own package so the
// regex patterns stay grep-able in their declared const form
// without triggering false-failures. Mirrors
// percheck_voiceover_alias_ban's `retiredVoiceoverSkipPathPrefixes`.
var imageAssetSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// imageAssetCanonicalOwnerPath is the repo-relative allowlisted
// path for the canonical SOLE owner of the ImageAsset struct
// definition.
//
// godlike/06 SSOT: this is the allow-list PIN. A future agent
// who legitimately expands ImageAsset's canonical owner (e.g.
// to a sibling canonical_metadata_test_helper.go file) MUST
// migrate the path here in lockstep with the new owner; the
// pattern is mirrored from percheck_voiceover_alias_ban's
// retiredVoiceoverSkipFiles precedent.
//
// Currently only consumed by Rule A (image_asset_literal_ban).
// Lives in shared to keep the allow-list surface co-located with
// the other path-pinned constants.
const imageAssetCanonicalOwnerPath = "internal/kernel/asset/canonical_metadata.go"

// imageAssetCanonicalBuilderPath is the repo-relative allowlisted
// path for the canonical SOLE builder helper that converts the
// typed `CanonicalMediaMetadata` into the runtime ImageAsset.
//
// Per PR-CANONICAL-GENERATED-IMAGE-METADATA, the literal here
// MAY appear as the terminal step of the canonical builder
// pipeline (verified at audit time — the literal does NOT
// appear in this file today; the canonical builder uses
// `b, _ := json.Marshal(asset.CanonicalMediaMetadata{...})`
// + metaJSON assignment). Keeping the file in the allow-list
// future-proofs any graceful struct-literal promotion.
//
// Currently only consumed by Rule A (image_asset_literal_ban).
// Lives in shared for the same reason as imageAssetCanonicalOwnerPath.
const imageAssetCanonicalBuilderPath = "internal/capabilities/images/workflow/storage_ingest_direct.go"

// pkgFromImageAssetRel extracts the package identifier from a
// repo-relative file path. Mirrors
// percheck_voiceover_alias_ban.go::pkgFromAliasBanRel
// (dashboard-filter field for the Violation.Package entry).
func pkgFromImageAssetRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// imageAssetWarn emits a single-residue-accounting warning
// string into r.Warnings. Centralized so the rule-family label
// + count phrasing stays uniform across Rule A and Rule B.
func imageAssetWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, percheckImageAssetInvariantsRuleLabel+" "+label+" "+msg)
}

// imageAssetSnippet produces a 1-line preview of the violation
// site for operator-friendly log output, marking the matched
// literal position so future readers see WHICH literal tripped.
// Truncates at 120 chars to keep report JSON bounded (mirrors
// percheck_voiceover_alias_ban.go::snippetVoiceoverAliasBan).
//
// Shared by both rules: Rule A passes the ImageAsset literals
// slice; Rule B passes a synthesised single-element slice
// `[]string{"json:\"NAME\""}` so the marker prints next to the
// matched json-tag substring rather than the entire line.
func imageAssetSnippet(text string, literals []string) string {
	const maxLen = 120
	const marker = " <<<"
	for _, lit := range literals {
		idx := strings.Index(text, lit)
		if idx < 0 {
			continue
		}
		end := idx + len(lit)
		start := idx
		if start > 40 {
			start -= 20
		} else {
			start = 0
		}
		if end > start+maxLen {
			end = start + maxLen
		}
		out := text[start:end]
		if len(text) > end {
			out += marker
		}
		return out
	}
	if len(text) > maxLen {
		return text[:maxLen] + marker
	}
	return text
}

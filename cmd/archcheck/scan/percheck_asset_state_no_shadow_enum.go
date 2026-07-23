// Package scan — per-check forward-prevention gate that bans
// `StateAssetX AssetState = "..."` const declarations OUTSIDE
// the canonical SOLE owner at
// internal/domain/asset/asset_state_values.go
// (PR-CATALOG-MULTILINGUA step 7, July 2026).
//
// scan/percheck_asset_state_no_shadow_enum.go owns the Go
// migration of the no-shadow forward-prevention gate.
//
// godlike/06 SSOT invariant: AssetState is a 14-value enum
// declared at exactly one place (the canonical file). A
// shadow declaration in any other source file (production
// .go under internal/, cmd/, anywhere) is a godlike/06
// SSOT violation that risks inconsistent alphabet drift
// (godlike/07 NO-FAKE-AVAILABILITY regression).
//
// scanner policy (mirrors percheck_image_asset_invariants.go):
//   - skip file basenames `.git`, `vendor`, `node_modules`,
//     `node-scraper`, `examples`, `archivist`, `docs`, `data`.
//   - skip `_test.go` files (test stubs legitimately need
//     fixture declarations; the regression-guard surface
//     is exempt from the production-code ban).
//   - skip `cmd/archcheck/scan/**` (this scanner file +
//     sibling scanners reference the canonical literals for
//     greppability — false-positive exemption).
//   - allow the canonical SOLE owner
//     (internal/domain/asset/asset_state_values.go) — the SSOT.
//   - comment-only references to StateAsset are WARNed
//     (residue accounting, godlike/07).
//
// matched rule_id: `percheck_asset_state_no_shadow_enum`.
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

// assetStateShadowSkipDirs mirrors percheck_image_asset_invariants.go's
// standard skip-dir set (one CRITICAL EXCEPTION: do NOT add
// `scripts` here — the canonical SSOT path
// internal/domain/asset/ lives under no scripts dir, but
// future sibling surfaces might). Mirrors the rationale in
// imageAssetSkipDirs.
var assetStateShadowSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// assetStateShadowSkipPathPrefixes is the scan's own package
// exemption — this file declares a regex literal matching
// the production canonical shape. Mirrors
// imageAssetSkipPathPrefixes.
var assetStateShadowSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// assetStateShadowDeclRe matches the canonical const-declaration
// shape anywhere in a .go file. The pattern has three anchors:
//
//	StateAsset\w+   — a name starting with the canonical prefix.
//	[\w.\s]*         — the middle (alias Module.Type or just whitespace).
//	\bAssetState\s*= — the AssetState type identifier + assignment.
//
// Closing the match is a literal `"` (string constant). Comments
// are filtered out BEFORE this regex is applied (the comment-only
// pathway handles residue accounting). The regex DOES NOT require
// tab-indent — top-level (column 0) const declarations like
//
//	const StateAssetX asset.AssetState = "FOO"
//
// MUST match; the SSOT requires no `StateAssetX` declaration
// outside the canonical file regardless of column position.
var assetStateShadowDeclRe = regexp.MustCompile(`StateAsset\w+\b[\w.\s]*\bAssetState\s*=\s*"`)

// assetStateShadowNote is the violation Note string for
// shadow declarations. The message references the canonical
// SOLE owner + the forward-prevention gate so the operator
// sees the migration path inline.
const assetStateShadowNote = "forbidden `StateAssetX AssetState = \"...\"` const declaration outside canonical SOLE owner (internal/domain/asset/asset_state_values.go); godlike/06 SSOT permits AssetState's alphabet ONLY in the canonical file; route through the canonical SOLE owner to avoid alphabet drift (PR-CATALOG-MULTILINGUA step 7 forward-prevention gate)"

// assetStateShadowRule is the rule-family id this scanner
// emits. Mirrors percheck_image_asset_invariants.go
// MatchedRule naming.
const assetStateShadowRule = "percheck_asset_state_no_shadow_enum"

// assetStateWarnShadow is the shadow-scanner WARN emitter
// for residue-accounting. Mirrors assetStateWarn.
func assetStateWarnShadow(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, assetStateShadowRule+" "+label+" "+msg)
}

// ScanAssetStateNoShadowEnum walks every .go file under
// <root> and emits a violation for any
// `StateAssetX AssetState = "..."` const declaration outside
// the canonical SOLE owner + the scanner's own package.
// Test files (_test.go) are exempt.
func ScanAssetStateNoShadowEnum(root string, pol *policy.Policy, r *report.Report) {
	_ = pol
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if assetStateShadowSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				for _, prefix := range assetStateShadowSkipPathPrefixes {
					if relSlash == prefix || strings.HasPrefix(relSlash, prefix+"/") {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		// Canonical SOLE owner is exempt — that's where the
		// 14 const declarations live. Anything else declaring
		// a StateAssetX const is a SSOT violation.
		if relSlash == assetStateCanonical14Path {
			return nil
		}
		scanAssetStateShadowFile(path, relSlash, r)
		return nil
	})
}

// scanAssetStateShadowFile opens a single .go file and emits
// percheck_asset_state_no_shadow_enum violations for any line
// matching the canonical const-declaration shape via
// assetStateShadowDeclRe. Comment-only lines are residue-
// accounted (godlike/07).
//
// Scans BOTH top-level statements (column-0 `const …`) AND
// tab-indented statements (inside a `const ( … )` block) so
// any `StateAssetX … AssetState = "…"` declaration outside
// the canonical SOLE owner is surfaced as a violation.
func scanAssetStateShadowFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
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
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			strings.Contains(line, "StateAsset") {
			// Residue accounting: comment-only references
			// are descriptive prose, not real declarations.
			commentOnly++
			continue
		}
		// Regex match: any line containing the canonical
		// const-declaration shape is a shadow declaration
		// (outside the canonical SOLE owner). The regex
		// doesn't require tab-indent so top-level (column-0)
		// declarations are caught too.
		if !assetStateShadowDeclRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromAssetStateRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        assetStateShadowRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "shadow_enum_declaration",
			Note:        assetStateShadowNote + " | snippet: " + truncateForReport(line),
		})
	}
	if commentOnly > 0 {
		assetStateWarnShadow(r, "shadow-enum:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// truncateForReport bounds the snippet surface at 120 chars
// to keep report JSON size stable. Mirrors imageAssetSnippet
// in percheck_image_asset_invariants.go.
func truncateForReport(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

// pkgFromAssetStateRel extracts the package identifier from
// a repo-relative file path. Mirrors pkgFromImageAssetRel.
func pkgFromAssetStateRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

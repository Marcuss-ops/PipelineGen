// Package scan — per-check forward-prevention gate that enforces
// the canonical-14-count invariant on the AssetState enum
// (PR-CATALOG-MULTILINGUA step 7, July 2026).
//
// scan/percheck_asset_state_canonical_14.go owns the Go
// migration of the canonical-14 forward-prevention gate.
// It reads ONLY the canonical SOLE owner
// (internal/kernel/asset/asset_state_values.go) and counts the
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
package boundaries

import (
	"regexp"
)

// assetStateCanonical14Path is the canonical SOLE owner of
// the AssetState enum (14 constants + helpers).
const assetStateCanonical14Path = "internal/kernel/asset/asset_state_values.go"

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

// percheck_image_asset_invariants_rule_a.go (split-image-asset-invariants,
// July 2026): Rule A — `image_asset_literal_ban`.
//
// Sub-rule that bans direct `&asset.ImageAsset{...}` (and the
// domain-alias `&domainasset.ImageAsset{...}`) literal
// instantiation anywhere outside the canonical definition +
// canonical builder helper + test stub files.
//
// Companion files in package scan:
//
//	percheck_image_asset_invariants.go         // registry + orchestrator
//	percheck_image_asset_invariants_rule_a.go  // image_asset_literal_ban (Rule A) — THIS FILE
//	percheck_image_asset_invariants_rule_b.go  // gemma_dto_leak_ban  (Rule B)
//	percheck_image_asset_invariants_shared.go  // cross-rule helpers
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
// the canonical pipeline for building ImageAsset metadata is
// `asset.CanonicalMediaMetadata` → `json.Marshal` →
// `&asset.ImageAsset{ MetadataJSON: ... }`. A direct literal
// `&asset.ImageAsset{Origin: ..., Provider: ..., Hash: ..., Width: ...,
// Height: ..., ...}` (the 5 territory-bearing fields inline) bypasses
// the canonical metadata_json path AND risks a generation-path
// cross-invariant violation (Origin=generated must imply
// Provider=ProviderGoogleSlides per PR-CANONICAL-GENERATED-
// IMAGE-METADATA). Routing through the structured JSON
// surface enforces the invariant at marshal time.
//
// Excluded paths (mirrors percheck_voiceover_alias_ban.go +
// percheck_spec_aliases.go precedent):
//   - canonical SOLE owner (imageAssetCanonicalOwnerPath in shared)
//   - canonical SOLE builder (imageAssetCanonicalBuilderPath in shared)
//   - all *_test.go files (test stubs legitimately need
//     `&asset.ImageAsset{AssetID: "stub", ...}` fixtures)
//
// Comment-only WARN policy (godlike/07 residue accounting):
// identical to percheck_voiceover_alias_ban.go's precedent.
// Comment-only hits are SeverityWarn entries in r.Warnings (NOT
// in r.Violations). Production-code hits are SeverityError entries
// in r.Violations.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// imageAssetLiteralBanID is the canonical MatchedRule identifier
// for Rule A. Pinned so string-matching against the report stays
// deterministic across splits / refactors.
const imageAssetLiteralBanID = "image_asset_literal_ban"

// imageAssetLiterals are the two substrings Rule A scans for.
// Both prefixes must end with `{` for the substring match to
// disambiguate a struct literal `&asset.ImageAsset{` from a
// bare type-name reference `*asset.ImageAsset` (which is
// legitimately used as a typed signature in function args).
var imageAssetLiterals = []string{
	"&asset.ImageAsset{",
	"&domainasset.ImageAsset{",
}

// imageAssetRuleANote is the violation Note string for Rule A
// failures. The message references the canonical metadata_json
// SSOT + the generation-path cross-invariant so future agents
// reading the CI failure have the full context inline.
const imageAssetRuleANote = "forbidden `&asset.ImageAsset{...}` literal outside canonical SOLE owner + canonical builder; godlike/06 SSOT requires routing through asset.CanonicalMediaMetadata → JSON marshal → &asset.ImageAsset{MetadataJSON: ...} to enforce the generated-path cross-invariant (Origin=generated ↔ Provider=ProviderGoogleSlides); PR-CANONICAL-IMAGE-ASSET-INVARIANTS forward-prevention gate"

// imageAssetLiteralBanRule is the registry-resident Rule A
// implementation. The struct is unexported so future agents
// extend the gate via the registry, not by reaching past
// ScanImageAssetInvariants.
type imageAssetLiteralBanRule struct{}

func (imageAssetLiteralBanRule) Scan(root string, r *report.Report) {
	scanImageAssetLiterals(root, r)
}

// scanImageAssetLiterals walks the tree and emits image_asset_
// literal_ban Rule A violations for any non-exempt file containing
// one of `imageAssetLiterals` outside the comment-only bucket.
func scanImageAssetLiterals(root string, r *report.Report) {
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if imageAssetSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				for _, prefix := range imageAssetSkipPathPrefixes {
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
		// Allowlist bypass for canonical SOLE owner and canonical
		// builder. The path is the SSOT pin; future agents MUST
		// migrate this allowlist if/when ImageAsset's canonical
		// owner expands to another file.
		if relSlash == imageAssetCanonicalOwnerPath || relSlash == imageAssetCanonicalBuilderPath {
			return nil
		}
		scanImageAssetLiteralsFile(path, relSlash, r)
		return nil
	})
}

// scanImageAssetLiteralsFile opens a single .go file and emits
// Rule A violations per the godlike/07 residue-accounting pattern.
// Mirrors scanVoiceoverAliasBanFile structure.
func scanImageAssetLiteralsFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentCount := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if !imageAssetLineMatchesAnyLiteral(line) {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*") {
			// godlike/07 residue accounting: log but DO NOT
			// promote to a violation. Forward-pointer note in
			// the violation Note makes the rationale visible.
			commentCount++
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromImageAssetRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        percheckImageAssetInvariantsRuleLabel,
			Severity:    string(report.SeverityError),
			MatchedRule: imageAssetLiteralBanID,
			Note:        imageAssetRuleANote + " | snippet: " + imageAssetSnippet(line, imageAssetLiterals),
		})
	}
	if commentCount > 0 {
		imageAssetWarn(r, "Rule A (ImageAsset literal):",
			strconv.Itoa(commentCount)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// imageAssetLineMatchesAnyLiteral trims each matched literal
// substring and reports whether ANY imageAssetLiterals entry
// appears on the line. The match is a plain Contains — the
// disambiguation between string-literal and type-name is
// pinned to the trailing `{` in the literal declaration
// (no struct literal can be missing that brace).
func imageAssetLineMatchesAnyLiteral(line string) bool {
	for _, lit := range imageAssetLiterals {
		if strings.Contains(line, lit) {
			return true
		}
	}
	return false
}

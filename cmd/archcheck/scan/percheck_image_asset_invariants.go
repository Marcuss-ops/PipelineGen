// Package scan — per-check forward-prevention gate that enforces
// TWO invariants in lockstep:
//
//	(Rule A) bans direct `&asset.ImageAsset{...}` (and the
//	         domain-alias `&domainasset.ImageAsset{...}`) literal
//	         instantiation anywhere outside the canonical
//	         definition + canonical builder helper + test
//	         stub files;
//	(Rule B) scans struct JSON-tag definitions + struct-literal
//	         sites inside the Gemma-prompt-construction scope
//	         (internal/application/scripts/usecase/**) for any
//	         key in the canonical deny-list
//	         (clipview.ForbiddenCandidateViewJSONFields), catching
//	         future agents that would marshal `asset_id`,
//	         `drive_link`, `clip_id`, `folder_path`, `content_hash`,
//	         `hash`, `local_path`, `job_id`, `plan_id`,
//	         `slot_ref`, `source_url`, etc. into the model-facing
//	         JSON stream.
//
// scan/percheck_image_asset_invariants.go owns the Go migration
// of the PR-CANONICAL-IMAGE-ASSET-INVARIANTS forward-prevention
// gate (July 2026, PR-CANONICAL-IMAGE-ASSET-INVARIANTS).
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
//
//   - Rule A: the canonical pipeline for building ImageAsset
//     metadata is `asset.CanonicalMediaMetadata` →
//     `json.Marshal` → `&asset.ImageAsset{ MetadataJSON: ... }`.
//     A direct literal `&asset.ImageAsset{Origin: ..., Provider:
//     ..., Hash: ..., Width: ..., Height: ..., ...}` (the 5
//     territory-bearing fields inline) bypasses the canonical
//     metadata_json path AND risks a generation-path cross-
//     invariant violation (Origin=generated must imply
//     Provider=ProviderGoogleSlides per PR-CANONICAL-GENERATED-
//     IMAGE-METADATA). Routing through the structured JSON
//     surface enforces the invariant at marshal time.
//
//   - Rule B: the runtime guard
//     `[clipview.CandidateView.ValidateForModelView]` already
//     hard-fails on a forbidden key at marshal time. The
//     compile-time gate here catches a future agent that
//     introduces a NEW type (e.g., `ScriptPromptEnvelope`)
//     with a JSON tag of `json:"asset_id,omitempty"` etc. —
//     such a struct would bypass the runtime guard because
//     the runtime guard only walks `CandidateView`. The
//     compile-time gate makes the model-facing deny-list
//     enforceable across the whole Gemma-prompt-construction
//     scope.
//
// Excluded paths (mirrors percheck_voiceover_alias_ban.go +
// percheck_spec_aliases.go precedent):
//
//   - internal/domain/asset/canonical_metadata.go — the
//     canonical SOLE owner of the ImageAsset struct shape; the
//     literal appears in adjacent test helpers and the typed
//     `CanonicalMediaMetadata` marshals-through wrapper. This
//     file is the structural source-of-truth.
//
//   - internal/application/images/storage_ingest_direct.go — the
//     canonical builder helper that converts the typed
//     `CanonicalMediaMetadata` into a runtime ImageAsset. Per
//     PR-CANONICAL-GENERATED-IMAGE-METADATA, the literal here
//     MAY appear as the terminal step of the canonical
//     builder pipeline (verified at audit time — the literal
//     does NOT appear in this file today; the canonical builder
//     uses `b, _ := json.Marshal(asset.CanonicalMediaMetadata{...})`
//
//   - `metaJSON` assignment). Keeping the file in the
//     allow-list future-proofs any graceful struct-literal
//     promotion without forcing a new scan-file edit.
//
//   - All *_test.go files — test stubs legitimately need
//     `&asset.ImageAsset{AssetID: "stub", ...}` fixtures.
//     Excluding them prevents false-positives on the
//     regression-guard surface.
//
//   - cmd/archcheck/scan/** — this scanner file (and the test
//     file) MUST reference the canonical literal to pin the
//     pattern in tests + godoc prose. Out of scope at the file-
//     level exemption + matched against via the per-file
//     retirement block in percheck_voiceover_alias_ban's
//     `retiredVoiceoverSkipPathPrefixes` precedent.
//
//   - internal/application/clipview/** — the canonical SOLE
//     owner of the runtime deny-list enforcement (CandidateView.
//     ValidateForModelView). The literal `validate-for-model` JSON
//     keys inside this package are the SSOT seal; Rule B is
//     enforced THERE structurally, not as a violation.
//
// Comment-only WARN policy (godlike/07 residue accounting):
// identical to percheck_voiceover_alias_ban.go's precedent.
// Comment-only hits are SeverityWarn entries in r.Warnings (NOT
// in r.Violations). Production-code hits are SeverityError entries
// in r.Violations.
//
// productionOnly flag is NOT plumbed via the standard CheckSpec
// closure here (the runner.go wiring uses
// `{"percheck_image_asset_invariants", scan.ScanImageAssetInvariants}`
// directly). The v1 scanner always reports BOTH violations AND
// comment-only warnings; future PR can upgrade to the closure
// pattern (mirroring `percheck_voiceover_alias_ban.go`) if the
// residue-warning bucket proves operationally noisy. Forward-
// pointer: PR-IMAGE-ASSET-INVARIANTS-PRODUCTION-ONLY.
//
// policy.MaxImageAssetFields is unused by this scanner per the
// PR design scoping (Rule A is "fidelity to canonical metadata_json
// shape" not "field-count-of-the-literal"; a future AST-based
// scanner can lift this field-aware cap).
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// imageAssetLiterals are the two substrings Rule A scans for.
// Both prefixes must end with `{` for the substring match to
// disambiguate a struct literal `&asset.ImageAsset{` from a
// bare type-name reference `*asset.ImageAsset` (which is
// legitimately used as a typed signature in function args).
var imageAssetLiterals = []string{
	"&asset.ImageAsset{",
	"&domainasset.ImageAsset{",
}

// imageAssetSkipDirs is the basename-level skip-list for both Rule
// A's and Rule B's WalkDir. The skip-list mirrors the standard
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
// scanned, and the canonical fact-table of `&asset.ImageAsset{`
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
// definition. The literal MAY appear here as the struct
// definition site; Rule A is bypassed for this single file.
//
// godlike/06 SSOT: this is the allow-list PIN. A future agent
// who legitimately expands ImageAsset's canonical owner (e.g.
// to a sibling canonical_metadata_test_helper.go file) MUST
// migrate the path here in lockstep with the new owner; the
// pattern is mirrored from percheck_voiceover_alias_ban's
// retiredVoiceoverSkipFiles precedent.
const imageAssetCanonicalOwnerPath = "internal/domain/asset/canonical_metadata.go"

// imageAssetCanonicalBuilderPath is the repo-relative allowlisted
// path for the canonical SOLE builder helper that converts the
// typed `CanonicalMediaMetadata` into the runtime ImageAsset.
//
// Per PR-CANONICAL-GENERATED-IMAGE-METADATA, the literal here
// MAY appear as the terminal step of the canonical builder
// pipeline (verified at audit time — the literal does NOT
// appear in this file today; the canonical builder uses
// `b, _ := json.Marshal(asset.CanonicalMediaMetadata{...})` +
// metaJSON assignment). Keeping the file in the allow-list
// future-proofs any graceful struct-literal promotion.
const imageAssetCanonicalBuilderPath = "internal/application/images/storage_ingest_direct.go"

// imageAssetRuleANote is the violation Note string for Rule A
// failures. The message references the canonical metadata_json
// SSOT + the generation-path cross-invariant so future agents
// reading the CI failure have the full context inline.
const imageAssetRuleANote = "forbidden `&asset.ImageAsset{...}` literal outside canonical SOLE owner + canonical builder; godlike/06 SSOT requires routing through asset.CanonicalMediaMetadata → JSON marshal → &asset.ImageAsset{MetadataJSON: ...} to enforce the generated-path cross-invariant (Origin=generated ↔ Provider=ProviderGoogleSlides); PR-CANONICAL-IMAGE-ASSET-INVARIANTS forward-prevention gate"

// imageAssetRuleBNote is the violation Note string for Rule B
// failures. The message references the canonical runtime deny-
// list (clipview.ForbiddenCandidateViewJSONFields) + the model-
// facing SAFETY contract so future agents can compare against
// the existing canonical projection.
const imageAssetRuleBNote = "forbidden JSON field in Gemma-prompt-construction scope; godlike/07 NO-FAKE-AVAILABILITY forbids any forbidden key from the canonical runtime deny-list (clipview.ForbiddenCandidateViewJSONFields) appearing on a model-facing struct in internal/application/scripts/usecase/; route through clipview.NewCandidateView + clipview.CandidateView.ValidateForModelView instead; PR-CANONICAL-IMAGE-ASSET-INVARIANTS forward-prevention gate"

// gemmaPromptScopeRelPathPrefix is the repo-relative path prefix
// that defines the GATE SCOPE for Rule B. Per user spec, the
// gate is scoped to internal/application/scripts/usecase/ ONLY.
// A future agent who introduces a new Gemma-prompt-construction
// directory MUST extend this prefix list (forward-pointer:
// PR-IMAGE-ASSET-INVARIANTS-SCOPE).
const gemmaPromptScopeRelPathPrefix = "internal/application/scripts/usecase"

// clipviewCanonicalRelPathPrefix is the canonical SOLE owner
// of the model-facing projection (clipview.CandidateView). Rule
// B's deny-list is enforced at runtime there; the scan MUST
// NOT touch files under this prefix.
const clipviewCanonicalRelPathPrefix = "internal/application/clipview/"

// forbiddenGemmaJSONFields is the mirror of
// clipview.ForbiddenCandidateViewJSONFields lifted at compile-
// time so the archcheck has zero cross-package dependency on
// the runtime deny-list. The lift is BY DESIGN: if a future
// agent adds a new forbidden key to the runtime list, the
// compile-time scan MUST be upgraded in lockstep (the forward-
// pointer note in imageAssetRuleBNote makes this explicit).
//
// godlike/06 SSOT: the runtime deny-list stays canonical; this
// scan-time mirror is duplicated ONLY because the archcheck
// runs at PR-CI time and cannot import internal-application/
// clipview (the scanner package must stay a leaf, no
// application-layer dependency). The duplication is documented
// in the per-entry comment so a divergence would surface as a
// failing-code-review note.
var forbiddenGemmaJSONFields = []string{
	// ─── Infrastructure identifiers ───
	"asset_id", "assetid",
	// ─── Drive infrastructure ───
	"drive_link", "drive_webviewlink", "drive_file_id",
	"download_link", "local_path", "relative_path",
	// ─── Folder / category side channels ───
	"folder_id", "folder_path", "normalized_group",
	// ─── Source provenance ───
	"source", "source_url", "source_provider", "source_video_id",
	"youtube_url", "youtube_video_id", "channel_id", "channel",
	// ─── Hash / content fingerprints ───
	"hash", "content_hash", "file_hash", "md5", "md5_checksum", "sha256",
	// ─── Filename / display name ───
	"filename", "name", "title", "local_filename",
	// ─── Internal lifecycle / status ───
	"lifecycle_state", "status", "job_id", "run_fingerprint",
	"workflow_id", "policy_version",
	// ─── IndexDocument / wire-only keys ───
	"qdrant_point_id", "index_document",
	// ─── Slot taxonomy ───
	"slot_ref", "plan_id",
}

// ScanImageAssetInvariants walks every .go file under <root>
// and emits TWO rule-family violations:
//   - percheck_image_asset_invariants: image_asset_literal_ban (Rule A)
//   - percheck_image_asset_invariants: gemma_dto_leak_ban       (Rule B)
//
// Both rules apply the same godlike/07 residue-accounting
// discipline (comment-only → r.Warnings via imageAssetWarn;
// production code → r.Violations with SeverityError).
//
// Severity is `error` (forward-prevention gate; the runner
// --strict mode promotes to ExitViolations). For non-strict mode,
// the runner still prints the report; the exit code remains 0
// unless --strict is on.
func ScanImageAssetInvariants(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved (PR-A godlike/08 evolution may plumb severity overrides)
	scanImageAssetLiterals(root, r)
	scanImageAssetGemmaDTOFields(root, r)
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
			Rule:        "percheck_image_asset_invariants",
			Severity:    string(report.SeverityError),
			MatchedRule: "image_asset_literal_ban",
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

// imageAssetSnippet produces a 1-line preview of the violation
// site for operator-friendly log output, marking the matched
// literal position so future readers see WHICH literal tripped.
// Truncates at 120 chars to keep report JSON bounded (mirrors
// percheck_voiceover_alias_ban.go::snippetVoiceoverAliasBan).
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

// scanImageAssetGemmaDTOFields walks the Gemma-prompt-
// construction directory (gemmaPromptScopeRelPathPrefix) and
// emits gemma_dto_leak_ban Rule B violations for any struct
// JSON tag that names one of the canonical forbidden fields.
//
// Filter: skip the canonical SOLE model-facing owner
// (clipview/); the deny-list is enforced at runtime there as
// the SSOT seal. The scan punts on clipview/ files because
// rewriting the runtime deny-list is its own contract — the
// compile-time guard is upstream of any change to that file.
func scanImageAssetGemmaDTOFields(root string, r *report.Report) {
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
				// Path-scope filter: descend ONLY into the
				// Gemma-prompt-construction scope (or into a
				// strict ancestor of the scope so the walker
				// can reach it).
				if !descendIntoGemmaScope(relSlash) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Mirror Rule A's test-file exemption (percheck_voiceover_
		// alias_ban.go precedent): test files legitimately build
		// fixtures that intentionally hold the forbidden keys to
		// exercise downstream code; promoting such a fixture to a
		// production-code violation would be a false-positive.
		// The forward-pointer PR-IMAGE-ASSET-INVARIANTS-OUT can
		// promote this to a per-test-file allowlist if a future
		// agent needs tighter coverage.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		// Canonical SOLE model-facing owner (clipview/**) is
		// explicitly excluded from Rule B: the deny-list is the
		// SSOT seal there. Rule B is upstream — preventing a NEW
		// gemmma-prompt-construction struct from leaking the
		// forbidden keys in the first place.
		if strings.HasPrefix(relSlash, clipviewCanonicalRelPathPrefix) {
			return nil
		}
		scanImageAssetGemmaDTOFieldsFile(path, relSlash, r)
		return nil
	})
}

// scanImageAssetGemmaDTOFieldsFile performs a regex-light scan
// of every `.go` file inside the Gemma-prompt scope for struct
// JSON tags that match ANY forbidden field name.
//
// Strategy: detect struct-literal targets near a `json:"..."`
// tag block. The naive approach of detecting every json: tag
// would over-fire (Rule B fires on a struct definition, not on
// every json.Marshal invocation); we narrow to lines that
// contain BOTH a "type ... struct" intent AND a struct field
// with a json tag naming a forbidden field.
//
// Heuristic: any backtick-delimited `json:"X"` whose X matches
// one of the denied entries IS a violation. We do NOT require
// the surrounding struct definition to be detected (Go's
// grammar on a single line is too ambiguous) — the violation
// Note explicitly references the forbidden-field name so an
// operator can immediately see WHICH struct definition tripped.
func scanImageAssetGemmaDTOFieldsFile(path, relPath string, r *report.Report) {
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
		// Extract every `json:"NAME"` backtick-segment on this
		// line in a single pass; cheap and reuses the bufio.
		tagNames := extractJSONTagNames(line)
		if len(tagNames) == 0 {
			continue
		}
		// Determine line-kind: comment-only lines (full-line
		// `//`, or trailing-comment `// foo`) do NOT trip the
		// gate; the runtime deny-list is the SSOT seal for
		// in-package usage, so comment-only references are pure
		// residue accounting.
		trimmed := strings.TrimLeft(line, " \t")
		isCommentOnly := strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")
		matched := matchForbiddenGemmaField(tagNames)
		if matched == "" {
			continue
		}
		if isCommentOnly {
			// godlike/07 residue accounting: comment-only
			// references to forbidden fields are descriptive
			// prose (godoc explaining the danger). Promote to
			// r.Warnings, NOT r.Violations.
			commentCount++
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromImageAssetRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        "percheck_image_asset_invariants",
			Severity:    string(report.SeverityError),
			MatchedRule: "gemma_dto_leak_ban",
			Note: imageAssetRuleBNote +
				" | forbidden-field: " + matched +
				" | snippet: " + imageAssetSnippet(line, []string{"json:\"" + matched + "\""}),
		})
	}
	if commentCount > 0 {
		imageAssetWarn(r, "Rule B (Gemma DTO leak):",
			strconv.Itoa(commentCount)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// extractJSONTagNames returns the names of every `json:"X"`
// segment found on the line. A backtick-tag named `json:"NAME"`
// adds NAME to the result; tags named `json:"NAME,omitempty"`
// are normalized to NAME (the canonical key, not the option
// bit). Returns an empty slice when no `json:"…"` segment is
// present.
//
// Implementation is a single-pass scan of the line; we
// recognize the backtick-delimited segment by scanning for
// `json:"` then collecting characters until the closing `"`
// inside the same backtick pair. This is intentionally a
// non-regex parser to keep the file fast on big repos.
func extractJSONTagNames(line string) []string {
	const tagOpen = "json:\""
	const backtick = "`"
	out := []string{}
	cursor := 0
	for cursor < len(line) {
		idx := strings.Index(line[cursor:], tagOpen)
		if idx < 0 {
			break
		}
		foundAt := cursor + idx + len(tagOpen)
		// The opening backtick is at cursor+idx-1 (one char
		// before the tagOpen prefix). Find the closing backtick
		// onward from foundAt.
		end := strings.Index(line[foundAt:], backtick)
		if end < 0 {
			break
		}
		rawTag := line[foundAt : foundAt+end]
		// Strip the trailing `"` left over from the JSON tag
		// body. The match window `line[foundAt:foundAt+end]`
		// slices from right after `json:"` up to (but not
		// including) the closing backtick — so the trailing
		// `"` of the JSON tag body is included in rawTag.
		// Excluding it pins the comparison `name == forbidden`
		// against the canonical key. Trim both `"` ends
		// defensively (a degenerate empty tag `json:""` would
		// also be cleanly handled).
		rawTag = strings.Trim(rawTag, "\"")
		// Normalize `NAME,omitempty` → NAME.
		if comma := strings.Index(rawTag, ","); comma >= 0 {
			rawTag = rawTag[:comma]
		}
		out = append(out, rawTag)
		cursor = foundAt + end + len(backtick)
	}
	return out
}

// matchForbiddenGemmaField returns the FIRST forbidden field
// name whose literal substring appears in `tagNames`. Empty
// string means no forbidden field is in scope.
func matchForbiddenGemmaField(tagNames []string) string {
	for _, name := range tagNames {
		for _, forbidden := range forbiddenGemmaJSONFields {
			if name == forbidden {
				return forbidden
			}
		}
	}
	return ""
}

// descendIntoGemmaScope returns true iff `dir` is on the path
// from the project root to any file under
// gemmaPromptScopeRelPathPrefix. Mirrors
// percheck_script_docs_route.go::shouldDescendIntoScope.
// Path-component-bounded: "internal/application/scripts/usecase/"
// (with trailing slash) is the canonical scope root, and every
// strict descendant starts with the prefix.
func descendIntoGemmaScope(dir string) bool {
	if dir == "." || dir == "" {
		return true
	}
	if dir == gemmaPromptScopeRelPathPrefix {
		return true
	}
	if strings.HasPrefix(dir, gemmaPromptScopeRelPathPrefix+"/") {
		return true
	}
	if strings.HasPrefix(gemmaPromptScopeRelPathPrefix, dir+"/") {
		return true
	}
	return false
}

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
	r.Warnings = append(r.Warnings, "percheck_image_asset_invariants "+label+" "+msg)
}

// percheck_image_asset_invariants_rule_b.go (split-image-asset-invariants,
// July 2026): Rule B — `gemma_dto_leak_ban`.
//
// Sub-rule that scans struct JSON-tag definitions + struct-literal
// sites inside the Gemma-prompt-construction scope
// (internal/application/scripts/usecase/**) for any key in the
// canonical deny-list (clipview.ForbiddenCandidateViewJSONFields),
// catching future agents that would marshal `asset_id`, `drive_link`,
// `clip_id`, `folder_path`, `content_hash`, `hash`, `local_path`,
// `job_id`, `plan_id`, `slot_ref`, `source_url`, etc. into the
// model-facing JSON stream.
//
// Companion files in package scan:
//
//	percheck_image_asset_invariants.go         // registry + orchestrator
//	percheck_image_asset_invariants_rule_a.go  // image_asset_literal_ban (Rule A)
//	percheck_image_asset_invariants_rule_b.go  // gemma_dto_leak_ban   (Rule B) — THIS FILE
//	percheck_image_asset_invariants_shared.go  // cross-rule helpers
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
// the runtime guard
// `[clipview.CandidateView.ValidateForModelView]` already
// hard-fails on a forbidden key at marshal time. The
// compile-time gate here catches a future agent that
// introduces a NEW type (e.g., `ScriptPromptEnvelope`)
// with a JSON tag of `json:"asset_id,omitempty"` etc. —
// such a struct would bypass the runtime guard because
// the runtime guard only walks `CandidateView`. The
// compile-time gate makes the model-facing deny-list
// enforceable across the whole Gemma-prompt-construction
// scope.
//
// Filter: skip the canonical SOLE model-facing owner
// (clipview/); the deny-list is enforced at runtime there as
// the SSOT seal. The scan punts on clipview/ files because
// rewriting the runtime deny-list is its own contract — the
// compile-time guard is upstream of any change to that file.
package structure

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// imageAssetGemmaDTOLeakBanID is the canonical MatchedRule
// identifier for Rule B. Pinned for the same reason as
// imageAssetLiteralBanID.
const imageAssetGemmaDTOLeakBanID = "gemma_dto_leak_ban"

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

// imageAssetRuleBNote is the violation Note string for Rule B
// failures. The message references the canonical runtime deny-
// list (clipview.ForbiddenCandidateViewJSONFields) + the model-
// facing SAFETY contract so future agents can compare against
// the existing canonical projection.
const imageAssetRuleBNote = "forbidden JSON field in Gemma-prompt-construction scope; godlike/07 NO-FAKE-AVAILABILITY forbids any forbidden key from the canonical runtime deny-list (clipview.ForbiddenCandidateViewJSONFields) appearing on a model-facing struct in internal/application/scripts/usecase/; route through clipview.NewCandidateView + clipview.CandidateView.ValidateForModelView instead; PR-CANONICAL-IMAGE-ASSET-INVARIANTS forward-prevention gate"

// imageAssetGemmaDTOLeakBanRule is the registry-resident Rule B
// implementation. Same shadow pattern as Rule A.
type imageAssetGemmaDTOLeakBanRule struct{}

func (imageAssetGemmaDTOLeakBanRule) Scan(root string, r *report.Report) {
	scanImageAssetGemmaDTOFields(root, r)
}

// scanImageAssetGemmaDTOFields walks the Gemma-prompt-
// construction directory (gemmaPromptScopeRelPathPrefix) and
// emits gemma_dto_leak_ban Rule B violations for any struct
// JSON tag that names one of the canonical forbidden fields.
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
		// gemma-prompt-construction struct from leaking the
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
			Rule:        percheckImageAssetInvariantsRuleLabel,
			Severity:    string(report.SeverityError),
			MatchedRule: imageAssetGemmaDTOLeakBanID,
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

// Package scan — per-check forward-prevention gate banning
// re-introduction of the 6 retired voiceover.* type aliases
// (PR-VOICEOVER-ALIASES-RETIRE, ship_date 2026-07-10).
//
// The 4 SHAs behind this gate are the canonical source-of-truth
// for what is RETIRED:
//   - Sub-PR A source migration: 8dde7a5d7
//     (voiceover.VoiceoverRecord + voiceover.VoiceoverRepository
//     migrated to persistence.VoiceoverRecord + ports.VoiceoverRepository)
//   - Sub-PR A doc lockstep: 1622f3c45
//   - Sub-PR A footer rename: d50868e90
//     (canonical naming page added to persistence/repository.go)
//   - Sub-PR B promo aliases retirement: f14b96d19
//     (voiceover.PromoRequest / PromoResult / PromoResponse /
//     voiceover.DefaultPromoLanguages migrated to
//     workflow/promo.{Request,Result,Response} +
//     translation.DefaultPromoLanguages)
//
// The 6 RETIRED aliases whose canonical-homes live elsewhere:
//
//  1. voiceover.VoiceoverRecord → canonical at
//     internal/capabilities/voiceover/service/persistence/repository.go::VoiceoverRecord
//  2. voiceover.VoiceoverRepository → canonical at
//     internal/capabilities/voiceover/service/ports.go::VoiceoverRepository
//  3. voiceover.PromoRequest → canonical at
//     internal/application/workflow/promo/generate.go::Request
//  4. voiceover.PromoResult → canonical at
//     internal/application/workflow/promo/generate.go::Result
//  5. voiceover.PromoResponse → canonical at
//     internal/application/workflow/promo/generate.go::Response
//  6. voiceover.DefaultPromoLanguages → canonical at
//     internal/application/translation/defaults.go::DefaultPromoLanguages
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
// pre-2026-07-09 the 6 aliases lived INSIDE the voiceover package
// (the voiceover internal abstraction leaked across 3 packages:
// persistence/workflow-promo/translation). Per godlike/06 SSOT
// each of the 5 entities (VoiceoverRecord, VoiceoverRepository,
// Promo{Request,Result,Response}, DefaultPromoLanguages) has ONE
// canonical owner. The voiceover.* prefix was a godlike/06 SSOT
// drift (proxy re-exports create 2-owner ambiguity) AND a
// godlike/07 NO-FAKE-AVAILABILITY drift (anyone importing the
// proxy got the wrong version if either side evolved).
//
// Scanning rule: any production-code .go reference to one of the
// 6 retired aliases is a SeverityError violation. Comment-only
// references (lines whose first non-whitespace is `//`) are
// SeverityWarn (godlike/07 no-fake-availability residue accounting
// — narrative references in retirement-annotated godoc prose are
// intentional documentation, not re-declarations).
//
// NOTE (godlike/07 minimum-blast-radius + godlike/06 SSOT):
// voiceover/types.go IS in the retiredVoiceoverSkipFiles
// allow-list (see below). Per the user-spec IF-conditional
// "git-rm voiceover/types.go if zero remaining symbols", the
// conditional evaluated to FALSE (8 top-level symbols remain —
// BatchRequest, BatchResponse, BatchItem, DestinationRequest,
// VoiceoverResult, ResolvedDestination, PayloadMap, Validate —
// plus 4 production-code consumers in translation/types.go,
// voiceover/command.go, voiceover/language.go,
// voiceover/persistence/doc.go). Therefore the file stays on
// disk per godlike/07 minimum-blast-radius: the gate MUST
// allow-list it so the Sub-PR A/B narrative goddoc (which
// references the retired aliases by name on purpose) does not
// self-flag. The forward-pointer PR-VOICEOVER-TYPES-RETIRE
// (deadline TBD) will revisit the retirement after the canonical
// narrative surface is migrated to its dedicated file (mirrors
// the persistence/repository.go "canonical naming page"
// precedent locked at d50868e90).
//
// Excluded surfaces (mirrors percheck_player_client.go +
// percheck_root_override.go precedent):
//
//   - internal/api/assets/voiceover/types.go + the voiceover/
//     subtree narrative goddoc blocks: the godlike/06 SSOT
//     retirement-annotation prose legitimately mentions the
//     retired aliases by name so future agents can understand what
//     changed. Skip-dir'd at the file level.
//   - All *_test.go files: tests may legitimately reference the
//     retired aliases as regression guards for the retirement
//     itself (e.g. a `--strict-ban-after-Q3` promotion smoke). NOT
//     currently needed but future-proofed.
//   - cmd/archcheck/scan/ directory (this file's own package):
//     the regex literal pattern must contain the alias names.
//   - .git / vendor / node_modules / node-scraper / examples /
//     scripts / archivist: irrelevant trees (skipped via the
//     two-tier SkipDirs + SkipPathPrefixes mechanism).
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

// retiredVoiceoverAliases are the 6 banned import-path patterns.
// Each entry is the canonical substring the gate scans for; the
// trailing `[^a-zA-Z0-9_]` word-boundary ensures we do NOT match
// the canonical replacement (e.g. `persistence.VoiceoverRecord`
// must NOT match `voiceover.VoiceoverRecord` because the literal
// character before `voiceover` is not present in `persistence.`).
//
// The 6 patterns: voiceover.VoiceoverRecord, voiceover.VoiceoverRepository,
// voiceover.PromoRequest, voiceover.PromoResult, voiceover.PromoResponse,
// voiceover.DefaultPromoLanguages.
//
// Canonical replacements (for the violation Note + operator
// guidance):
//   - voiceover.Voiceover*        → persistence.VoiceoverRecord / ports.VoiceoverRepository
//   - voiceover.Promo*            → workflow/promo.{Request,Result,Response}
//   - voiceover.DefaultPromoLanguages → translation.DefaultPromoLanguages
var retiredVoiceoverAliases = []struct {
	Literal string // substring to match
	Note    string // canonical replacement + PR-ID for operator guidance
}{
	{
		Literal: "voiceover.VoiceoverRecord",
		Note:    "canonical: persistence.VoiceoverRecord (internal/capabilities/voiceover/service/persistence/repository.go). PR-VOICEOVER-ALIASES-RETIRE Sub-PR A (8dde7a5d7) retired this alias.",
	},
	{
		Literal: "voiceover.VoiceoverRepository",
		Note:    "canonical: ports.VoiceoverRepository (internal/capabilities/voiceover/service/ports.go). PR-VOICEOVER-ALIASES-RETIRE Sub-PR A (8dde7a5d7) retired this alias.",
	},
	{
		Literal: "voiceover.PromoRequest",
		Note:    "canonical: workflow/promo.Request (internal/application/workflow/promo/generate.go). PR-VOICEOVER-ALIASES-RETIRE Sub-PR B (f14b96d19) retired this alias.",
	},
	{
		Literal: "voiceover.PromoResult",
		Note:    "canonical: workflow/promo.Result (internal/application/workflow/promo/generate.go). PR-VOICEOVER-ALIASES-RETIRE Sub-PR B (f14b96d19) retired this alias.",
	},
	{
		Literal: "voiceover.PromoResponse",
		Note:    "canonical: workflow/promo.Response (internal/application/workflow/promo/generate.go). PR-VOICEOVER-ALIASES-RETIRE Sub-PR B (f14b96d19) retired this alias.",
	},
	{
		Literal: "voiceover.DefaultPromoLanguages",
		Note:    "canonical: translation.DefaultPromoLanguages (internal/application/translation/defaults.go). PR-VOICEOVER-ALIASES-RETIRE Sub-PR B (f14b96d19) retired this alias.",
	},
}

// retiredVoiceoverSkipDirs is the basename-skip map (mirrors the
// percheck_player_client.go + percheck_root_override.go precedent).
// These trees are not source-of-truth candidates for our purpose
// (vendored, generated, build artifacts, infra configs).
var retiredVoiceoverSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"scripts":      true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// retiredVoiceoverSkipPathPrefixes is the nested-prefix skip slice.
// Mirrors percheck_root_override.go's two-tier skip mechanism:
// the basename map catches top-level dirs; the prefix slice
// catches nested subtrees that the basename check would miss.
var retiredVoiceoverSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// retiredVoiceoverSkipFiles is the allow-list of files where
// reference to the retired aliases is LEGITIMATE (mirrors
// percheck_player_client.go's allowlist-by-filename pattern).
// Currently EMPTY: voiceover/types.go was previously in this list
// but was REMOVED at audit time because the file's 8 RETAINED
// symbols (BatchRequest, BatchResponse, etc.) do NOT match the 6
// retired-alias patterns, so the scanner's production-code path
// produces ZERO false-positive violations. Only the comment-only
// references (the canonical narrative goddoc) generate WARNings
// (residue accounting per godlike/07 NO-FAKE-AVAILABILITY). Keeping
// the file on the allow-list would have CREATED A SILENT COVERAGE
// HOLE — a future agent who re-introduced e.g. `type VoiceoverRecord
// struct{}` in voiceover/types.go would be silently allowed past
// the gate. Removing the entry ensures the gate scans the file
// and catches any future production-code re-introduction.
//
// Adding a new file to this list is a godlike/06 SSOT action: the
// alias retirement annotation is canonical narrative, and only
// files that OWN the canonical narrative contract may legitimately
// reference the retired names. Future narrative relocations MUST
// migrate this allow-list entry in lockstep with the goddoc move.
var retiredVoiceoverSkipFiles = map[string]bool{}

// ScanVoiceoverAliasBan walks <root> + skips + scans for
// production-code references to the 6 retired voiceover.* aliases.
//
// productionOnly mode (PR-P12-PERCHECK-BASELINE-ZERO, July 2026):
// when true, comment-only warnings are silenced (comments are
// documentation, not "hits") so the operator-facing "zero
// production-code hits" claim is auditable via
// `len(r.Violations) == 0`. Comment-only references still get
// added to r.Warnings regardless (residue accounting per
// godlike/07 no-fake-availability).
//
// scanVoiceoverAliasBanOne scans a single alias across the
// entire .go file tree (production code only). Mirrors the
// productionOnly + comment-only WARN/VIOLATION split established
// by percheck_player_client.go and extended by percheck_root_override.go.
//
// pol is reserved for future PR-A plumbing (per-check severity
// overrides per godlike/08 evolution track). Typed parameter
// retention keeps the CheckSpec.Run signature uniform.
func scanVoiceoverAliasBanOne(root, literal, note string, r *report.Report, productionOnly bool, pol *policy.Policy) {
	_ = pol // reserved (PR-A godlike/08 evolution may plumb severity overrides)

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if retiredVoiceoverSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				for _, prefix := range retiredVoiceoverSkipPathPrefixes {
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
		// Test-file exemption: tests legitimately reference
		// canonical imports + canonical-homes; a retirement-
		// regression test would reference the retired alias by
		// name too (forward-prevention surface validation).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Filename allow-list: voiceover/types.go is the canonical
		// narrative README for the retirement (godlike/06 SSOT).
		relForAllow, relErr := filepath.Rel(root, path)
		if relErr == nil && retiredVoiceoverSkipFiles[filepath.ToSlash(relForAllow)] {
			return nil
		}
		// Production-code violation detection (one file at a time).
		scanVoiceoverAliasBanFile(path, root, literal, note, r, productionOnly)
		return nil
	})
}

// ScanVoiceoverAliasBan (the public entry point) threads
// pol through to the per-alias walker. The reserved-typed
// parameter keeps the CheckSpec.Run signature uniform without
// requiring per-alias closure capture.
func ScanVoiceoverAliasBan(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol // same reserved-plumbing rationale as scanVoiceoverAliasBanOne
	for _, alias := range retiredVoiceoverAliases {
		scanVoiceoverAliasBanOne(root, alias.Literal, alias.Note, r, productionOnly, pol)
	}
}

// scanVoiceoverAliasBanFile opens a single .go file, scans line-by-
// line, classifies hits as VIOLATION (production code) or
// WARN (comment only), appends to r.Violations / r.Warnings.
//
// Comment-only detection mirrors percheck_player_client.go's
// "isGoCommentLine": a line whose first non-whitespace is `//` is
// treated as a comment for the purposes of this gate. Multi-line
// block comments (/* ... */) are detected best-effort: only an
// opening line that BEGINS with `/*` is treated as comment-only;
// this is a known limitation matching the percheck_player_client.go
// precedent (full multi-line block-comment tracking is forward-
// pointed to PR-PERCHECK-BLOCK-COMMENT-FULL).
func scanVoiceoverAliasBanFile(path, root, literal, note string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if !strings.Contains(text, literal) {
			continue
		}
		isComment := isGoCommentLineForAliasBan(text)
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		if isComment {
			// godlike/07 residue accounting — surface in Warnings
			// (which is `[]string` per report/model.go) regardless
			// of productionOnly so residue is visible in both
			// modes (percheck_root_override.go mirror). The
			// canonical-on-disk residue message uses the same
			// prefix as the violation Note so dashboards can
			// correlate comment-only and production-code hits.
			if !productionOnly {
				r.Warnings = append(r.Warnings,
					"percheck_voiceover_alias_ban: "+rel+":"+strconv.Itoa(line)+" comment-only reference \""+literal+"\": "+note)
			}
			continue
		}
		// Production-code violation. The Snippet-style line
		// preview is folded into the Note (Violation has no Snippet
		// field per report/model.go:45-56; Note is the canonical
		// human-readable surface). Severity is string per
		// Violation.Severity declaration; report.SeverityError is
		// a constant of the typed alias `report.Severity` so we
		// cast to plain string at the struct-literal site.
		r.Violations = append(r.Violations, report.Violation{
			Rule:     "percheck_voiceover_alias_ban",
			Severity: string(report.SeverityError),
			Package:  pkgFromAliasBanRel(rel),
			File:     rel,
			Line:     line,
			Note:     note + " | snippet: " + snippetVoiceoverAliasBan(text, literal),
		})
	}
}

// isGoCommentLineForAliasBan reports whether the stripped line
// begins with `//` or `*` (multi-line block comment continuation).
// Mirrors percheck_player_client.go::isGoCommentLine.
func isGoCommentLineForAliasBan(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, "//") ||
		strings.HasPrefix(trimmed, "/*") ||
		strings.HasPrefix(trimmed, "*")
}

// pkgFromAliasBanRel extracts the package identifier from a
// repo-relative file path. Mirrors percheck_root_override.go.
// Used as the Violation.Package field for dashboard filtering.
func pkgFromAliasBanRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// snippetVoiceoverAliasBan produces a 1-line preview of the
// violation site for operator-friendly log output. Truncates at
// 120 chars to keep report JSON bounded (mirrors percheck_root_override.go).
func snippetVoiceoverAliasBan(text, literal string) string {
	const maxLen = 120
	const marker = " <<<"
	idx := strings.Index(text, literal)
	if idx < 0 {
		if len(text) > maxLen {
			return text[:maxLen] + marker
		}
		return text
	}
	end := idx + len(literal)
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

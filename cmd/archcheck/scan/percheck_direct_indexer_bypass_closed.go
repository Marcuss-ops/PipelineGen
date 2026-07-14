// Package scan — Check Card 7.2 (July 2026): forward-prevention
// gate for the closed DirectIndexer bypass.
//
// The legacy `DirectIndexer` (and its associated symbols:
// NewDirectIndexer, WithAdminReindex, IsAdminReindex,
// SetAdminReindexAuditLogger, AdminReindexKey, ErrDirectIndexerAbuse)
// was the BYPASS surface for admin reindex that allowed callers
// to mutate media_assets + write to outbox_events without
// routing through the canonical AssetCommitter chain. Card 7
// (July 2026) closed the bypass; Card 7.1 added the
// `force=true` seam on BuildReindexEnvelopeV1Force for the
// canonical admin reindex path.
//
// godlike/06 SSOT (one canonical owner per fact): the
// admin-reindex write surface is `outboxevents.Repository.Enqueue`
// invoked from `outboxRepairAdapter.EnqueueReindex` in
// `cmd/admin/reconcile_qdrant_adapters.go`. The legacy
// DirectIndexer surface is RETIRED — no production path
// may reintroduce it. This gate is the forward-prevention
// fence: any production-code reference to one of the 7
// deleted symbols trips the build as SeverityError.
//
// scanner policy (mirrors percheck_voiceover_alias_ban +
// percheck_asset_committer_event_ssot precedent):
//   - skip standard excluded dirs (.git, vendor,
//     node_modules, node-scraper, examples, archivist,
//     docs, data).
//   - skip `cmd/archcheck/scan/**` (this scanner + sibling
//     scanners reference the deleted symbols for documentation
//     purposes — false-positive exemption).
//   - skip `_test.go` files (regression-guard surface
//     legitimately needs fixture setups + the percheck test
//     file itself references the symbols for hermetic
//     verification).
//   - two RESIDUE files document the closure and are
//     exempt-as-WARN (NOT as violation):
//   - internal/infrastructure/database/sqlite/outbox/dispatcher.go
//     (header comment: "(DirectIndexer) was removed in
//     Card 7 (July 2026)")
//   - cmd/admin/reconcile_qdrant_adapters.go
//     (docstring references the historical context)
//     These are residue-accounted per godlike/07 discipline;
//     the per-check operator-facing "zero production-code hits"
//     claim remains auditable via len(r.Violations) == 0.
//   - comment-only references (// line, /* block, * line
//     continuation) outside the residue files are also
//     residue-accounted as WARN, NOT as violation.
//   - productionOnly mode silences the comment-only WARN
//     bucket (mirrors percheck_voiceover_alias_ban).
//
// matched rule_id: `percheck_direct_indexer_bypass_closed`.
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

// directIndexerBypassClosedSymbols is the canonical 7-symbol set
// the Card 7 closure retired. This is the SINGLE SOURCE OF
// TRUTH for the bypass symbol set: the regex below is built from
// it, and the percheck test references the same slice for the
// per-symbol lock. A future contributor who adds an 8th symbol
// to the closure (or removes one) MUST update this slice — the
// regex + the test stay in lockstep via the shared var.
//
// The 7 symbols (Card 7 closure, July 2026):
//   - NewDirectIndexer                — constructor
//   - DirectIndexer                   — type
//   - WithAdminReindex                — option setter
//   - IsAdminReindex                  — predicate
//   - SetAdminReindexAuditLogger      — audit logger setter
//   - AdminReindexKey                 — config key
//   - ErrDirectIndexerAbuse           — sentinel error
var directIndexerBypassClosedSymbols = []string{
	"NewDirectIndexer",
	"DirectIndexer",
	"WithAdminReindex",
	"IsAdminReindex",
	"SetAdminReindexAuditLogger",
	"AdminReindexKey",
	"ErrDirectIndexerAbuse",
}

// directIndexerBypassClosedRe matches production-code
// references to ANY of the 7 deleted bypass symbols as
// standalone tokens (word boundaries prevent sub-string
// false positives like "NewDirectIndexerFoo" or
// "DirectIndexersAreBad").
//
// Built from directIndexerBypassClosedSymbols so there is a
// single source of truth for the symbol set.
var directIndexerBypassClosedRe = regexp.MustCompile(
	`\b(` + strings.Join(directIndexerBypassClosedSymbols, "|") + `)\b`,
)

// directIndexerBypassClosedRule is the rule-family id the
// scanner emits.
const directIndexerBypassClosedRule = "percheck_direct_indexer_bypass_closed"

// directIndexerBypassClosedNote is the violation Note string.
// References the canonical owner + the migration path so the
// operator sees the fix inline.
const directIndexerBypassClosedNote = "Card 7 bypass closure (July 2026): the DirectIndexer surface was the BYPASS around the canonical AssetCommitter chain; the admin reindex path is NOW `outboxRepairAdapter.EnqueueReindex` in `cmd/admin/reconcile_qdrant_adapters.go` (force=true → BuildReindexEnvelopeV1Force → outboxevents.Repository.Enqueue). Reintroducing ANY of the 7 deleted symbols (NewDirectIndexer, DirectIndexer, WithAdminReindex, IsAdminReindex, SetAdminReindexAuditLogger, AdminReindexKey, ErrDirectIndexerAbuse) is a godlike/06 SSOT violation (no canonical owner for admin reindex) AND a godlike/07 NO-FAKE-AVAILABILITY regression risk (silent QDRANT-002 atomicity loss). Route every admin reindex through the canonical outbox envelope builder; do NOT re-introduce the direct-write surface"

// directIndexerBypassClosedSkipDirs is the standard skip-dir
// set (mirrors percheck_voiceover_alias_ban + percheck_asset_committer_event_ssot).
var directIndexerBypassClosedSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// directIndexerBypassClosedSkipPathPrefixes is the scan's
// own-package exemption (the scanner + sibling scanners
// reference the deleted symbols for documentation purposes).
var directIndexerBypassClosedSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// directIndexerBypassClosedResidueFiles is the set of files
// that LEGITIMATELY reference the deleted symbols in
// descriptive prose (the closure commit + the canonical
// admin reindex adapter's history comment). These emit a
// WARN, NOT a violation — they're the documented residue
// per godlike/07.
var directIndexerBypassClosedResidueFiles = map[string]bool{
	"internal/infrastructure/database/sqlite/outbox/dispatcher.go": true,
	"cmd/admin/reconcile_qdrant_adapters.go":                       true,
}

// directIndexerBypassClosedWarn is the WARN-bucket emitter
// for residue accounting (godlike/07).
func directIndexerBypassClosedWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, directIndexerBypassClosedRule+" "+label+" "+msg)
}

// ScanDirectIndexerBypassClosed walks every .go file under
// <root> and emits a violation for any production-code
// (NOT _test.go, NOT comment-only, NOT in the documented
// residue file set) reference to one of the 7 deleted bypass
// symbols. Comment-only references and residue files are
// WARNed (silenced under productionOnly mode).
func ScanDirectIndexerBypassClosed(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol // reserved for future SeverityOverride plumbing.

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if directIndexerBypassClosedSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, directIndexerBypassClosedSkipPathPrefixes) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		// _test.go files are exempt — regression-guard surface
		// legitimately needs fixture setups (matches the
		// percheck_voiceover_alias_ban precedent).
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		// Out-of-scope (NOT under internal/, tests/, or cmd/):
		// skip without scanning.
		if !strings.HasPrefix(relSlash, "internal/") &&
			!strings.HasPrefix(relSlash, "tests/") &&
			!strings.HasPrefix(relSlash, "cmd/") {
			return nil
		}
		scanDirectIndexerBypassClosedFile(path, relSlash, r, productionOnly)
		return nil
	})
}

// scanDirectIndexerBypassClosedFile opens a single .go file
// and emits percheck_direct_indexer_bypass_closed violations
// for any line matching one of the 7 deleted symbols. Comment-
// only references and residue-file references are WARNed.
func scanDirectIndexerBypassClosedFile(path, relPath string, r *report.Report, productionOnly bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	isResidueFile := directIndexerBypassClosedResidueFiles[relPath]

	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if !directIndexerBypassClosedRe.MatchString(line) {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")

		// Comment-only line: residue-accounted as WARN.
		// (Matches percheck_asset_committer_event_ssot discipline.)
		isComment := strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")
		if isComment || isResidueFile {
			commentOnly++
			continue
		}

		// Production-code hit: emit a violation. The matched
		// symbol is captured into the note via a `matched: SYM`
		// field so the per-symbol test lock (and any operator
		// scanning the JSON report) can find the EXACT symbol
		// without substring-ambiguity vs sibling symbols
		// (e.g. "NewDirectIndexer" vs "DirectIndexer" both
		// substring-match the other). The per-symbol test
		// searches for "matched: <sym>" with the space-after-
		// colon, NOT for the bare symbol.
		match := directIndexerBypassClosedRe.FindString(line)
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromDirectIndexerBypassClosedRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        directIndexerBypassClosedRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "bypass_symbol_reintroduction",
			Note:        directIndexerBypassClosedNote + " | matched: " + match + " | snippet: " + truncateDirectIndexerBypassClosed(line),
		})
	}
	if commentOnly > 0 && !productionOnly {
		directIndexerBypassClosedWarn(r, "bypass-symbol-residue:",
			strconv.Itoa(commentOnly)+" comment-only / residue-file reference(s) to deleted bypass symbols in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// pkgFromDirectIndexerBypassClosedRel extracts the package
// identifier from a repo-relative file path.
func pkgFromDirectIndexerBypassClosedRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// truncateDirectIndexerBypassClosed bounds the snippet
// surface at 120 chars to keep report JSON size stable.
func truncateDirectIndexerBypassClosed(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

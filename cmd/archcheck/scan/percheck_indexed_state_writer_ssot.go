// Package scan — percheck_indexed_state_writer_ssot.go (Wave YY,
// INDEXED state SSOT forward-prevention, July 2026).
//
// Forward-prevention per-check that BANS the SQL write pattern
// `index_state = 'INDEXED'` from any file OUTSIDE the canonical
// clipindexer package. The canonical INDEXED state transition is:
//
//	IndexingHandler.Handle (outbox consumer)
//	  → clipindexer.IndexClip
//	    → tryFastPath / finalizeIndex
//	      → setIndexedAt (single atomic UPDATE with CAS fence)
//
// Per godlike/06 SSOT (one canonical owner per fact), the
// media_assets.index_state='INDEXED' value is the terminal success
// state. The ONLY legitimate writer is setIndexedAt (called from
// IndexClip, which is invoked exclusively by the IndexingHandler).
//
// Per the user directive (Italian, July 2026): "Fare in modo che
// lo stato asset.index.state=INDEXED passi solo dal consumer outbox
// dedicato. Rimuovere le scritture concorrenti da tutti i workflow."
//
// Exempt zones (per the same family precedent as
// percheck_qdrant_index_import_ban):
//  1. internal/infrastructure/indexing/clipindexer/** — the
//     canonical writer package. setIndexedAt lives here.
//  2. **/*_test.go — regression-guard surface legitimately needs
//     fixture setups (e.g. simulating the worker's INDEXED write
//     for end-to-end tests). Mirrors the percheck_qdrant_index_import_ban
//     _test.go exemption.
//
// Comment-marker allowlist: a file that declares
// `// INDEXED_WRITER_SCOPE: clipindexer` in its header godoc is
// also exempt. This handles the edge case where a file outside the
// canonical package needs to write INDEXED (e.g. a future
// admin-tool reconcile path that re-stamps a stale INDEXED row).
// Today NO file uses this marker; the allowlist is documented for
// future use.
//
// Comment-only references to the SQL pattern are residue-accounted
// (godlike/07) — descriptive prose is non-fatal but emits a WARN
// so operator dashboards can spot drift.
//
// Matched rule_id: `percheck_indexed_state_writer_ssot`.
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

// indexedStateWriterSSOTSkipDirs mirrors the standard sibling scanning
// policy from percheck_qdrant_index_import_ban.go.
var indexedStateWriterSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// indexedStateWriterSSOTSkipPathPrefixes is the scanner-package-exemption
// set: cmd/archcheck/scan references this literal pattern for
// greppability, mirrors the family precedent.
var indexedStateWriterSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// indexedStateWriterSSOTCanonicalPath is the canonical INDEXED writer
// package. setIndexedAt (the only function that writes the INDEXED
// state to media_assets) lives here. Files under this prefix are
// exempt.
const indexedStateWriterSSOTCanonicalPath = "internal/infrastructure/indexing/clipindexer/"

// indexedStateWriterSSOTScanScope is the prefix the gate applies to.
// Production .go files under this prefix are scanned. _test.go files
// are exempt (regression-guard surface).
const indexedStateWriterSSOTScanScope = "internal/"

// indexedStateWriterSSOTRe matches a literal INDEXED assignment in an
// SQL SET clause. Qualified read predicates such as
// `alias.index_state = 'INDEXED'` are projections/filters, not state
// transitions and must not be treated as writers.
var indexedStateWriterSSOTRe = regexp.MustCompile(`(?i)\bSET\s+(?:[a-z_][a-z0-9_]*\.)?index_state\s*=\s*['"]?INDEXED['"]?`)
var indexedStateWriterSSOTSetRe = regexp.MustCompile(`(?i)\bSET\b`)
var indexedStateWriterSSOTAssignmentRe = regexp.MustCompile(`(?i)(?:[a-z_][a-z0-9_]*\.)?index_state\s*=\s*['"]?INDEXED['"]?`)

// indexedStateWriterSSOTReferenceRe is used only for residue accounting in
// comments. It intentionally remains broader than the write matcher so
// descriptive references are still visible without becoming violations.
var indexedStateWriterSSOTReferenceRe = regexp.MustCompile(`(?i)index_state\s*=\s*['"]?INDEXED['"]?`)

// indexedStateWriterSSOTRule is the rule-family id the scanner emits.
const indexedStateWriterSSOTRule = "percheck_indexed_state_writer_ssot"

// indexedStateWriterSSOTScopeMarker is the comment marker that exempts
// a file from the gate. Use: `// INDEXED_WRITER_SCOPE: clipindexer`
// in the file header godoc block.
const indexedStateWriterSSOTScopeMarker = "INDEXED_WRITER_SCOPE: clipindexer"

// indexedStateWriterSSOTNote is the violation Note for any
// non-canonical SQL write to index_state='INDEXED'.
const indexedStateWriterSSOTNote = "forbidden SQL write to media_assets.index_state='INDEXED' from a non-canonical file; the canonical INDEXED state transition is via the outbox consumer pipeline: IndexingHandler.Handle (internal/application/jobs/outbox/indexing_handle.go) -> clipindexer.IndexClip (internal/infrastructure/indexing/clipindexer/indexing.go) -> setIndexedAt (internal/infrastructure/indexing/clipindexer/indexing_state.go, single atomic UPDATE with CAS fence on source_version + index_state='INDEXING'). Workflows MUST NOT bypass the outbox consumer; the only way to transition to INDEXED is via the canonical outbox consumer. _test.go files are exempt (regression-guard surface). The comment-marker `// INDEXED_WRITER_SCOPE: clipindexer` in a file header is the documented allowlist for edge cases (none today). Per godlike/06 SSOT (one canonical owner per fact), the only legitimate writer to index_state='INDEXED' is setIndexedAt. Per the user directive (Italian, July 2026): 'Fare in modo che lo stato asset.index.state=INDEXED passi solo dal consumer outbox dedicato.'"

// indexedStateWriterSSOTWarnBucket is the centralized residue-emitter.
// Mirrors qdrantImportBanWarnBucket + assetStateWarn.
func indexedStateWriterSSOTWarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, indexedStateWriterSSOTRule+" "+label+" "+msg)
}

// ScanIndexedStateWriterSSOT walks every .go file under <root>/internal/**
// and emits a violation for any production file (NOT _test.go) outside
// the canonical clipindexer package that contains an SQL write to
// index_state='INDEXED'. The canonical clipindexer package + test
// files are exempt. Comment-only references to the SQL pattern are
// residue-accounted as WARN (godlike/07).
func ScanIndexedStateWriterSSOT(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if indexedStateWriterSSOTSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, indexedStateWriterSSOTSkipPathPrefixes) {
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
		// Test files are exempt — regression-guard surface
		// legitimately needs fixture setups.
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		// Out-of-scope: only scan internal/** production files.
		if !strings.HasPrefix(relSlash, indexedStateWriterSSOTScanScope) {
			return nil
		}
		// Canonical writer path: exempt.
		if strings.HasPrefix(relSlash, indexedStateWriterSSOTCanonicalPath) {
			return nil
		}
		scanIndexedStateWriterSSOTFile(path, relSlash, r)
		return nil
	})
}

// scanIndexedStateWriterSSOTFile opens a single .go file and emits
// percheck_indexed_state_writer_ssot violations for any line whose
// content matches an INDEXED assignment in a SQL SET clause.
// Comment-only references are residue-accounted as WARN (godlike/07
// discipline). Files with the INDEXED_WRITER_SCOPE comment marker in
// their header are exempt (documented allowlist for edge cases).
func scanIndexedStateWriterSSOTFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	// Read the full file content for the comment-marker check.
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	hasScopeMarker := strings.Contains(string(content), indexedStateWriterSSOTScopeMarker)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	insideSetClause := false
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		// Residue accounting (godlike/07): comment-only
		// references to the SQL pattern are descriptive
		// prose, not real writes. WARN, do NOT violate.
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			indexedStateWriterSSOTReferenceRe.MatchString(line) {
			commentOnly++
			continue
		}
		isIndexedAssignment := indexedStateWriterSSOTRe.MatchString(line) ||
			(insideSetClause && indexedStateWriterSSOTAssignmentRe.MatchString(line))
		if isIndexedAssignment {
			// Comment-marker allowlist: the file declares itself as
			// an INDEXED writer (e.g. admin reconcile path).
			if !hasScopeMarker {
				r.Violations = append(r.Violations, report.Violation{
					Package:     pkgFromIndexedStateWriterSSOTRel(relPath),
					File:        relPath,
					Line:        lineNo,
					Rule:        indexedStateWriterSSOTRule,
					Severity:    string(report.SeverityError),
					MatchedRule: "indexed_state_writer_ssot",
					Note:        indexedStateWriterSSOTNote + " | snippet: " + truncateIndexedStateWriterSSOT(line),
				})
			}
		}
		if indexedStateWriterSSOTSetRe.MatchString(line) {
			insideSetClause = true
		}
		if insideSetClause && strings.Contains(line, ";") {
			insideSetClause = false
		}
	}
	if commentOnly > 0 {
		indexedStateWriterSSOTWarnBucket(r, "indexed-state-writer-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// pkgFromIndexedStateWriterSSOTRel extracts the package identifier
// from a repo-relative file path. Mirrors pkgFromQdrantImportBanRel.
func pkgFromIndexedStateWriterSSOTRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// truncateIndexedStateWriterSSOT bounds the snippet surface at 120 chars
// to keep report JSON size stable. Mirrors truncateQdrantImportBan.
func truncateIndexedStateWriterSSOT(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

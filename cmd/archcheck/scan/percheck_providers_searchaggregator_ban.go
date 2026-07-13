// Package scan — per-check forward-prevention gate that bans the
// legacy `providers.SearchAggregator` literal from production Go code
// (PR-PROVIDERS-SEARCHAGGREGATOR-REMOVE, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): the canonical
// search aggregator lives at
// `internal/application/search.Aggregator`. The legacy god-service
// aggregator previously bundled inside `providers.SearchAggregator`
// is git-rm'd; the composition-only bridge
// `internal/app/search_backends.go::providersBridgeToSearch` was
// the last ghost of that surface and is RETIRED in this commit.
//
// This gate is the forward-prevention fence that pins the SSOT:
// any re-introduction of the legacy `providers.SearchAggregator`
// literal (as a type reference, a function call, a struct
// declaration, or even a doc-comment that names the legacy surface
// for migration purposes) emits a violation. Comment-only mentions
// are WARNed (residue accounting, godlike/07) so descriptive prose
// surviving a rename does NOT trip CI on resubmission — but a
// stray production-code reference does.
//
// scanner policy (mirrors percheck_mediatransformer_no_infra_fields.go):
//   - skip file basenames `.git`, `vendor`, `node_modules`,
//     `node-scraper`, `examples`, `archivist`, `docs`, `data`.
//   - skip `_test.go` files (regression-guard surface).
//   - skip `cmd/archcheck/scan/**` (this scanner file + sibling
//     scanners reference the canonical literal for documentation).
//   - comment-only references to `providers.SearchAggregator` are
//     WARNed (residue accounting, godlike/07).
//
// matched rule_id: `percheck_providers_searchaggregator_ban`.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// providersSearchAggregatorBannedLiteral is the canonical literal
// whose re-introduction this gate forbids. The literal is matched
// as a whole-word substring (case-sensitive) so unrelated names
// like `providers.SearchAggregatorConfig` or
// `*NewProvidersSearchAggregator` would NOT match (they don't
// exist intentionally — the canonical SSOT is
// `application/search.Aggregator` and every neighbouring name is
// different).
const providersSearchAggregatorBannedLiteral = "providers.SearchAggregator"

// providersSearchAggregatorLineRe matches a Go line that contains
// the banned literal as a whole-word token. The regex uses `\b`
// word-boundaries so `providers.SearchAggregator` matches but
// `Providers.SearchAggregator` (capital P) or
// `providers.SearchAggregatorConfig` (suffix) do NOT.
var providersSearchAggregatorLineRe = regexp.MustCompile(`\bproviders\.SearchAggregator\b`)

// providersSearchAggregatorRule is the rule-family id the scanner
// emits. Mirrors percheck_mediatransformer_no_infra_fields.go
// MatchedRule naming (snake_case + descriptive suffix).
const providersSearchAggregatorRule = "percheck_providers_searchaggregator_ban"

// providersSearchAggregatorNote is the violation Note string for
// re-introduced references to the legacy god-service aggregator.
// The message points operators at the canonical SSOT and the
// retired bridge + archcheck gate so the migration path is
// auditable inline.
const providersSearchAggregatorNote = "forbidden reference to providers.SearchAggregator (PR-PROVIDERS-SEARCHAGGREGATOR-REMOVE, July 2026); the canonical search aggregator is application/search.Aggregator (internal/application/search/aggregator.go). The legacy providers.SearchAggregator god-service is git-rm'd and the providersBridgeToSearch composition-only bridge is RETIRED. Route every search consumer through the canonical search.Aggregator + registered SearchBackends (the 6 canonical backends: semantic, local, youtube-live, artlist-live, stock, images are registered via BuildSearchBackends)"

// providersSearchAggregatorSkipDirs mirrors the standard
// skip-dir set used by sibling archcheck forward-prevention gates.
var providersSearchAggregatorSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// providersSearchAggregatorSkipPathPrefixes is the scan's own
// package exemption — this file declares the literal in the
// documentation (false-positive exemption for the gate's own
// surface).
var providersSearchAggregatorSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// providersSearchAggregatorWarn emits a WARN-level entry for
// residue-accounting (mirrors mediaTransformerWarn semantics).
func providersSearchAggregatorWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, providersSearchAggregatorRule+" "+label+" "+msg)
}

// ScanProvidersSearchAggregatorBan walks every production-code
// Go file under the project root and inspects each non-test,
// non-self-package file for any line that mentions the legacy
// `providers.SearchAggregator` literal. Each match emits a
// violation; comment-only matches (lines starting with `//`,
// `/*`, or `*`) are residue-accounted via a single WARN rather
// than a violation so descriptive prose in surviving doc-strings
// does NOT trip CI on resubmission.
//
// productionOnly mode (PR-P12-PERCHECK-BASELINE-ZERO, July 2026,
// deadline 2026-08-15): when true, the comment-only WARN is
// suppressed so the operator-facing "zero production-code hits"
// claim is auditable via `len(r.Violations) == 0`. Comment-only
// references still flip the per-file `commentOnly` counter to
// non-zero (so the conditional remains exercised), but the
// appended WARN itself is gated on `!productionOnly`. Mirrors
// the convention used by `scanVoiceoverAliasBanOne` and the
// Wave 5 forward-prevention gate cluster (`percheck_root_override_ban`,
// `percheck_voiceover_alias_ban`, etc.).
//
// The scan walks the entire project (no fixed scan root) because
// the banned literal could legitimately live anywhere — a stray
// reference in `internal/app/`, `internal/api/`, `cmd/admin/`, or
// `tests/` is equally invalid. The standard skip-dir + `_test.go`
// allowlist is honoured so the scan surface matches the rest of
// the archcheck forward-prevention gates.
func ScanProvidersSearchAggregatorBan(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	_ = pol
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if providersSearchAggregatorSkipDirs[base] {
				return filepath.SkipDir
			}
			return nil
		}
		// Only inspect .go files.
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files (regression-guard surface).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		inspectProvidersSearchAggregatorFile(root, path, r, productionOnly)
		return nil
	})
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "<root>",
			File:        root,
			Line:        0,
			Rule:        providersSearchAggregatorRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "scan_root_unreadable",
			Note:        providersSearchAggregatorNote + " | cannot walk root: " + err.Error(),
		})
	}
}

// inspectProvidersSearchAggregatorFile opens a single Go file
// and scans each line for the banned literal. Comment-only lines
// flip the per-file `commentOnly` counter to non-zero; the
// per-file WARN is appended only when `!productionOnly` (so the
// audit-friendly "zero production-code hits" claim,
// PR-P12-PERCHECK-BASELINE-ZERO, is honoured).
func inspectProvidersSearchAggregatorFile(root, absPath string, r *report.Report, productionOnly bool) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	// Skip the scan's own package (false-positive exemption for
	// the regex literal declaring the banned literal).
	for _, prefix := range providersSearchAggregatorSkipPathPrefixes {
		if strings.HasPrefix(relPath, prefix) {
			return
		}
	}

	f, err := os.Open(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "<root>",
			File:        relPath,
			Line:        0,
			Rule:        providersSearchAggregatorRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        providersSearchAggregatorNote + " | cannot open file: " + err.Error(),
		})
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
		trimmed := strings.TrimSpace(line)

		// Comment-only line: residue-accounted via a single
		// WARN per file rather than a per-line violation.
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			if providersSearchAggregatorLineRe.MatchString(line) {
				commentOnly++
			}
			continue
		}

		// Match the banned literal.
		if !providersSearchAggregatorLineRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     "<root>",
			File:        relPath,
			Line:        lineNo,
			Rule:        providersSearchAggregatorRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "forbidden_legacy_search_aggregator_reference",
			Note: providersSearchAggregatorNote +
				" | file: " + relPath +
				" | line: " + trimmed,
		})
	}
	if commentOnly > 0 && !productionOnly {
		providersSearchAggregatorWarn(r, "banned-literal:",
			"comment-only reference(s) to providers.SearchAggregator in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability; replace or remove before next sweep)")
	}
}

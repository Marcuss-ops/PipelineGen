// Package scan — Check 81 (PR-DIAGNOSI-FINALE rule 6, July 2026):
// SearchAggregator is constructed exactly once at the composition
// root (godlike/06 SSOT, singleton).
//
// scan/percheck_search_aggregator_singleton.go pins the
// godlike/06 SSOT that the canonical SearchAggregator singleton
// is constructed at EXACTLY ONE place: the composition root
// `internal/app/registry_search.go`. The canonical constructor
// is `search.NewAggregator(backends, log)` (declared at
// `internal/application/search/aggregator.go::NewAggregator`).
//
// Why singleton: the SearchAggregator owns the per-backend
// fan-out + per-scope routing policy (semantic, local, youtube-
// live, artlist-live, stock, images — the 6 canonical backends
// registered via `BuildSearchBackends`). A second construction
// site that constructs its own NewAggregator with a different
// BackendRegistry would diverge the search-routing decision
// silently — query A returns hits from backends X+Y while query
// B (sent to the second singleton) returns hits from backends
// X+Z. This is a godlike/07 NO-FAKE-AVAILABILITY regression:
// from the user's perspective the search feature availability
// divergence is invisible (no error surface), and any future
// "all backends healthy" diagnostic would surface a phantom
// second singleton.
//
// This gate is the forward-prevention fence. Production-code
// emission of `search.NewAggregator(` outside the canonical
// composition-root site surfaces as a CI build failure. The
// per-call is counted globally and the count MUST equal exactly
// 1; 0 callers trips a "singleton-missing" violation class;
// > 1 callers trips a "singleton-duplicated" violation class.
//
// workerdoctor.NewAggregator — a different-domain
// (`internal/application/workerdoctor/`) function — is NOT
// matched by the gate's regex (the regex anchors on the
// `search.` package qualifier). The gate is narrowly scoped to
// the canonical SearchAggregator surface; the workerdoctor
// Aggregator is a separate concern (health-probe aggregation,
// not search aggregation) and is intentionally distinct.
//
// scanner policy:
//   - skip file basenames `.git`, `vendor`, `node_modules`,
//     `node-scraper`, `examples`, `archivist`, `docs`, `data`.
//   - skip `_test.go` files (regression-guard surface
//     legitimately constructs SearchAggregator stubs for
//     aggregation tests; per the search/aggregator_test.go
//     precedent).
//   - skip `cmd/archcheck/scan/**` (this scanner file
//     references the canonical pattern for documentation).
//   - the gate's matched surface is the EXACT regex pattern
//     `search\.NewAggregator\(` — package-qualified and
//     call-shape (parens immediately after). Test files
//     construct it via `NewAggregator(...)` (without the
//     search. prefix) to use the package-local alias; those
//     are exempt via the `_test.go` skip.
//   - comment-only references are residue-accounted as WARN.
//   - signature conforms to CheckSpec.Run: (root, pol, r) → void
//     (mutates r.Violations directly, mirroring the family
//     idiom from percheck_asset_state_canonical_14.go).
//
// matched rule_id: `percheck_search_aggregator_singleton`.
package boundaries

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

// searchAggregatorSingletonSkipDirs is the standard skip-dir set.
var searchAggregatorSingletonSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// searchAggregatorSingletonSkipPathPrefixes is the scan's own
// package exemption.
var searchAggregatorSingletonSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// searchAggregatorSingletonScanScope is the primary scan
// prefix. Files under this prefix or under the cmd/ fallback
// prefix AND not under any exempt prefix are the gate's
// target surface.
const searchAggregatorSingletonScanScope = "internal/"

// searchAggregatorSingletonCallRe is the canonical regex for
// the production construction site. The pattern anchors on
// `search.NewAggregator(` as a whole word — the receiver MUST
// be the `search` package alias and the call shape MUST be a
// function-call (parens immediately after). The
// `search.NewAggregatorBuilder(` or
// `search.NewAggregatorResult(` would NOT trip the regex
// (different suffixes — though they don't exist today).
var searchAggregatorSingletonCallRe = regexp.MustCompile(`\bsearch\.NewAggregator\s*\(`)

// searchAggregatorSingletonRule is the rule-family id the
// scanner emits.
const searchAggregatorSingletonRule = "percheck_search_aggregator_singleton"

// searchAggregatorSingletonNote is the violation Note string
// for any non-canonical production construction site for the
// canonical SearchAggregator singleton. The message references
// the godlike/06 SSOT singleton invariant + the canonical
// composition-root site so the operator sees the migration path
// inline.
const searchAggregatorSingletonNote = "forbidden non-canonical construction site for canonical search.Aggregator singleton (PR-DIAGNOSI-FINALE rule 6, July 2026); godlike/06 SSOT requires the canonical SearchAggregator to be constructed EXACTLY ONCE at the composition root (internal/app/registry_search.go or internal/app/search_backends.go). A second construction site would diverge the search-routing decision silently (query A returns hits from backends X+Y while query B sent to the second singleton returns hits from backends X+Z — silent godlike/07 NO-FAKE-AVAILABILITY regression with no error surface). The workerdoctor.Aggregator is a separate-domain helper (health-probe aggregation, NOT search aggregation) and is exempt by construction (the regex requires the search. package qualifier)."

// searchAggregatorSingletonWarn is the residue-emitter for
// comment-only references.
func searchAggregatorSingletonWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, searchAggregatorSingletonRule+" "+label+" "+msg)
}

// ScanSearchAggregatorSingleton walks every .go file under
// <root>/internal/** + cmd/** and counts production-code
// construction sites of `search.NewAggregator(`. The count
// MUST equal exactly 1 (the canonical composition root);
// 0 callers => a `search_aggregator_singleton_missing`
// violation (godlike/07 NO-FAKE-AVAILABILITY);
//
//	> 1 callers => a `search_aggregator_singleton_duplicated`
//	violation (godlike/06 SSOT singleton-divergence).
//
// The function mutates r.Violations directly (matching the
// CheckSpec.Run signature convention). The first construction
// site (counted even if total > 1) is recorded inline in the
// violation Note so the operator can grep for the canonical
// composition site.
func ScanSearchAggregatorSingleton(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	totalCallSites := 0
	firstConstructionSite := ""
	firstConstructionLine := 0

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if searchAggregatorSingletonSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, searchAggregatorSingletonSkipPathPrefixes) {
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
		// legitimately constructs SearchAggregator stubs.
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		// Out-of-scope (NOT under internal/ or cmd/):
		// skip without scanning.
		if !strings.HasPrefix(relSlash, searchAggregatorSingletonScanScope) &&
			!strings.HasPrefix(relSlash, "cmd/") {
			return nil
		}
		totalCallSites, firstConstructionSite, firstConstructionLine =
			countSearchAggregatorSingletonFile(path, relSlash, totalCallSites, firstConstructionSite, firstConstructionLine, r)
		return nil
	})

	// Singleton assertion: count MUST equal exactly 1.
	switch {
	case totalCallSites == 0:
		r.Violations = append(r.Violations, report.Violation{
			Package:     "<root>",
			File:        "(canonical composition-root site)",
			Line:        0,
			Rule:        searchAggregatorSingletonRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "search_aggregator_singleton_missing",
			Note: searchAggregatorSingletonNote +
				" | construction site MISSING — internal/app/registry_search.go (or internal/app/search_backends.go) MUST construct the canonical search.NewAggregator( exactly once at composition time. A missing construction site is a godlike/07 NO-FAKE-AVAILABILITY regression: search feature availability becomes invisible-zero.",
		})
	case totalCallSites > 1:
		r.Violations = append(r.Violations, report.Violation{
			Package:     "<root>",
			File:        firstConstructionSite,
			Line:        firstConstructionLine,
			Rule:        searchAggregatorSingletonRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "search_aggregator_singleton_duplicated",
			Note: searchAggregatorSingletonNote +
				" | counted " + strconv.Itoa(totalCallSites) +
				" production-code construction sites — the canonical SSOT requires EXACTLY 1. First construction site: " +
				firstConstructionSite + ":" + strconv.Itoa(firstConstructionLine),
		})
	}
}

// countSearchAggregatorSingletonFile opens a single .go file
// and counts production-code call sites of `search.NewAggregator(`.
// Comment-only references are residue-accounted as WARN
// (godlike/07 discipline). Returns the updated counters via
// the multiple-return-value idiom so callers can thread state
// across the WalkDir closure without a struct wrapper.
func countSearchAggregatorSingletonFile(
	path, relPath string,
	totalCallSites int,
	firstConstructionSite string,
	firstConstructionLine int,
	r *report.Report,
) (int, string, int) {
	f, err := os.Open(path)
	if err != nil {
		return totalCallSites, firstConstructionSite, firstConstructionLine
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
			searchAggregatorSingletonCallRe.MatchString(line) {
			commentOnly++
			continue
		}

		if !searchAggregatorSingletonCallRe.MatchString(line) {
			continue
		}
		totalCallSites++
		if firstConstructionSite == "" {
			firstConstructionSite = relPath
			firstConstructionLine = lineNo
		}
	}
	if commentOnly > 0 {
		searchAggregatorSingletonWarn(r, "search-aggregator-singleton-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) to search.NewAggregator( in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
	return totalCallSites, firstConstructionSite, firstConstructionLine
}

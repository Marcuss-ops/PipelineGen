// Package scan — percheck_searchmode_forced_ban.go
//
// Forward-prevention gate that BANS forcing the search mode to
// SearchModeANN inside adapters, infrastructure, and transport code.
//
// The canonical mode is owned by the domain policy
// (internal/domain/media/search_policy.go) and resolved by the
// application-layer ResolutionPolicyResolver
// (internal/application/mediamemory/policy_resolver.go). Adapters
// MUST forward the resolved policy.Mode verbatim and MUST NOT fall
// back to ANN locally.
//
// Exempt zones:
//   - internal/application/search — the SearchMode enum SSOT.
//   - internal/domain/media — the ResolutionSearchPolicy SSOT.
//   - internal/api/mediasearch — the thin transport maps the
//     incoming wire value to search.SearchMode; it does not force ANN.
//   - **/*_test.go — test fixtures.
//   - cmd/archcheck/scan — the scanner itself.
//
// Matched rule_id: percheck_searchmode_forced_ban
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

// searchModeForcedRe matches assignment of a mode field/variable to
// SearchModeANN. It catches both struct-literal `Mode:` and assignment
// `mode = search.SearchModeANN`, but not equality comparisons.
var searchModeForcedRe = regexp.MustCompile(`(?:\b[Mm]ode\s*:\s*(?:search\.)?SearchModeANN\b|\b[Mm]ode\s*=\s*(?:search\.)?SearchModeANN\b)`)

const searchModeForcedRule = "percheck_searchmode_forced_ban"

const searchModeForcedNote = "forbidden hardcoded SearchModeANN assignment. The search mode must be derived from ResolutionSearchPolicy.Mode; adapters/infrastructure must not force ANN. See internal/domain/media/search_policy.go and internal/application/mediamemory/policy_resolver.go."

var searchModeForcedSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

var searchModeForcedSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

var searchModeForcedExemptPathPrefixes = []string{
	"internal/application/search",
	"internal/domain/media",
	"internal/api/mediasearch",
}

const searchModeForcedScanScope = "internal/"

// ScanSearchModeForcedBan walks every .go file under <root>/internal/**
// and emits a violation for any production file that hardcodes a
// Mode assignment to SearchModeANN outside the exempt zones.
func ScanSearchModeForcedBan(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if searchModeForcedSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, searchModeForcedSkipPathPrefixes) {
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
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		if !strings.HasPrefix(relSlash, searchModeForcedScanScope) {
			return nil
		}
		if hasAnyPathPrefix(relSlash, searchModeForcedExemptPathPrefixes) {
			return nil
		}
		scanSearchModeForcedBanFile(path, relSlash, r)
		return nil
	})
}

func scanSearchModeForcedBanFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0

	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if !searchModeForcedRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromSearchModeForcedRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        searchModeForcedRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "search_mode_forced_ban",
			Note:        searchModeForcedNote + " | snippet: " + truncateSearchModeForced(line),
		})
	}
}

func pkgFromSearchModeForcedRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

func truncateSearchModeForced(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

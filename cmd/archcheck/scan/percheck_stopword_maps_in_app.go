// Package scan — percheck_stopword_maps_in_app.go
//
// Forward-prevention gate that BANS hardcoded stop-word maps
// (map[string]struct{}{"the":..., "and":..., "for":...}) in
// production Go files under internal/application/ and
// internal/infrastructure/.
//
// Stop-word sets MUST be loaded from the LexiconRegistry
// (internal/domain/linguistics/) at bootstrap. Any production
// file that defines a literal stop-word map is a godlike/06
// SSOT violation — stop words are linguistic data, not code.
//
// Exempt zones:
//   - internal/domain/linguistics/ — the canonical lexicons
//   - **/*_test.go — regression-guard surface
//   - cmd/archcheck/scan/ — scanner's own source code
//
// Matched rule_id: percheck_stopword_maps_in_app
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

// stopwordMapRe matches Go map literal lines that look like stop-word
// definitions: `map[string]struct{}{` or `map[string]bool{` followed
// by quoted words like "the", "a", "an", "and", "or", "for".
// This pattern is characteristic of hardcoded linguistic data.
var stopwordMapRe = regexp.MustCompile(`map\[string\](struct\{\}|bool)\{`)

// stopwordWordRe matches common stop-word-like quoted strings on the
// same line as a map literal.
var stopwordWordRe = regexp.MustCompile(`"the"|"and"|"for"|"with"|"from"|"that"|"this"`)

// stopwordCanonicalPaths are paths where stop-word maps are legitimately
// defined (the LexiconRegistry).
var stopwordCanonicalPaths = map[string]bool{
	"internal/domain/linguistics/lexicon_registry.go": true,
}

const stopwordMapRule = "percheck_stopword_maps_in_app"

const stopwordMapNote = "forbidden hardcoded stop-word map in application/infrastructure code. Stop-word sets MUST be loaded from the LexiconRegistry (internal/domain/linguistics/) at bootstrap via linguistics.DefaultLexicon().StopWords(). Hardcoded linguistic maps are a godlike/06 SSOT violation. See internal/domain/linguistics/lexicon_registry.go for the canonical approach."

var stopwordMapSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

var stopwordMapSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// ScanStopwordMapsInApp walks production .go files under
// internal/application/ and internal/infrastructure/ and emits
// violations for hardcoded stop-word map literals.
func ScanStopwordMapsInApp(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if stopwordMapSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, stopwordMapSkipPathPrefixes) {
					return filepath.SkipDir
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
		// Only scan internal/application/ and internal/infrastructure/.
		if !strings.HasPrefix(relSlash, "internal/application/") &&
			!strings.HasPrefix(relSlash, "internal/infrastructure/") {
			return nil
		}
		// Canonical lexicon path is exempt.
		if stopwordCanonicalPaths[relSlash] {
			return nil
		}
		scanStopwordMapFile(path, relSlash, r)
		return nil
	})
}

func scanStopwordMapFile(path, relPath string, r *report.Report) {
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
		// Must match the map literal pattern AND contain stop-word-like strings.
		if !stopwordMapRe.MatchString(line) {
			continue
		}
		if !stopwordWordRe.MatchString(line) {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromStopwordMapRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        stopwordMapRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "stopword_maps_ssot_gate",
			Note:        stopwordMapNote + " | snippet: " + truncateStopwordMap(line),
		})
	}
}

func pkgFromStopwordMapRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

func truncateStopwordMap(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

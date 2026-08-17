// Package scan — percheck_project_derivation_ssot.go
//
// Forward-prevention gate: production Go code MUST NOT derive the voiceover
// artifact Project namespace from a hardcoded "scene" fallback. The canonical
// routing decision is internal/kernel/script.ArtifactRoutingContext; Project
// is resolved ONCE at generation start and propagated verbatim. An empty
// Project with a requested publish fails closed before TTS
// (ErrProjectRequired / ErrVoiceoverPublishProjectRequired) — it is never
// silently replaced with a fabricated namespace.
//
// The gate bans the literal `project = "scene"` / `Project: "scene"` /
// `project == "scene"` shapes that implement the retired fallback.
//
// Exempt zones:
//   - **/*_test.go — regression-guard surface may assert the legacy shape.
//   - cmd/archcheck/scan — the scanner itself.
//
// Matched rule_id: percheck_project_derivation_ssot
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

const projectDerivationSSOTRule = "percheck_project_derivation_ssot"

const projectDerivationSSOTNote = "forbidden hardcoded scene project-namespace derivation. Project must originate from the canonical routing context (internal/kernel/script.ArtifactRoutingContext) and propagate verbatim; an empty project with a requested publish fails closed (ErrProjectRequired) instead of silently falling back to a fabricated namespace."

// projectDerivationSSOTRe matches the retired `project = "scene"` fallback
// shapes (assignment, struct-literal field, and comparison), case-insensitive.
var projectDerivationSSOTRe = regexp.MustCompile(`(?i)project\s*(?:=|:=|==)\s*"scene"`)

var projectDerivationSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

var projectDerivationSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// ScanProjectDerivationSSOT walks every .go file under the repo root and
// emits a violation for any production file that re-introduces the retired
// "scene" project fallback.
func ScanProjectDerivationSSOT(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if projectDerivationSSOTSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				if hasAnyPathPrefix(filepath.ToSlash(rel), projectDerivationSSOTSkipPathPrefixes) {
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
		scanProjectDerivationSSOTFile(path, relSlash, r)
		return nil
	})
}

func scanProjectDerivationSSOTFile(path, relPath string, r *report.Report) {
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
		if !projectDerivationSSOTRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromProjectDerivationSSOTRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        projectDerivationSSOTRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "project_derivation_ssot_gate",
			Note:        projectDerivationSSOTNote + " | snippet: " + truncateProjectDerivationSSOT(line),
		})
	}
}

func truncateProjectDerivationSSOT(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

func pkgFromProjectDerivationSSOTRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// Package scan — percheck_version_strings_ban.go
//
// Forward-prevention gate that BANS hardcoded pipeline version
// strings of the form "<name>-v<N>" outside the canonical version
// registry.
//
// The canonical version registry lives in
// internal/kernel/media/version.go. Every component that needs to
// expose a version MUST reference a constant from that file instead
// of repeating the literal string.
//
// Exempt zones:
//   - internal/kernel/media/version.go — the canonical SSOT owner.
//   - **/*_test.go — test fixtures may use literal versions for
//     regression checks.
//   - cmd/archcheck/scan — the scanner itself.
//
// Matched rule_id: percheck_version_strings_ban
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// versionStringsSSOTRe matches the canonical resolution pipeline
// version literals (e.g. "brain-v1", "multilingual-e5-v1"). It is
// deliberately narrow to avoid flagging unrelated schema / model
// versions such as "2026-06-16-v1".
var versionStringsSSOTRe = regexp.MustCompile(`"(brain|intent-registry|multilingual-e5|diversity-policy|slot-sampler|provider-registry|media-ranker|scene-planner|visual-intent)-v[0-9]+"`)

const versionStringsSSOTRule = "percheck_version_strings_ban"

const versionStringsSSOTNote = "forbidden hardcoded pipeline version string outside the canonical version registry (internal/kernel/media/version.go). Components MUST reference a version constant from the registry so that version changes propagate from a single source of truth."

var versionStringsSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

var versionStringsSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

var versionStringsSSOTExemptPathPrefixes = []string{
	"internal/kernel/media",
}

const versionStringsSSOTScanScope = "internal/"

// ScanVersionStringsBan walks every .go file under <root>/internal/**
// and emits a violation for any production file that contains a
// hardcoded pipeline version string outside the canonical registry.
func ScanVersionStringsBan(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if versionStringsSSOTSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, versionStringsSSOTSkipPathPrefixes) {
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
		if !strings.HasPrefix(relSlash, versionStringsSSOTScanScope) {
			return nil
		}
		if hasAnyPathPrefix(relSlash, versionStringsSSOTExemptPathPrefixes) {
			return nil
		}
		scanVersionStringsBanFile(path, relSlash, r)
		return nil
	})
}

func scanVersionStringsBanFile(path, relPath string, r *report.Report) {
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
		if !versionStringsSSOTRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromVersionStringsBanRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        versionStringsSSOTRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "version_strings_ssot_gate",
			Note:        versionStringsSSOTNote + " | snippet: " + truncateVersionStringsBan(line),
		})
	}
}

func pkgFromVersionStringsBanRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

func truncateVersionStringsBan(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

// Package textban is the shared walker/scanner behind the text-literal
// governance gates. Each gate supplies a Spec (scope, exemptions, match
// predicate, note); the walk, comment handling, snippet formatting and
// violation shape live here once so the gates cannot drift apart.
package textban

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// Spec configures one text-ban gate.
type Spec struct {
	RuleID      string
	MatchedRule string
	Note        string
	// ScanPrefixes is the set of repo-relative prefixes the gate applies to.
	// A file must match at least one prefix.
	ScanPrefixes []string
	// SkipDirs excludes whole directory basenames from the walk.
	SkipDirs map[string]bool
	// SkipPathPrefixes excludes directories/files by repo-relative prefix
	// (for example the scanner package itself).
	SkipPathPrefixes []string
	// ExemptPathPrefixes allows matching production files under a prefix.
	ExemptPathPrefixes []string
	// ExemptPaths allows exact repo-relative files (the canonical SSOT owner).
	ExemptPaths []string
	// Match reports whether a non-comment line is a violation.
	Match func(line string) bool
	// CommentReference, when non-empty, counts comment-only lines containing
	// that literal as residue instead of violations.
	CommentReference string
	// WarnLabel prefixes the residue warning; CommentWarn renders its body.
	WarnLabel   string
	CommentWarn func(relPath string, count int) string
}

// Scan walks root and emits one violation per matching production line.
func Scan(root string, spec Spec, r *report.Report) {
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if spec.SkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil && hasAnyPathPrefix(filepath.ToSlash(rel), spec.SkipPathPrefixes) {
				return filepath.SkipDir
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
		if !hasAnyPathPrefix(relSlash, spec.ScanPrefixes) || fileExempt(relSlash, spec) {
			return nil
		}
		scanFile(path, relSlash, spec, r)
		return nil
	})
	if err != nil {
		r.Warnings = append(r.Warnings, spec.RuleID+" scan "+root+": "+err.Error())
	}
}

func fileExempt(relPath string, spec Spec) bool {
	if hasAnyPathPrefix(relPath, spec.ExemptPathPrefixes) {
		return true
	}
	for _, exempt := range spec.ExemptPaths {
		if relPath == exempt {
			return true
		}
	}
	return false
}

func scanFile(path, relPath string, spec Spec, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			if spec.CommentReference != "" && strings.Contains(line, spec.CommentReference) {
				commentOnly++
			}
			continue
		}
		if !spec.Match(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        spec.RuleID,
			Severity:    string(report.SeverityError),
			MatchedRule: spec.MatchedRule,
			Note:        spec.Note + " | snippet: " + truncate(line),
		})
	}
	if commentOnly > 0 && spec.CommentWarn != nil {
		r.Warnings = append(r.Warnings, spec.RuleID+" "+spec.WarnLabel+" "+spec.CommentWarn(relPath, commentOnly))
	}
}

func hasAnyPathPrefix(relPath string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(relPath, prefix) {
			return true
		}
	}
	return false
}

func pkgFromRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

func truncate(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

// CountCommentResidue is exported for gate-specific warning builders that
// need the same stable phrasing.
func CountCommentResidue(relPath string, count int) string {
	return strconv.Itoa(count) + " comment-only reference(s) in " + relPath +
		" (descriptive prose; non-fatal per godlike/07 no-fake-availability)"
}

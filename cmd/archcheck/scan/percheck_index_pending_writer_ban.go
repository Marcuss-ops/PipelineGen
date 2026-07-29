// Package scan — percheck_index_pending_writer_ban.go
//
// Forward-prevention gate that BANS writing the deprecated
// asset.StateIndexPending value in production code under
// internal/application/ and internal/infrastructure/.
//
// StateIndexPending is retained in the domain only for DB
// backward-compat. New production code MUST use the canonical
// asset lifecycle (asset.StateDiscovered / NewIndexableAssetState)
// and MUST NOT introduce new writers of INDEX_PENDING.
//
// Exempt zones:
//   - internal/kernel/asset/index_state.go — the canonical definition
//     of the deprecated value.
//   - internal/application/assets/operator/index_health_resolver.go —
//     reads legacy state for backward-compat health reporting.
//   - **/*_test.go — regression-guard surface.
//   - cmd/archcheck/scan — the scanner's own source code.
//
// Matched rule_id: percheck_index_pending_writer_ban
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

// indexPendingWriterRe matches any use of the deprecated state literal.
// The gate is intentionally broad: new production code should not
// reference the deprecated value at all; legitimate readers are
// listed in the exemption set below.
var indexPendingWriterRe = regexp.MustCompile(`\basset\.StateIndexPending\b`)

const indexPendingWriterRule = "percheck_index_pending_writer_ban"

const indexPendingWriterNote = "forbidden reference to deprecated asset.StateIndexPending in production code. New writers MUST use the canonical asset lifecycle (asset.StateDiscovered / NewIndexableAssetState). StateIndexPending is retained only for DB backward-compat. See internal/kernel/asset/index_state.go."

var indexPendingWriterSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

var indexPendingWriterSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

var indexPendingWriterExemptPaths = map[string]bool{
	"internal/kernel/asset/index_state.go":                          true,
	"internal/application/assets/operator/index_health_resolver.go": true,
}

const indexPendingWriterScanScope = "internal/"

// ScanIndexPendingWriterBan walks every production .go file under
// <root>/internal/ and emits a violation when a file outside the
// exempt set references asset.StateIndexPending.
func ScanIndexPendingWriterBan(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if indexPendingWriterSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, indexPendingWriterSkipPathPrefixes) {
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
		if !strings.HasPrefix(relSlash, indexPendingWriterScanScope) {
			return nil
		}
		if indexPendingWriterExemptPaths[relSlash] {
			return nil
		}
		scanIndexPendingWriterBanFile(path, relSlash, r)
		return nil
	})
}

func scanIndexPendingWriterBanFile(path, relPath string, r *report.Report) {
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
		if !indexPendingWriterRe.MatchString(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromIndexPendingWriterRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        indexPendingWriterRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "index_pending_writer_ban",
			Note:        indexPendingWriterNote + " | snippet: " + truncateIndexPendingWriter(line),
		})
	}
}

func pkgFromIndexPendingWriterRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

func truncateIndexPendingWriter(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

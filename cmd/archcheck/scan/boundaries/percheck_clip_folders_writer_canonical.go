// Package scan — percheck_clip_folders_writer_canonical is the
// forward-prevention gate that bans direct SQL writes to `clip_folders`
// from ANY package outside the canonical folder writers.
//
// MEDIA-SSOT folder projection (POSTGRES-MEDIA-CUTOVER, September 2026):
// catalog sync reads ListFolders from the PostgreSQL clip_folders projection
// (internal/platform/postgres/media.FolderRepository). The operational SQLite
// row stays the write-through owner and mirrors every mutation into that
// projection through the imagesregistry.FolderProjection port.
//
// The split is only sound while EVERY folder write passes through the
// canonical writer. A raw `INSERT/UPDATE/DELETE clip_folders` executed
// anywhere else silently desynchronises the projection: the folder then never
// appears in the PostgreSQL list, so pruneMissingFolders can never reconcile
// it, and the row survives forever in one store only. Four such writers used
// to exist in cmd/admin (sync-outros, reset-video-ai, reset-stock-subfolders,
// list-drive-folder); this gate exists so they cannot come back.
//
// CANONICAL OWNERSHIP IS BY PACKAGE, NOT BY FILENAME, resolved through
// policy.IsCanonicalClipFolderWriter — the same construction as the
// media_assets fence, for the same anti-drift reason.
//
// matched rule_id: `percheck_clip_folders_writer_canonical`.
package boundaries

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// clipFoldersWriterScanRoots are the directory roots the gate walks. cmd/ is
// included because the admin CLI is the historical exception surface that
// carried three of the four retired writers.
var clipFoldersWriterScanRoots = []string{
	"internal",
	"cmd",
}

// clipFoldersWriterForbiddenRe matches a Go line that contains a direct SQL
// write to clip_folders (INSERT, UPDATE, DELETE, REPLACE).
//
// The INSERT/REPLACE/UPDATE branches require the `SET`/`(` trailer so a
// column-list or SET clause is present; the DELETE branch does NOT, mirroring
// the media_assets fence — requiring a trailer on DELETE silently exempts the
// row deletions this gate most needs to catch.
//
// The two top-level alternatives place the verb in DIFFERENT capture groups,
// so callers must scan for the first participating group instead of assuming
// group 1 (Go reports unmatched groups as -1).
var clipFoldersWriterForbiddenRe = regexp.MustCompile(
	`(?is)\b(INSERT\s+(?:OR\s+\w+\s+)?INTO|REPLACE\s+INTO|UPDATE)\s+clip_folders\b\s*(?:SET|\()|\b(DELETE\s+FROM)\s+clip_folders\b`,
)

// clipFoldersWriterRule is the rule-family id the scanner emits.
const clipFoldersWriterRule = "percheck_clip_folders_writer_canonical"

// clipFoldersWriterNote is the violation Note string.
const clipFoldersWriterNote = "forbidden direct SQL write to clip_folders outside the canonical folder writers (MEDIA-SSOT folder projection, September 2026): catalog sync reads the PostgreSQL clip_folders projection, so every folder mutation MUST go through the canonical writer — internal/platform/postgres/media.FolderRepository (projection) or the operational repository internal/platform/sqlite/assets/imagesregistry (which mirrors into it). A raw write anywhere else desynchronises the two stores and makes folder reconciliation unable to see the row."

// ScanClipFoldersWriterCanonical walks internal/ + cmd/ and inspects every
// non-test Go file for direct SQL writes to clip_folders. Canonical owners are
// exempt (resolved through policy.IsCanonicalClipFolderWriter).
//
// God comments are masked before matching, so a doc block that quotes the
// legacy statement is not reported as a live write.
func ScanClipFoldersWriterCanonical(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range clipFoldersWriterScanRoots {
		absRoot := filepath.Join(root, scanRoot)
		filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if policy.StandardSkipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			inspectClipFoldersWriterFile(root, path, r)
			return nil
		})
	}
}

// inspectClipFoldersWriterFile scans one Go file for forbidden clip_folders
// writes. Canonical owners, the scanner package itself, the SQL migration
// trees and the closed test-only support list are exempt.
func inspectClipFoldersWriterFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)

	if policy.IsCanonicalClipFolderWriter(relPath) {
		return
	}
	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return
	}
	if hasAnyPathPrefix(relPath, policy.SQLMigrationPrefixes) || policy.IsTestOnlySupportFile(relPath) {
		return
	}

	source, err := os.ReadFile(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        0,
			Rule:        clipFoldersWriterRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        clipFoldersWriterNote + " | cannot open file: " + err.Error(),
		})
		return
	}

	masked := source
	fileSet := token.NewFileSet()
	if parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments); parseErr == nil && parsed != nil {
		masked = maskGoComments(source, fileSet, parsed)
	}
	maskedStr := string(masked)

	for _, match := range clipFoldersWriterForbiddenRe.FindAllStringSubmatchIndex(maskedStr, -1) {
		if len(match) < 4 {
			continue
		}
		verb := ""
		for group := 2; group+1 < len(match); group += 2 {
			if match[group] >= 0 {
				verb = maskedStr[match[group]:match[group+1]]
				break
			}
		}
		if verb == "" {
			continue
		}
		lineNo := 1 + strings.Count(maskedStr[:match[0]], "\n")
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        clipFoldersWriterRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "forbidden_sql_" + strings.ToLower(strings.TrimSpace(verb)),
			Note: clipFoldersWriterNote +
				" | file: " + relPath +
				" | matched verb: " + strings.ToLower(strings.TrimSpace(verb)) +
				" | table: clip_folders" +
				" | route through the canonical folder repository (FolderProjection port)",
		})
	}
}

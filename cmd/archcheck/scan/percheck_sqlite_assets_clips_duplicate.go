// Package scan — forward-prevention gate for the retired duplicate
// SQLite assets/clips repository package.
package scan

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	sqliteAssetsClipsDuplicateRule = "percheck_sqlite_assets_clips_duplicate"
	sqliteAssetsClipsImportRule    = "percheck_sqlite_assets_clips_import"
	retiredSQLiteClipsImportPath   = "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/clips"
	retiredSQLiteClipsDir          = "internal/platform/sqlite/assets/clips"
)

// The subpackage remains only as the owner of the unrelated idempotency
// adapter. The ClipsRepository implementation and its cache adapter now live
// in the parent assets package; these are the only files allowed to remain in
// the retired directory.
var allowedSQLiteAssetsClipsFiles = map[string]bool{
	"idempotency_repository.go":      true,
	"idempotency_repository_test.go": true,
}

// ScanSQLiteAssetsClipsDuplicateBan prevents the deleted ClipsRepository
// implementation from being recreated under assets/clips and prevents callers
// from reintroducing a dependency on that retired package. It is deliberately
// path- and AST-based: the directory allowlist catches new business-logic files,
// while Go's parser catches real imports without matching comments or prose.
func ScanSQLiteAssetsClipsDuplicateBan(root string, _ *policy.Policy, r *report.Report) {
	clipsDir := filepath.Join(root, filepath.FromSlash(retiredSQLiteClipsDir))
	_ = filepath.WalkDir(clipsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || allowedSQLiteAssetsClipsFiles[entry.Name()] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		r.Violations = append(r.Violations, report.Violation{
			File:        filepath.ToSlash(rel),
			Rule:        sqliteAssetsClipsDuplicateRule,
			MatchedRule: "retired_sqlite_clips_file",
			Severity:    string(report.SeverityError),
			Note:        "business logic in internal/platform/sqlite/assets/clips is retired; use the canonical parent assets package for ClipsRepository (the idempotency adapter now lives in internal/platform/sqlite/idempotency)",
		})
		return nil
	})

	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"examples": true, "scripts": true, "docs": true,
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil
		}
		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil || (importPath != retiredSQLiteClipsImportPath && !strings.HasPrefix(importPath, retiredSQLiteClipsImportPath+"/")) {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			position := fset.Position(spec.Pos())
			r.Violations = append(r.Violations, report.Violation{
				File:        filepath.ToSlash(rel),
				Line:        position.Line,
				Rule:        sqliteAssetsClipsImportRule,
				MatchedRule: "retired_sqlite_clips_import",
				Severity:    string(report.SeverityError),
				Note:        "the duplicate SQLite assets/clips package is retired; import the canonical parent assets package or an application-owned port instead",
			})
		}
		return nil
	})
}

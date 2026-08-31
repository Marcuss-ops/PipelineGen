// Package main - archcheck database/sql ownership rules.
//
// This file owns the current-tree database/sql gate. SQL access belongs to
// concrete persistence adapters under internal/platform and to composition
// wiring under internal/app. Capability packages are permitted only in the
// explicitly designated persistence seams.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const databaseSQLImport = "database/sql"

// checkDatabaseSQLGate enforces the current ownership rule for database/sql.
// It parses production Go files under internal/, records actual import sites,
// and fails closed for every site outside internal/app and internal/platform.
func checkDatabaseSQLGate() (map[string]int, []string) {
	stats := map[string]int{
		"actual":      0,
		"allowed":     0,
		"regressions": 0,
	}

	actual, err := scanDatabaseSQLImports(".")
	if err != nil {
		stats["regressions"] = -1
		return stats, []string{fmt.Sprintf("checkDatabaseSQLGate: scan failed: %v", err)}
	}

	stats["actual"] = len(actual)
	var violations []string
	for _, path := range actual {
		if databaseSQLImportAllowed(path) {
			stats["allowed"]++
			continue
		}
		violations = append(violations, "database/sql import outside authorized platform/persistence areas: "+path)
	}
	stats["regressions"] = len(violations)
	return stats, violations
}

// scanDatabaseSQLImports returns production Go files under internal/ that
// import database/sql. Parsing imports avoids false positives from comments,
// strings, and unrelated package names. The returned paths are repository-
// relative and sorted for deterministic reports.
func scanDatabaseSQLImports(root string) ([]string, error) {
	internalRoot := filepath.Join(root, "internal")
	var actual []string

	err := filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", filepath.ToSlash(path), err)
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return fmt.Errorf("decode import in %s: %w", filepath.ToSlash(path), err)
			}
			if importPath != databaseSQLImport {
				continue
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return fmt.Errorf("relativize %s from %s: %w", path, root, err)
			}
			actual = append(actual, filepath.ToSlash(rel))
			break
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return normalizeDatabaseSQLPaths(actual), nil
}

func normalizeDatabaseSQLPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func databaseSQLImportAllowed(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if strings.HasPrefix(path, "internal/platform/") || strings.HasPrefix(path, "internal/app/") {
		return true
	}
	for _, prefix := range authorizedPersistenceCapabilityPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// These are the only capability persistence seams currently authorized to
// depend directly on database/sql. Keep this list explicit and small: new
// capability persistence must be introduced through a platform adapter or
// deliberately added here with an architectural review.
var authorizedPersistenceCapabilityPrefixes = []string{
	"internal/capabilities/assets/persistence/",
	"internal/capabilities/assets/artifacts/",
	"internal/capabilities/assets/finalizer/",
	"internal/capabilities/execution/steps/",
	"internal/capabilities/jobs/",
	"internal/capabilities/maintenance/",
	"internal/capabilities/operations/",
	"internal/capabilities/voiceover/service/persistence/",
}

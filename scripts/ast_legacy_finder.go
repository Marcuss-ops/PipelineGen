//go:build ignore

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// astLegacyFinder scans Go files and finds real references to models.MediaAsset
// using AST analysis. Ignores comments, strings, and test files (optional).
//
// Usage: go run scripts/ast_legacy_finder.go [dir] [--include-tests]
//
// Output: one file path per line (sorted) containing a real reference.
//
// The scanner now verifies references against the actual import path
// (github.com/Marcuss-ops/PipelineGen/internal/media/models), not just
// the package identifier name. This prevents false positives when other
// packages happen to declare a MediaAsset type in package "models".

func main() {
	dir := "internal"
	includeTests := false

	args := os.Args[1:]
	for _, a := range args {
		if a == "--include-tests" {
			includeTests = true
		} else if !strings.HasPrefix(a, "-") {
			dir = a
		}
	}

	fset := token.NewFileSet()
	refs := make(map[string]bool)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil // skip unparseable files
		}

		if containsLegacyRef(file) {
			refs[path] = true
		}
		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "error walking directory: %v\n", err)
		os.Exit(1)
	}

	// Output sorted
	var paths []string
	for p := range refs {
		paths = append(paths, p)
	}
	// Simple sort
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if paths[i] > paths[j] {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
	}

	for _, p := range paths {
		fmt.Println(p)
	}
}

// isLegacySelectorExpr returns true if expr is a SelectorExpr where the
// package identifier maps to the legacy models package AND the selected
// name is "MediaAsset".
func isLegacySelectorExpr(expr ast.Expr, legacyImports map[string]bool) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && legacyImports[id.Name] && sel.Sel.Name == "MediaAsset"
}

// containsLegacyRef walks the AST looking for any reference to models.MediaAsset
// (or aliased) in expressions, types, and composite literals — but NOT in
// comments or strings. It verifies references against the actual import path
// to prevent false positives.
func containsLegacyRef(file *ast.File) bool {
	// Build a map of valid package identifiers (aliases or base package
	// names) that resolve to the legacy models package.
	legacyImports := make(map[string]bool)
	// Canonical module path for the legacy package.
	targetPath := "github.com/Marcuss-ops/PipelineGen/internal/media/models"

	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		// Match exact canonical path OR any path ending in /internal/media/models.
		// The suffix match covers both relative and alternate-module imports.
		if path == targetPath || strings.HasSuffix(path, "/internal/media/models") {
			if imp.Name != nil {
				// Explicit alias: import foo "github.com/.../models" → use "foo"
				if imp.Name.Name != "_" && imp.Name.Name != "." {
					legacyImports[imp.Name.Name] = true
				}
			} else {
				// Default identifier is the last element of the import path
				// (conventional Go: .../models → package name is "models")
				parts := strings.Split(path, "/")
				if len(parts) > 0 {
					legacyImports[parts[len(parts)-1]] = true
				}
			}
		}
	}

	// If the file doesn't import the legacy package, it can't contain a
	// reference to models.MediaAsset.
	if len(legacyImports) == 0 {
		return false
	}

	found := false

	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		if n == nil {
			return true
		}

		switch x := n.(type) {
		// [alias].MediaAsset as a type selector: e.g. *models.MediaAsset,
		// models.MediaAsset{...}
		case *ast.SelectorExpr:
			if isLegacySelectorExpr(x, legacyImports) {
				found = true
				return false
			}

		// Composite literal: &[alias].MediaAsset{}, [alias].MediaAsset{...}
		case *ast.CompositeLit:
			if isLegacySelectorExpr(x.Type, legacyImports) {
				found = true
				return false
			}

		// Type assertion or type switch: v.([alias].MediaAsset)
		case *ast.TypeAssertExpr:
			if isLegacySelectorExpr(x.Type, legacyImports) {
				found = true
				return false
			}
		}

		return true
	})

	return found
}

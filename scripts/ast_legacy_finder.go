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

// containsLegacyRef walks the AST looking for any reference to models.MediaAsset
// in expressions, types, and composite literals — but NOT in comments or strings.
func containsLegacyRef(file *ast.File) bool {
	found := false

	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		if n == nil {
			return true
		}

		switch x := n.(type) {
		// models.MediaAsset as a type selector: e.g. *models.MediaAsset, models.MediaAsset{
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && id.Name == "models" && x.Sel.Name == "MediaAsset" {
				found = true
				return false
			}

		// models.MediaAsset as an ident (rare, but possible after import alias)
		case *ast.Ident:
			if x.Name == "MediaAsset" {
				// Check parent context — skip if it's a standalone Ident
				// (this is a heuristic; selector expr is the main case)
			}

		// Composite literal: &models.MediaAsset{}, models.MediaAsset{...}
		case *ast.CompositeLit:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "models" && sel.Sel.Name == "MediaAsset" {
					found = true
					return false
				}
			}

		// Type assertion or type switch: v.(models.MediaAsset)
		case *ast.TypeAssertExpr:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "models" && sel.Sel.Name == "MediaAsset" {
					found = true
					return false
				}
			}

		// Function type or field type: func(clip *models.MediaAsset)
		case *ast.Field:
			// handled by the generic ast.Inspect on child nodes
		}

		return true
	})

	return found
}

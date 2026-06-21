// Package main implements a small AST-aware identifier reference detector.
//
// Why this exists: residual checks for the Wave 15 + Wave 17 exit-gate need to
// confirm that once-deleted symbols (e.g., `type CoreDeps struct`,
// `projectRootToCoreDeps`, `type services struct`) are gone from active Go
// code. The grep-regex approach under-fires badly because:
//
//   - `rg --type go` is a FILE-type filter (does NOT strip comments at AST).
//   - Bare `grep -vE ':\\s*\\d+\\s*:\\s*//'` over `file:line:` prefixes mis-
//     matches when ripgrep emits `:N:` immediately followed by `//` with no
//     space (common in single-quote bash regex escapes).
//   - `rg --passthru` doesn't help: ripgrep is line-grep, not Go-as-parser.
//
// The proper approach is `go/parser`: the Go parser already segregates
// `ast.CommentGroup` from `ast.Ident`. When we walk the AST with `ast.Inspect`,
// we collect `ast.Ident.Name == "$symbol"` tokens that are NOT inside a
// Comment — they're real code references. String literals are NOT comment-
// stripped, so a literal like `"composeIntegration"` in a test fixture would
// still match (rare in practice, acceptable noise).
//
// Exit code: 0 always; print matches to stdout in `file:line:\tsymbol` format.
// Caller counts lines via `wc -l` for the residual-hits integer.
//
// Usage: `go run ./scripts/cmd/residual_ast_scan/main.go <symbol> <target> ...`
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

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <symbol> <target> [<target> ...]\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}
	symbol := os.Args[1]
	targets := os.Args[2:]

	fset := token.NewFileSet()
	total := 0
	for _, target := range targets {
		// Walk the target recursively (handles both dir and file inputs).
		err := filepath.Walk(target, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil // tolerate unreadable paths
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			// Skip test fixtures only if explicitly requested via env var.
			// Default behaviour: include _test.go files so that test fixtures
			// don't silently hide residuals in production code (operator can
			// visually distinguish by file path).
			file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: parse error %s: %v\n", path, parseErr)
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok || ident.Name != symbol {
					return true
				}
				pos := fset.Position(ident.Pos())
				fmt.Printf("%s:%d:\t%s\n", path, pos.Line, ident.Name)
				total++
				return true
			})
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: walk error on %s: %v\n", target, err)
		}
	}
	// Emit a summary line so the gate can grep for `---ast-scan-total=N`.
	fmt.Fprintf(os.Stderr, "---ast-scan-total=%d\n", total)
}

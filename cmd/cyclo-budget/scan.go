package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go/ast"
	"go/parser"
	"go/token"
)

type finding struct {
	Key        string
	Complexity int
	File       string
	Line       int
}

// scan walks the three production roots. Missing roots are skipped so the
// same command can run in a trimmed test fixture.
func scan(root string, threshold int) (map[string]finding, error) {
	findings := make(map[string]finding)
	for _, dir := range []string{"internal", "cmd", "pkg"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		fset := token.NewFileSet()
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if shouldSkipDir(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || isGenerated(path) {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			relDir, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			relDir = filepath.ToSlash(relDir)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				value := complexity(fn.Body)
				if value <= threshold {
					continue
				}
				key := relDir + "." + functionName(fn)
				previous, exists := findings[key]
				if exists && previous.Complexity >= value {
					continue
				}
				findings[key] = finding{
					Key:        key,
					Complexity: value,
					File:       filepath.ToSlash(path),
					Line:       fset.Position(fn.Pos()).Line,
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return findings, nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case "vendor", "testdata", "node_modules", ".git", ".tmp":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// isGenerated excludes files carrying the canonical generated-code marker in
// their first 2 KiB. Generated code is not hand-maintainable and must not
// shape the ratchet.
func isGenerated(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	header, err := io.ReadAll(io.LimitReader(file, 2048))
	if err != nil {
		return false
	}
	text := string(header)
	return strings.Contains(text, "Code generated") && strings.Contains(text, "DO NOT EDIT.")
}

func complexity(body *ast.BlockStmt) int {
	value := 1
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.CaseClause, *ast.CommClause:
			value++
		case *ast.BinaryExpr:
			if n.Op == token.LAND || n.Op == token.LOR {
				value++
			}
		}
		return true
	})
	return value
}

func functionName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "(*" + receiverName(t.X) + ")"
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	default:
		return "?"
	}
}

func complexityMap(findings map[string]finding) map[string]int {
	out := make(map[string]int, len(findings))
	for key, value := range findings {
		out[key] = value.Complexity
	}
	return out
}

func printFindings(findings map[string]finding) {
	rows := sortedFindings(findings)
	for _, row := range rows {
		fmt.Printf("%d\t%s\t%s:%d\n", row.Complexity, row.Key, row.File, row.Line)
	}
}

func sortedFindings(findings map[string]finding) []finding {
	rows := make([]finding, 0, len(findings))
	for _, row := range findings {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Complexity != rows[j].Complexity {
			return rows[i].Complexity > rows[j].Complexity
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestLoopbackLiteralsStayInConfigAuthority pins the single authority for
// local endpoints. A new sidecar client must build its URL with
// LoopbackBaseURL/LoopbackURLPrefix and ask IsLoopbackHost/IsLoopbackBindHost
// instead of repeating the literal, otherwise the next environment with a
// different local layout cannot be configured without a code change.
//
// The scan parses Go ASTs rather than grepping, so documentation comments may
// still name the address. Struct tags carrying the loader default (`yaml:`)
// are exempt because they are declared next to their DTO; runtime defaults
// must still come from this package.
func TestLoopbackLiteralsStayInConfigAuthority(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	allowed := map[string]bool{
		filepath.Join(root, "internal", "platform", "config"): true,
		filepath.Join(root, "pkg", "defaults"):                true,
		filepath.Join(root, "third_party"):                    true,
		filepath.Join(root, "tests"):                          true,
	}
	markers := []string{"127.0.0.1", "localhost", "::1"}
	fset := token.NewFileSet()
	var offenses []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if allowed[path] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			if strings.HasPrefix(literal.Value, "`") && strings.Contains(literal.Value, "yaml:") {
				return true
			}
			value, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				return true
			}
			for _, marker := range markers {
				if strings.Contains(value, marker) {
					offenses = append(offenses, fset.Position(literal.Pos()).String())
					return true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan repo for loopback literals: %v", err)
	}
	if len(offenses) > 0 {
		t.Fatalf("loopback literals outside config authority (use config.LoopbackBaseURL/LoopbackURLPrefix/IsLoopbackHost): %v", offenses)
	}
}

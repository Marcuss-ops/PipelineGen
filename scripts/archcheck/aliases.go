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

func FindAliases(root string) ([]string, error) {
	var aliases []string
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == "archcheck" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		fileAST, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)

		// Check for type aliases: type T = S
		ast.Inspect(fileAST, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			// In Go AST, ts.Assign is non-zero (Assign != token.NoPos) if it's a type alias: type T = S
			if ts.Assign.IsValid() {
				aliasKey := fmt.Sprintf("%s:%d: type alias %s", relPath, fset.Position(ts.Pos()).Line, ts.Name.Name)
				aliases = append(aliases, aliasKey)
			}
			return true
		})

		// Check for import aliases: import alias "path"
		for _, imp := range fileAST.Imports {
			if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
				aliasKey := fmt.Sprintf("%s:%d: import alias %s -> %s", relPath, fset.Position(imp.Pos()).Line, imp.Name.Name, imp.Path.Value)
				aliases = append(aliases, aliasKey)
			}
		}

		return nil
	})

	return aliases, err
}

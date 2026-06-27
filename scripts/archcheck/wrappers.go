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

func FindWrappers(root string) ([]string, error) {
	var wrappers []string
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

		ast.Inspect(fileAST, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}

			// A simple wrapper function typically has exactly 1 statement in its body
			if len(fd.Body.List) == 1 {
				stmt := fd.Body.List[0]
				var call *ast.CallExpr

				switch s := stmt.(type) {
				case *ast.ReturnStmt:
					if len(s.Results) == 1 {
						if c, ok := s.Results[0].(*ast.CallExpr); ok {
							call = c
						}
					}
				case *ast.ExprStmt:
					if c, ok := s.X.(*ast.CallExpr); ok {
						call = c
					}
				}

				if call != nil {
					// Check if call is calling an imported package function (selector expr)
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if ident, ok := sel.X.(*ast.Ident); ok {
							// E.g., pkg.Do something
							wrapperKey := fmt.Sprintf("%s:%d: func %s wraps %s.%s", relPath, fset.Position(fd.Pos()).Line, fd.Name.Name, ident.Name, sel.Sel.Name)
							wrappers = append(wrappers, wrapperKey)
						}
					}
				}
			}
			return true
		})

		return nil
	})

	return wrappers, err
}

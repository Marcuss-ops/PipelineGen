package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type FileViolation struct {
	File    string
	Line    int
	Rule    string
	Message string
}

func AnalyzeImports(root string) ([]FileViolation, error) {
	var violations []FileViolation
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip directories like .git, vendor, node_modules, and scripts/archcheck itself
			name := info.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == "archcheck" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		// Parse file
		fileAST, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil // ignore parse errors on broken test files, or return nil
		}

		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)

		// Don't check test files for certain rules (like database/sql or os/exec in tests)
		isTestFile := strings.HasSuffix(info.Name(), "_test.go")

		// Analyze imports
		for _, imp := range fileAST.Imports {
			val := strings.Trim(imp.Path.Value, `"`)
			pos := fset.Position(imp.Pos())

			// Rule 1: pkg -> internal
			if strings.HasPrefix(relPath, "pkg/") && strings.HasPrefix(val, "github.com/Marcuss-ops/PipelineGen/internal/") {
				violations = append(violations, FileViolation{
					File:    relPath,
					Line:    pos.Line,
					Rule:    "pkg_to_internal",
					Message: "pkg/ must not import internal/: " + val,
				})
			}

			// Rule 2: domain -> application
			if strings.HasPrefix(relPath, "internal/domain/") && strings.HasPrefix(val, "github.com/Marcuss-ops/PipelineGen/internal/application") {
				violations = append(violations, FileViolation{
					File:    relPath,
					Line:    pos.Line,
					Rule:    "domain_to_application",
					Message: "domain must not import application: " + val,
				})
			}

			// Rule 3: domain -> infrastructure
			if strings.HasPrefix(relPath, "internal/domain/") && strings.HasPrefix(val, "github.com/Marcuss-ops/PipelineGen/internal/infrastructure") {
				violations = append(violations, FileViolation{
					File:    relPath,
					Line:    pos.Line,
					Rule:    "domain_to_infrastructure",
					Message: "domain must not import infrastructure: " + val,
				})
			}

			// Rule 4: application -> api
			if strings.HasPrefix(relPath, "internal/application/") && strings.HasPrefix(val, "github.com/Marcuss-ops/PipelineGen/internal/api") {
				violations = append(violations, FileViolation{
					File:    relPath,
					Line:    pos.Line,
					Rule:    "application_to_api",
					Message: "application must not import api: " + val,
				})
			}

			// Rule 5: application -> database/sql
			if strings.HasPrefix(relPath, "internal/application/") && val == "database/sql" {
				violations = append(violations, FileViolation{
					File:    relPath,
					Line:    pos.Line,
					Rule:    "application_to_database_sql",
					Message: "application must not import database/sql: " + val,
				})
			}

			// Rule 6: Gin outside api
			if !strings.HasPrefix(relPath, "internal/api/") && !strings.HasPrefix(relPath, "scripts/") && val == "github.com/gin-gonic/gin" {
				violations = append(violations, FileViolation{
					File:    relPath,
					Line:    pos.Line,
					Rule:    "gin_outside_api",
					Message: "gin must only be used in internal/api/: " + val,
				})
			}

			// Rule 7: os/exec outside infrastructure (or pkg/executil etc.)
			if !strings.HasPrefix(relPath, "internal/infrastructure/") && !strings.HasPrefix(relPath, "pkg/") && !strings.HasPrefix(relPath, "scripts/") && val == "os/exec" {
				violations = append(violations, FileViolation{
					File:    relPath,
					Line:    pos.Line,
					Rule:    "os_exec_outside_infrastructure",
					Message: "os/exec must only be imported in infrastructure or pkg: " + val,
				})
			}

			// Rule 9: SQLite outside infrastructure/database
			if !strings.HasPrefix(relPath, "internal/infrastructure/database/") && !strings.HasPrefix(relPath, "scripts/") && val == "github.com/mattn/go-sqlite3" {
				violations = append(violations, FileViolation{
					File:    relPath,
					Line:    pos.Line,
					Rule:    "sqlite_outside_infrastructure_database",
					Message: "sqlite driver must only be imported in internal/infrastructure/database/: " + val,
				})
			}
		}

		// Rule 8: os.Getenv outside config/app
		if !isTestFile {
			ast.Inspect(fileAST, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || ident.Name != "os" {
					return true
				}
				if sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv" {
					// Check if path is in allowed paths for Getenv
					isAllowed := strings.HasPrefix(relPath, "internal/platform/config/") ||
						strings.HasPrefix(relPath, "internal/app/") ||
						strings.HasPrefix(relPath, "config/") ||
						relPath == "internal/api/routes.go" ||
						relPath == "internal/api/server.go" ||
						strings.HasPrefix(relPath, "internal/api/middleware/") ||
						relPath == "cmd/server/main.go" ||
						relPath == "cmd/worker/main.go" ||
						relPath == "cmd/admin/main.go"

					if !isAllowed {
						pos := fset.Position(call.Pos())
						violations = append(violations, FileViolation{
							File:    relPath,
							Line:    pos.Line,
							Rule:    "os_getenv_outside_config_app",
							Message: "os.Getenv/LookupEnv call outside allowed config/app packages: " + sel.Sel.Name,
						})
					}
				}
				return true
			})
		}

		return nil
	})

	return violations, err
}

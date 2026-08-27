package boundaries

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const kernelBoundaryRule = "percheck_kernel_boundary"

var kernelForbiddenImports = []string{
	"database/sql",
	"net/http",
	"os/exec",
	"github.com/mattn/go-sqlite3",
	"google.golang.org/api/",
}

// ScanKernelBoundary enforces that kernel code is technology-neutral and only
// depends on stdlib or kernel packages. It also rejects kernel -> app,
// capabilities and platform imports. Test files are included deliberately:
// production architecture must not be bypassed by test-only imports.
func ScanKernelBoundary(root string, _ *policy.Policy, r *report.Report) {
	kernelRoot := filepath.Join(root, "internal", "kernel")
	_ = filepath.WalkDir(kernelRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		scanKernelGoFile(root, path, r)
		return nil
	})
}

func scanKernelGoFile(root, path string, r *report.Report) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File: rel, Rule: kernelBoundaryRule, MatchedRule: "kernel_parse_error",
			Severity: string(report.SeverityError), Note: "kernel file cannot be parsed: " + err.Error(),
		})
		return
	}
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		position := fset.Position(imp.Pos())
		matched := ""
		for _, forbidden := range kernelForbiddenImports {
			if importPath == forbidden || strings.HasPrefix(importPath, forbidden) {
				matched = forbidden
				break
			}
		}
		if matched == "" {
			matched = forbiddenKernelDirection(importPath)
		}
		if matched == "" {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File: rel, Line: position.Line, Rule: kernelBoundaryRule,
			MatchedRule: "kernel_forbidden_import", Severity: string(report.SeverityError),
			Note: "kernel must not import technological or outer-layer package: " + importPath,
		})
	}
}

func forbiddenKernelDirection(importPath string) string {
	const module = "github.com/Marcuss-ops/PipelineGen/"
	if !strings.HasPrefix(importPath, module) {
		return ""
	}
	internal := strings.TrimPrefix(importPath, module+"internal/")
	for _, forbidden := range []string{"app", "capabilities", "platform"} {
		if internal == forbidden || strings.HasPrefix(internal, forbidden+"/") {
			return "internal/" + forbidden
		}
	}
	return ""
}

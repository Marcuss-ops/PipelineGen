// Package scan — Check 76: input immutability (Wave 5, July 2026).
//
// scan/percheck_inputimmutability.go owns the forward-prevention gate
// for input immutability. Use-case input structs (named *Input or
// *Request) should be treated as read-only after they are passed to a
// function. Mutating an input struct in-place makes the code harder to
// reason about and breaks the caller's expectation of the input value.
//
// This gate flags two common mutation patterns:
//  1. Reassigning the whole input struct: `*req = ...`
//  2. Assigning to a field of an parameter named req/input/request/params.
//
// Allowlist:
//   - *_test.go : tests may mutate inputs to verify behavior.
//   - internal/app/** : composition-root wiring may transform inputs.
//
// Pattern anchors:
//
//	\*(req|input|request|params)\s*=              — whole-struct reassignment
//	(req|input|request|params)\.[A-Za-z_]+\s*=    — field assignment
package scan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ScanInputImmutability walks <root>/internal/application/** and
// <root>/internal/api/** for non-test .go files and flags mutations
// of input parameters.
func ScanInputImmutability(root string, pol *policy.Policy, r *report.Report) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	for _, subdir := range []string{"internal/application", "internal/api"} {
		dir := filepath.Join(root, subdir)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			scanInputImmutabilityAST(path, relSlash, r)
			return nil
		})
	}
}

var inputNameRE = regexp.MustCompile(`(?i)^(req|input|request|params)$`)

func scanInputImmutabilityAST(path, relPath string, r *report.Report) {
	if inputMutationAllowlist[relPath] {
		return
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return
	}
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		params := map[string]bool{}
		if fn.Type.Params != nil {
			for _, field := range fn.Type.Params.List {
				if _, pointer := field.Type.(*ast.StarExpr); !pointer {
					continue
				}
				if isHTTPReq(field.Type) {
					continue
				}
				for _, name := range field.Names {
					if inputNameRE.MatchString(name.Name) {
						params[name.Name] = true
					}
				}
			}
		}
		if len(params) == 0 {
			return false
		}
		shadowed := false
		ast.Inspect(fn.Body, func(x ast.Node) bool {
			if a, ok := x.(*ast.AssignStmt); ok && a.Tok == token.DEFINE {
				for _, lhs := range a.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && params[id.Name] {
						shadowed = true
					}
				}
			}
			return !shadowed
		})
		if shadowed {
			return false
		}
		ast.Inspect(fn.Body, func(x ast.Node) bool {
			assign, ok := x.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				if name, hit := assignedInputName(lhs, params); hit {
					pos := fset.Position(lhs.Pos())
					r.Violations = append(r.Violations, report.Violation{File: relPath, Line: pos.Line, Rule: "percheck_input_immutability", Severity: string(report.SeverityWarn), MatchedRule: "input_struct_mutation", Note: "input parameter mutation detected: " + name})
				}
			}
			return true
		})
		return false
	})
}

var inputMutationAllowlist = map[string]bool{
	"internal/capabilities/assets/providers/stock/stockpipeline/run_orchestrator.go": true,
	"internal/capabilities/assets/providers/stock/stockpipeline/query_resolution.go": true,
	"internal/application/jobs/enqueue_service.go":                                  true,
	"internal/application/lessons/service.go":                                       true,
	"internal/application/voiceover/stages.go":                                      true,
}

func isHTTPReq(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.StarExpr:
		return isHTTPReq(t.X)
	case *ast.SelectorExpr:
		id, ok := t.X.(*ast.Ident)
		return ok && id.Name == "http" && t.Sel.Name == "Request"
	case *ast.Ident:
		return t.Name == "Request"
	}
	return false
}

func assignedInputName(e ast.Expr, params map[string]bool) (string, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name, params[x.Name]
	case *ast.SelectorExpr:
		return assignedInputName(x.X, params)
	case *ast.IndexExpr:
		return assignedInputName(x.X, params)
	case *ast.StarExpr:
		return assignedInputName(x.X, params)
	case *ast.ParenExpr:
		return assignedInputName(x.X, params)
	}
	return "", false
}

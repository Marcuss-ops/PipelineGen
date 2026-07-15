// Package scan — Check 76: input immutability (Wave 5, July 2026).
//
// This gate parses Go source and reports mutations only when the target is an
// actual pointer parameter whose contract type is named *Input, *Request,
// *Params, or *Command. Local variables named req/input/request/params,
// value-parameter normalization, output DTO construction, and net/http
// requests are intentionally excluded because they do not mutate the caller's
// input object.
package scan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const inputImmutabilityRule = "percheck_input_immutability"

var inputParameterNames = map[string]struct{}{
	"req": {}, "input": {}, "request": {}, "params": {},
}

// ScanInputImmutability walks application and API production files and reports
// caller-visible mutations of pointer input contracts.
func ScanInputImmutability(root string, pol *policy.Policy, r *report.Report) {
	_ = pol
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
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			scanInputImmutabilityFile(path, filepath.ToSlash(rel), r)
			return nil
		})
	}
}

type trackedInput struct {
	name     string
	typeName string
}

func scanInputImmutabilityFile(path, relPath string, r *report.Report) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		r.Warnings = append(r.Warnings, inputImmutabilityRule+" parse-skip "+relPath+": "+err.Error())
		return
	}

	httpAliases := netHTTPAliases(file)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		scanInputMutationsInFunction(fset, relPath, fn.Type, fn.Body, nil, httpAliases, r)
	}
}

func scanInputMutationsInFunction(
	fset *token.FileSet,
	relPath string,
	fnType *ast.FuncType,
	body *ast.BlockStmt,
	inherited map[*ast.Object]trackedInput,
	httpAliases map[string]struct{},
	r *report.Report,
) {
	tracked := cloneTrackedInputs(inherited)
	collectTrackedInputParams(fnType, httpAliases, tracked)
	if len(tracked) == 0 {
		return
	}

	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			scanInputMutationsInFunction(fset, relPath, n.Type, n.Body, tracked, httpAliases, r)
			return false
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if input, kind, ok := mutatedTrackedInput(lhs, tracked); ok {
					appendInputMutationViolation(fset, relPath, lhs.Pos(), input, kind, r)
				}
			}
		case *ast.IncDecStmt:
			if input, kind, ok := mutatedTrackedInput(n.X, tracked); ok {
				appendInputMutationViolation(fset, relPath, n.X.Pos(), input, kind, r)
			}
		}
		return true
	})
}

func collectTrackedInputParams(fnType *ast.FuncType, httpAliases map[string]struct{}, tracked map[*ast.Object]trackedInput) {
	if fnType == nil || fnType.Params == nil {
		return
	}
	for _, field := range fnType.Params.List {
		typeName, ok := pointerInputContractType(field.Type, httpAliases)
		if !ok {
			continue
		}
		for _, name := range field.Names {
			if _, ok := inputParameterNames[strings.ToLower(name.Name)]; !ok || name.Obj == nil {
				continue
			}
			tracked[name.Obj] = trackedInput{name: name.Name, typeName: typeName}
		}
	}
}

func pointerInputContractType(expr ast.Expr, httpAliases map[string]struct{}) (string, bool) {
	star, ok := unparen(expr).(*ast.StarExpr)
	if !ok {
		return "", false
	}
	base := unparen(star.X)
	for {
		switch t := base.(type) {
		case *ast.IndexExpr:
			base = unparen(t.X)
		case *ast.IndexListExpr:
			base = unparen(t.X)
		default:
			goto resolved
		}
	}

resolved:
	qualifier, name := namedType(base)
	if name == "" {
		return "", false
	}
	if name == "Request" {
		if _, isHTTP := httpAliases[qualifier]; isHTTP {
			return "", false
		}
	}
	for _, suffix := range []string{"Input", "Request", "Params", "Command"} {
		if strings.HasSuffix(name, suffix) {
			if qualifier == "" {
				return name, true
			}
			return qualifier + "." + name, true
		}
	}
	return "", false
}

func namedType(expr ast.Expr) (qualifier, name string) {
	switch t := expr.(type) {
	case *ast.Ident:
		return "", t.Name
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name, t.Sel.Name
		}
	}
	return "", ""
}

func netHTTPAliases(file *ast.File) map[string]struct{} {
	aliases := map[string]struct{}{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "net/http" {
			continue
		}
		alias := "http"
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if alias != "_" && alias != "." {
			aliases[alias] = struct{}{}
		}
	}
	return aliases
}

func mutatedTrackedInput(expr ast.Expr, tracked map[*ast.Object]trackedInput) (trackedInput, string, bool) {
	expr = unparen(expr)
	kind := "input struct field assignment"
	if star, ok := expr.(*ast.StarExpr); ok {
		if id, ok := rootIdent(star.X); ok {
			if input, exists := tracked[id.Obj]; exists {
				return input, "whole input struct reassignment", true
			}
		}
		return trackedInput{}, "", false
	}
	id, ok := rootIdent(expr)
	if !ok || id.Obj == nil {
		return trackedInput{}, "", false
	}
	input, exists := tracked[id.Obj]
	if !exists {
		return trackedInput{}, "", false
	}
	switch expr.(type) {
	case *ast.SelectorExpr, *ast.IndexExpr:
		return input, kind, true
	default:
		return trackedInput{}, "", false
	}
}

func rootIdent(expr ast.Expr) (*ast.Ident, bool) {
	switch e := unparen(expr).(type) {
	case *ast.Ident:
		return e, true
	case *ast.SelectorExpr:
		return rootIdent(e.X)
	case *ast.IndexExpr:
		return rootIdent(e.X)
	case *ast.StarExpr:
		return rootIdent(e.X)
	}
	return nil, false
}

func appendInputMutationViolation(fset *token.FileSet, relPath string, pos token.Pos, input trackedInput, kind string, r *report.Report) {
	r.Violations = append(r.Violations, report.Violation{
		File:        relPath,
		Line:        fset.Position(pos).Line,
		Rule:        inputImmutabilityRule,
		Severity:    string(report.SeverityWarn),
		MatchedRule: "input_struct_mutation",
		Note:        "input parameter mutation detected: " + kind + " on " + input.name + " (" + input.typeName + ") — treat pointer input contracts as read-only; copy into a local value or return a dedicated output type instead",
	})
}

func cloneTrackedInputs(src map[*ast.Object]trackedInput) map[*ast.Object]trackedInput {
	dst := make(map[*ast.Object]trackedInput, len(src))
	for obj, input := range src {
		dst[obj] = input
	}
	return dst
}

func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

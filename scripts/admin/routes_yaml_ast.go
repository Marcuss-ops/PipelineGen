// Package main — routes_yaml_ast.go contains the AST inspection engine
// extracted from generate_routes_yaml.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band C #2).
//
// Owns: inspectAPIFile, inspectAssignStmt, inspectCallExpr, stringLiteralValue.
package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// inspectAPIFile walks one file and extracts route registration
// patterns. Returns (routes, warnings). Warnings are emitted for
// patterns the walker cannot conclusively fold (e.g. dynamic-path
// arguments, unfound gin methods) so the gate's drift-detection
// surfaces them to the operator after the generator runs.
func inspectAPIFile(fset *token.FileSet, file *ast.File, relPath string) ([]manifestRoute, []string) {
	var routes []manifestRoute
	var warnings []string

	// Track group-prefix assignments within this file, so that
	// children emitted under `children := rg.Group("/api/foo");
	// children.POST("/bar", h.B)` get the parent prefix folded
	// onto the child literal at emission time.
	groupPrefixByIdent := map[string]string{}

	// Const declarations whose values are string literals — used
	// to fold const route paths.
	constStringByIdent := map[string]string{}

	// Walk const decls first so the const map is available during
	// call-statement inspection.
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if uq, err := strconv.Unquote(lit.Value); err == nil {
						constStringByIdent[name.Name] = uq
					}
				}
			}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			inspectAssignStmt(fset, stmt, relPath, groupPrefixByIdent, &routes, &warnings)
		case *ast.ExprStmt:
			inspectCallExpr(fset, stmt.X, relPath, groupPrefixByIdent, constStringByIdent, &routes, &warnings)
		}
		return true
	})

	return routes, warnings
}

// inspectAssignStmt detects "children := rg.Group(prefix)" + "rg.POST(...)"
// assignment patterns. Group assignments populate groupPrefixByIdent
// for later child-method calls; direct method calls emit route rows
// when the path arg is a string literal.
//
// Chained-group assignments (`g2 := g1.Group("/a").Group("/b")`) emit
// a warning AND return immediately — the walker does NOT fold
// multi-level prefixes (would require accumulating two literals that
// the static AST cannot resolve). Returning early prevents the
// silent half-fold of the OUTERMOST prefix only, which would
// later multiply children's path by the wrong prefix and surface as
// confusing drift in the C2-E gate.
func inspectAssignStmt(
	fset *token.FileSet,
	stmt *ast.AssignStmt,
	relPath string,
	groupPrefixByIdent map[string]string,
	routes *[]manifestRoute,
	warnings *[]string,
) {
	if len(stmt.Lhs) != 1 || len(stmt.Rhs) != 1 {
		return
	}
	lhsIdent, ok := stmt.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}
	call, ok := stmt.Rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}

	// Chained-group pattern: outerSel.X is itself a CallExpr whose Fun
	// is `.Group` AND the outer Sel is ALSO `.Group` (true multi-level
	// group chain, e.g. `g2 := g1.Group("/a").Group("/b")`). Fire the
	// warning and return EARLY.
	if innerCall, ok := sel.X.(*ast.CallExpr); ok {
		if innerSel, ok := innerCall.Fun.(*ast.SelectorExpr); ok && innerSel.Sel.Name == "Group" && sel.Sel.Name == "Group" {
			*warnings = append(*warnings, fmt.Sprintf(`%s: chained Group assignment on line %d (e.g. g2 := g1.Group("/a").Group("/b")); the walker does NOT fold multi-level prefixes — children emit without the cumulative prefix and will surface as drift in the C2-E gate`,
				relPath, fset.Position(call.Pos()).Line))
			return
		}
	}

	// Group assignment to a fresh ident: `children := rg.Group("/api/foo")`
	if sel.Sel.Name == "Group" {
		if len(call.Args) >= 1 {
			if prefix := stringLiteralValue(call.Args[0]); prefix != "" {
				groupPrefixByIdent[lhsIdent.Name] = prefix
			} else {
				*warnings = append(*warnings, fmt.Sprintf("%s: dynamic Group prefix on line %d — children will surface as drift in C2-E gate",
					relPath, fset.Position(call.Pos()).Line))
			}
		}
		return
	}

	// Direct method call: rg.POST(...) — registered as a route.
	if m := methodByBaseName(sel.Sel.Name); m != "" {
		if len(call.Args) >= 1 {
			path := stringLiteralValue(call.Args[0])
			if path == "" {
				*warnings = append(*warnings, fmt.Sprintf("%s: %s call with non-literal path on line %d — route will not appear in manifest",
					relPath, sel.Sel.Name, fset.Position(call.Pos()).Line))
				return
			}
			*routes = append(*routes, manifestRoute{
				Method: m,
				Path:   path,
				Source: relPath,
			})
			return
		}
		return
	}

	// Unfound gin method (Handle/Any/Match/Redirect/Static/StaticFS).
	if isUnfoundGinMethod(sel.Sel.Name) {
		*warnings = append(*warnings, fmt.Sprintf("%s: %s assignment on line %d is NOT folded into the manifest by the walker; routes registered via this call will surface as docs-only DRIFT in the C2-E gate — investigate or accept as a known limitation",
			relPath, sel.Sel.Name, fset.Position(call.Pos()).Line))
	}
}

// inspectCallExpr handles bare statement-expressions like
// `rg.POST("/api/x", h.B)` (no `:=`) and emits a row immediately.
// Already-folded children (e.g. `children.POST("/bar", h.B)` where
// `children := rg.Group("/api/foo")`) get parent prefix appended via
// groupPrefixByIdent.
//
// Single-statement chained Group+POST (rare):
//
//	rg.Group("/api/foo").POST("/bar", h.Baz)
//
// → folded inline at emission time.
func inspectCallExpr(
	fset *token.FileSet,
	expr ast.Expr,
	relPath string,
	groupPrefixByIdent map[string]string,
	constStringByIdent map[string]string,
	routes *[]manifestRoute,
	warnings *[]string,
) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}

	// Single-statement chained Group+Method:
	// rg.Group("/api/foo").POST("/bar", h.Baz) → "/api/foo/bar"
	if innerCall, ok := sel.X.(*ast.CallExpr); ok {
		if innerSel, ok := innerCall.Fun.(*ast.SelectorExpr); ok {
			if innerSel.Sel.Name == "Group" && len(innerCall.Args) >= 1 {
				prefix := stringLiteralValue(innerCall.Args[0])
				if prefix == "" {
					*warnings = append(*warnings, fmt.Sprintf("%s: chained-group prefix on line %d is non-literal — fallback emitted without prefix",
						relPath, fset.Position(call.Pos()).Line))
				}
				if m := methodByBaseName(sel.Sel.Name); m != "" {
					if len(call.Args) >= 1 {
						path := stringLiteralValue(call.Args[0])
						if prefix != "" && path != "" {
							*routes = append(*routes, manifestRoute{
								Method: m,
								Path:   prefix + path,
								Source: relPath,
							})
							return
						}
					}
				}
			}
		}
	}

	// Receiver-side fold: children.POST("/x", h) where children is
	// an ident bound earlier in the same file to a Group prefix.
	if id, ok := sel.X.(*ast.Ident); ok {
		if prefix, found := groupPrefixByIdent[id.Name]; found {
			if m := methodByBaseName(sel.Sel.Name); m != "" {
				if len(call.Args) >= 1 {
					path := stringLiteralValue(call.Args[0])
					if path != "" {
						*routes = append(*routes, manifestRoute{
							Method: m,
							Path:   prefix + path,
							Source: relPath,
						})
						return
					}
				}
			}
		}
	}

	// Top-level router-method detection: rg.POST("/x", h.Foo) /
	// engine.GET("/x", h.Foo). When the receiver-rg ident cannot be
	// matched against groupPrefixByIdent, emit a row with the
	// literal path as-is.
	if m := methodByBaseName(sel.Sel.Name); m != "" {
		if len(call.Args) >= 1 {
			path := stringLiteralValue(call.Args[0])
			if path == "" {
				// Fallback: try resolving via constStringByIdent
				if id, ok := call.Args[0].(*ast.Ident); ok {
					if cv, found := constStringByIdent[id.Name]; found {
						path = cv
					}
				}
			}
			if path == "" {
				*warnings = append(*warnings, fmt.Sprintf("%s: %s call with non-literal path on line %d — route will not appear in manifest",
					relPath, sel.Sel.Name, fset.Position(call.Pos()).Line))
				return
			}
			*routes = append(*routes, manifestRoute{
				Method: m,
				Path:   path,
				Source: relPath,
			})
			return
		}
	}

	// Unfound gin methods: Handle/Any/Match/Redirect/Static/StaticFS.
	if isUnfoundGinMethod(sel.Sel.Name) {
		*warnings = append(*warnings, fmt.Sprintf("%s: %s call on line %d is NOT folded into the manifest by the walker (gin method takes the HTTP verb at runtime or is filesystem-bound); routes registered via this call will surface as docs-only DRIFT in the C2-E gate — investigate or accept as a known limitation",
			relPath, sel.Sel.Name, fset.Position(call.Pos()).Line))
	}
}

func stringLiteralValue(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	uq, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return uq
}

// Package main is the Blocco C2-E pre-step generator (June 2026):
//
//	scans the canonical HTTP capability surface (internal/api/**/RegisterRoutes
//	method bodies + nesting through api.NewRouteModule closures) and emits
//	`architecture/routes.yaml`, the structured manifest that the C2-E gate
//	(`scripts/archcheck/gates/gate_c2_route_manifest.go`) compares against
//	`docs/api/ACTIVE_API_GENERATED.md` for the route ≡ manifest ≡ docs
//	invariant.
//
// ── What this captures ────────────────────────────────────────────────
//
// The walker inspects every .go file under internal/api/** (production
// only, skipping *_test.go and generated/) and looks for HTTP method
// registrations on a *gin.RouterGroup / *gin.Engine / *gin.IRouter
// receiver. Recognised shapes:
//
//  1. Top-level method calls on the receiver
//     rg.POST("/api/foo", h.Bar)
//     engine.GET("/health", h.HealthCheck)
//     → exactly one route row, path = literal "/api/foo".
//
//  2. Constant string paths computed at compile time
//     const routePath = "/api/foo"
//     rg.POST(routePath, ...)
//     → if the const value is recoverable from the enclosing file,
//     emitted; otherwise logged as a manual-annotation row.
//
//  3. Sub-router chains closed over a constant prefix (best-effort):
//     func (m *FooModule) RegisterRoutes(rg *gin.RouterGroup) {
//     children := rg.Group("/api/foo")
//     children.POST("/bar", h.Baz)
//     children.DELETE("/:id", h.Delete)
//     }
//     → one row per emitted child, full path = "/api/foo" + child
//     literal = "/api/foo/bar" / "/api/foo/:id".
//
//  4. Single-statement chained Group+POST (rare):
//     rg.Group("/api/foo").POST("/bar", h.Baz)
//     → folded inline at emission time.
//
// ── What surfaces as DRIFT in the C2-E gate ───────────────────────────
//
//   - Variable group prefixes (`rg.Group(prefix)` where `prefix` is not
//     a string literal at compile time) — children emit without the
//     parent prefix; the gate's "manifest-only" or "docs-only" diff
//     surfaces the discrepancy.
//   - Routes registered via `.Handle` / `.Any` / `.Match` / `.Redirect` /
//     `.Static` / `.StaticFS` (gin methods that take the HTTP verb at
//     runtime or are filesystem-bound) — emit a warning per call so the
//     operator sees the gap; the C2-E gate's "docs-only" diff reports
//     the missing-from-manifest rows.
//   - Chained-group assignments like `g2 := g1.Group("/a").Group("/b")` —
//     only the outermost Group is folded; cumulative prefix NOT applied.
//     Emit a warning per chained assignment.
//   - Routes registered via methods called on values returned from
//     helper closures (`buildSubrouter(rg).POST(...)` where the helper
//     is in another file) — same drift-detection fallback.
//
// ── Usage ─────────────────────────────────────────────────────────────
//
//	go run ./scripts/admin/generate_routes_yaml.go [root_dir] [output_path]
//
// root_dir defaults to "."; output_path defaults to "architecture/routes.yaml".
// Exit codes: 0 = success, 2 = invocation error, 3 = zero-routes
// sentinel (the AST scanner is broken, NOT "no API exists" — the
// latter is impossible in this codebase).
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// HTTP methods recognised on *gin.RouterGroup / *gin.Engine receivers.
// Order matches the typical handler-method signature grouping; the
// mapping table itself is unordered.
var httpMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}

// gin methods that the walker CANNOT resolve without runtime /
// whole-program analysis. Each emits a per-call warning so the
// operator can detect C2-E gate drift.
var unfoundGinMethods = []string{"Handle", "Any", "Match", "Redirect", "Static", "StaticFS"}

// methodByBaseName returns the uppercase HTTP method if `name`
// matches one of the gin-recognized router methods, otherwise "".
// The check is case-sensitive on the underlying name (gin uses ALL
// CAPS); function-shaped calls like `.Handle(h.Method, path, ...)`
// take the method as a string argument and are NOT matched here.
func methodByBaseName(name string) string {
	upper := strings.ToUpper(name)
	for _, m := range httpMethods {
		if upper == m {
			return m
		}
	}
	return ""
}

// isUnfoundGinMethod reports whether `name` is one of the gin
// router methods that this static-AST walker cannot fold.
func isUnfoundGinMethod(name string) bool {
	for _, m := range unfoundGinMethods {
		if m == name {
			return true
		}
	}
	return false
}

// ── Output shape ─────────────────────────────────────────────────────

type manifestDocument struct {
	Routes []manifestRoute `yaml:"routes"`
}

type manifestRoute struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Source string `yaml:"source,omitempty"`
}

// ── main ─────────────────────────────────────────────────────────────

func main() {
	var root, outputPath string
	flag.StringVar(&root, "root", ".", "repo root (defaults to \".\")")
	flag.StringVar(&outputPath, "output", "architecture/routes.yaml", "output YAML path (defaults to architecture/routes.yaml)")
	flag.Parse()

	if flag.NArg() >= 1 {
		root = flag.Arg(0)
	}
	if flag.NArg() >= 2 {
		outputPath = flag.Arg(1)
	}

	fset := token.NewFileSet()
	files, err := discoverAPIFiles(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: discover API files: %v\n", err)
		os.Exit(2)
	}
	sort.Strings(files)

	var routes []manifestRoute
	var warnings []string
	for _, path := range files {
		relPath := relSlashed(root, path)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("parse error %s: %v", relPath, parseErr))
			continue
		}
		lineRoutes, lineWarnings := inspectAPIFile(fset, file, relPath)
		routes = append(routes, lineRoutes...)
		warnings = append(warnings, lineWarnings...)
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Method != routes[j].Method {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})

	// De-duplicate (same method+path may appear in multiple files).
	// First-source-wins; duplicate-detect warnings surface BEFORE the
	// prune so operators can investigate.
	var dupWarnings []string
	routes, dupWarnings = dedupeManifest(routes)
	warnings = append(warnings, dupWarnings...)

	if len(routes) == 0 {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: 0 routes detected — sentinel error, inspect walker (probably a parse-fatal cascade)\n")
		os.Exit(3)
	}

	doc := manifestDocument{Routes: routes}
	yamlData, err := yaml.Marshal(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: marshal YAML: %v\n", err)
		os.Exit(2)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: mkdir %s: %v\n", filepath.Dir(outputPath), err)
		os.Exit(2)
	}
	if err := os.WriteFile(outputPath, yamlData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: write %s: %v\n", outputPath, err)
		os.Exit(2)
	}

	fmt.Fprintf(os.Stderr, "C2-E pre-step: wrote %d route(s) to %s (warnings=%d)\n", len(routes), outputPath, len(warnings))
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "  WARN: %s\n", w)
	}
}

// ── File walker ──────────────────────────────────────────────────────

// discoverAPIFiles returns every production .go file under
// internal/api/**, excluding *_test.go and generated/ subtrees.
// The walker mirrors the C2-A / C2-C gate walker's scope (production-
// code-only) so the generator's output matches the surface that
// actually reaches the gin.Engine.
func discoverAPIFiles(root string) ([]string, error) {
	apiDir := filepath.Join(root, "internal", "api")
	if _, statErr := os.Stat(apiDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("internal/api not found under %s", root)
		}
		return nil, statErr
	}
	var files []string
	walkErr := filepath.Walk(apiDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil {
			return nil
		}
		if info.IsDir() {
			basename := filepath.Base(path)
			if basename == "generated" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return files, nil
}

// relSlashed normalizes a path to repo-relative, forward-slash form
// for stable YAML `source:` field output.
func relSlashed(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

// ── AST inspection ──────────────────────────────────────────────────

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
	// is `.Group`. Fire the warning and return EARLY so we do NOT
	// populate groupPrefixByIdent with the OUTERMOST prefix only
	// (which would silently half-fold children).
	if innerCall, ok := sel.X.(*ast.CallExpr); ok {
		if innerSel, ok := innerCall.Fun.(*ast.SelectorExpr); ok && innerSel.Sel.Name == "Group" {
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

// dedupeManifest removes duplicate (method, path) pairs, keeping
// the first occurrence (lowest source path lexically, after the
// upstream sort). Routes registered in multiple files surface as
// a single row with the lowest source.
//
// Duplicate detection emits a warning per (method, path) seen >1
// BEFORE the prune so the operator can investigate the source
// files. The canonical first-rule of godlike/06 (one owner per
// fact) means duplicate emitters are almost always a bug.
func dedupeManifest(in []manifestRoute) ([]manifestRoute, []string) {
	counts := map[string][]string{}
	out := make([]manifestRoute, 0, len(in))
	seen := map[string]bool{}
	for _, r := range in {
		key := r.Method + " " + r.Path
		counts[key] = append(counts[key], r.Source)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	var warnings []string
	for key, srcs := range counts {
		if len(srcs) > 1 {
			warnings = append(warnings, fmt.Sprintf("duplicate route %q registered from multiple files: %s — investigate (intentional mirror vs accidental duplication?)",
				key, strings.Join(srcs, ", ")))
		}
	}
	return out, warnings
}

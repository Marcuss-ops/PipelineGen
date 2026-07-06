// Package main — archcheck Phase 0 rules.
//
// phase0_checks.go owns the 5 Phase 0 baseline-on-baseline rules
// (Wave 19 PR-A, June 2026). These rules enforce the North Star
// "Compiler-enforced boundaries" invariant:
//
//  1. checkInterfaceBraceGrowth — bare `interface{}` / `any` in
//     production Go code (field/parameter/return types).
//  2. checkSetterDetector — post-construction `Set<X>` methods on
//     Service / Client / Builder / Cfg / Adapter types.
//  3. checkTypeAliasCrossPkg — `type X = pkg.Y` aliases that cross
//     package boundaries.
//  4. checkFakeRoute — handler methods returning
//     `http.StatusNotImplemented` (501).
//  5. checkHandlerToDB — handler files reaching into `database/sql`.
//
// During the minor cycle, these rules run via `--future-ratchet` and
// fail ONLY on regressions (new entries vs the committed baseline
// file `scripts/archcheck/phase0_baseline.json`). After the cycle,
// the operator promotes them by removing the flag and folding them
// into `runRatchetChecks()`.
//
// The rules are consumed by runner.go::runPhase0Checks and
// main.go::runSeedBaseline.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	bl "github.com/Marcuss-ops/PipelineGen/scripts/archcheck/baseline"
)

// ── Phase 0 rule 1: interface{} growth ────────────────────────────────────

// checkInterfaceBraceGrowth uses go/parser + go/ast to count only
// actual field/parameter/return types declared as `interface{}` or
// `any` (NOT comment prose, NOT generic type parameters like
// `[T any]`). The previous regex-based approach caught english-prose
// tokens ("under any allowed base path", "without touching any
// dependency") and prompt strings, inflating the baseline with false
// positives. Post-AST-rewrite, the baseline shrinks to real type usages
// only, and stale entries (paths no longer in the codebase, e.g.
// internal/api/helpers.go) are surfaced as violations.
func checkInterfaceBraceGrowth(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_interface_braces_actual":      0,
		"phase0_interface_braces_baseline":    len(baseline),
		"phase0_interface_braces_regressions": 0,
		"phase0_interface_braces_stale":       0,
	}

	actual = walkASTForInterfaceAny()
	stats["phase0_interface_braces_actual"] = len(actual)

	added, stale := bl.Compare(actual, baseline)

	stats["phase0_interface_braces_regressions"] = len(added)
	for _, line := range added {
		violations = append(violations, "phase0 interface{}/any growth: "+line)
	}
	stats["phase0_interface_braces_stale"] = len(stale)
	for _, entry := range stale {
		violations = append(violations, "phase0 interface{} baseline stale (path or type no longer present): "+entry)
	}

	sort.Strings(violations)
	return actual, violations, stats
}

// walkASTForInterfaceAny walks all production Go files under internal/
// and pkg/ (excluding *_test.go) and returns every line where a
// field, parameter, or return type is declared as bare `interface{}`
// or the predeclared identifier `any`. Generic type parameters
// (`[T any]`) are explicitly excluded by matching on the parent AST
// node types (*ast.FuncType, *ast.StructType, *ast.InterfaceType)
// and processing only Params/Results/Fields/Methods — never TypeParams.
// The output format matches the legacy rg shape: "path:line: text" so
// existing baseline entries that survive the AST filter can still be
// compared.
func walkASTForInterfaceAny() []string {
	var results []string
	fset := token.NewFileSet()

	processFields := func(fl *ast.FieldList, filePath string) {
		if fl == nil {
			return
		}
		for _, field := range fl.List {
			if field.Type == nil || !isBareInterfaceOrAny(field.Type) {
				continue
			}
			pos := fset.Position(field.Type.Pos())
			normPath := filepath.ToSlash(filePath)
			results = append(results, fmt.Sprintf("%s:%d: %s",
				normPath, pos.Line, renderTypeLine(field)))
		}
	}

	for _, root := range []string{"internal", "pkg"} {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if parseErr != nil {
				return nil
			}

			// Only visit FuncType (Params + Results), StructType (Fields),
			// and InterfaceType (Methods). TypeParams is explicitly
			// skipped — generic type parameters like [T any] are NOT
			// field/parameter/return declarations.
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FuncType:
					processFields(x.Params, path)
					processFields(x.Results, path)
				case *ast.StructType:
					processFields(x.Fields, path)
				case *ast.InterfaceType:
					processFields(x.Methods, path)
				}
				return true
			})
			return nil
		})
	}
	sort.Strings(results)
	return results
}

// isBareInterfaceOrAny returns true when the expression node is exactly
// `interface{}` (ast.InterfaceType with zero methods) or the predeclared
// identifier `any`. Generic type-parameter usages ([T any]) reach us via
// a different AST path (TypeParams field list), so they are excluded.
func isBareInterfaceOrAny(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.InterfaceType:
		return t.Methods == nil || t.Methods.NumFields() == 0
	case *ast.Ident:
		return t.Name == "any"
	default:
		return false
	}
}

// renderTypeLine returns a canonical text representation of the
// field containing the interface{}/any type. Uses AST names + type
// string rather than re-reading the source file, so the output is
// deterministic regardless of source formatting.
func renderTypeLine(field *ast.Field) string {
	names := fieldNames(field)
	typeStr := typeString(field.Type)
	if names == "" {
		return typeStr
	}
	return names + " " + typeStr
}

// fieldNames returns the comma-separated names of a field, or "" if
// the field is anonymous (embedded).
func fieldNames(field *ast.Field) string {
	if len(field.Names) == 0 {
		return ""
	}
	parts := make([]string, len(field.Names))
	for i, n := range field.Names {
		parts[i] = n.Name
	}
	return strings.Join(parts, ", ")
}

// typeString returns a canonical string representation of an ast.Expr
// used as a type. Covers the two cases we care about: interface{} and any.
func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ident:
		return t.Name
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// ── Phase 0 rule 2: setter detector ────────────────────────────────────────

// checkSetterDetector scans every production Go file for
// post-construction setter methods of the shape
// `func (x *Type) SetFoo(...)` or `func (x Type) SetFoo(...)`.
// The North Star invariant forbids setters on Service / Client /
// Builder / Cfg types; PR-B removed SetReranker and SetVLLMConfig as
// canonical examples. The rule is intentionally permissive (it counts
// EVERY Set<X> method, not just the typed-named ones) so the baseline
// can be audited holistically.
func checkSetterDetector(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_setters_actual":      0,
		"phase0_setters_baseline":    len(baseline),
		"phase0_setters_regressions": 0,
	}
	out, err := exec.Command("rg", "-n",
		`func\s+\(\w+\s+\*?\w+\)\s+Set[A-Z]\w*\(`,
		"internal", "pkg",
		"--type", "go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkSetterDetector: rg failed: %v", err)}, stats
	}
	actual = splitNonEmpty(strings.TrimRight(string(out), "\n"))
	stats["phase0_setters_actual"] = len(actual)
	added := bl.SubtractSet(actual, baseline)
	stats["phase0_setters_regressions"] = len(added)
	for _, line := range added {
		violations = append(violations, "phase0 dependency setter introduced: "+line)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

// ── Phase 0 rule 3: type alias cross-package ───────────────────────────────

// checkTypeAliasCrossPkg detects `type X = pkg.Y` aliases whose
// `pkg.Y` source lives in a different Go package than the file's
// own. North Star calls these "pass-through aliases" that hide real
// architectural debt; PR-B cleaned up several (see commit d61068b3
// for the realtime / association alias removals).
func checkTypeAliasCrossPkg(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_type_aliases_cross_pkg_actual":      0,
		"phase0_type_aliases_cross_pkg_baseline":    len(baseline),
		"phase0_type_aliases_cross_pkg_regressions": 0,
	}
	out, err := exec.Command("rg", "-n",
		`^\s*type\s+\w+\s*=\s*[a-z][a-z0-9_]*\.[A-Z]\w*\s*$`,
		"internal", "pkg",
		"--type", "go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkTypeAliasCrossPkg: rg failed: %v", err)}, stats
	}
	actual = splitNonEmpty(strings.TrimRight(string(out), "\n"))
	stats["phase0_type_aliases_cross_pkg_actual"] = len(actual)
	added := bl.SubtractSet(actual, baseline)
	stats["phase0_type_aliases_cross_pkg_regressions"] = len(added)
	for _, line := range added {
		violations = append(violations, "phase0 cross-package type alias: "+line)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

// ── Phase 0 rule 4: fake route ────────────────────────────────────────────

// checkFakeRoute detects handler methods whose MountGin body returns
// `http.StatusNotImplemented` (501). The canonical "fake route" the
// godlike program forbids is documented in
// `docs/architecture/godlike/14_INITIAL_BACKLOG.md` (Block 1 — Dead
// HTTP surfaces). The pattern is intentionally narrow (501 only) so
// real `not implemented` errors in handlers returning 503/500 with
// full message remain unaffected; only the canonical "mount the route
// but never serve content" code shape is caught.
func checkFakeRoute(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_fake_routes_actual":      0,
		"phase0_fake_routes_baseline":    len(baseline),
		"phase0_fake_routes_regressions": 0,
	}
	out, err := exec.Command("rg", "-n",
		`c\.(JSON|String|AbortWithStatus|Status)\s*\(\s*http\.StatusNotImplemented`,
		"internal/api",
		"--type", "go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkFakeRoute: rg failed: %v", err)}, stats
	}
	actual = splitNonEmpty(strings.TrimRight(string(out), "\n"))
	stats["phase0_fake_routes_actual"] = len(actual)
	added := bl.SubtractSet(actual, baseline)
	stats["phase0_fake_routes_regressions"] = len(added)
	for _, line := range added {
		violations = append(violations, "phase0 fake route (501) introduced: "+line)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

// ── Phase 0 rule 5: handler-to-DB ──────────────────────────────────────────

// checkHandlerToDB scans every production Go file under `internal/api/`
// whose name matches `handler*.go` (mirror of the North Star Pattern
// 8 invariant: `internal/api/**` is thin transport only, never the
// owner of database writes). A file is flagged if its surface contains
// either a `database/sql` import OR a `*sql.DB` field-type substring.
//
// Files which are explicitly excluded from the gate: any path ending
// in `_test.go` (handled by the rg `--glob` filter) and any path whose
// filename starts with `health_integration_test.go` (test-typed files
// are already excluded, but the substring is repeated for clarity in
// the violation message).
func checkHandlerToDB(baseline []string) (actual []string, violations []string, stats map[string]int) {
	stats = map[string]int{
		"phase0_handlers_to_db_actual":      0,
		"phase0_handlers_to_db_baseline":    len(baseline),
		"phase0_handlers_to_db_regressions": 0,
	}
	out, err := exec.Command("rg", "-nl",
		`database/sql|\*sql\.DB`,
		"internal/api",
		"--type", "go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		return actual, []string{fmt.Sprintf("checkHandlerToDB: rg failed: %v", err)}, stats
	}
	actual = bl.NormalizePaths(splitNonEmpty(string(out)))
	stats["phase0_handlers_to_db_actual"] = len(actual)
	added := bl.SubtractSet(actual, baseline)
	stats["phase0_handlers_to_db_regressions"] = len(added)
	for _, path := range added {
		violations = append(violations, "phase0 handler file reaches into database/sql: "+path)
	}
	sort.Strings(violations)
	return actual, violations, stats
}

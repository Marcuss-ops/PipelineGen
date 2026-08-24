// Package scan — unit tests for percheck_api_module_deps_max_8.
//
// percheck_api_module_deps_max_8_test.go exercises the AST
// scanner against synthetic module.go files written to a
// per-test tmp dir. No filesystem fixtures from the canonical
// tree are used so the test surface stays self-contained and
// does not pick up incidental drift from real modules during
// PR-iteration.
//
// Coverage:
//   - 7-field struct → zero violations (PASS).
//   - 8-field struct → zero violations (PASS, boundary).
//   - 9-field struct → 1 violation with ActualCount=9, AllowedCount=8.
//   - 9-field struct in bypass-list rel path → 1 WARN, zero violations.
//   - 0-field type alias (`type Dependencies = Other`) → not found → zero.
//   - separate Deps struct (9 fields) → 1 violation (alternative name covered).
//   - file without any Dependencies/Deps struct → not found → zero.
//   - parse-error path (syntax-broken file) → 1 violation with MatchedRule=deps_count_parse_fail.
//   - embedded field → counted as 1 (DI perspective).
//   - grouped multi-decl `A, B Service` → counts as 2.
package governance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// testPercheckDepsRoot scaffolds a synthetic module tree under
// t.TempDir() and returns the root path. Each per-test layout
// is fully isolated so concurrent runs / parallel test bursts
// are race-safe.
func testPercheckDepsRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	apiRoot := filepath.Join(root, "internal", "api")
	if err := os.MkdirAll(apiRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for relPath, body := range files {
		full := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	return root
}

// filterDepsMaxViolations returns the subset of r.Violations
// attributable to the percheck_api_module_deps_max_8 scanner
// (other scanners, if added concurrently, are filtered out so
// the test assertion does not flake on incidental noise).
func filterDepsMaxViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == apiModuleDepsMaxRule {
			out = append(out, v)
		}
	}
	return out
}

// TestScanApiModuleDepsMax8_BelowCap verifies that a 7-field
// `Dependencies` struct produces zero violations.
func TestScanApiModuleDepsMax8_BelowCap(t *testing.T) {
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": `package test

type Dependencies struct {
	A int
	B int
	C int
	D int
	E int
	F int
	G int
}

func Build(deps Dependencies) error { return nil }
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 0 {
		t.Fatalf("below-cap 7 fields should pass; got %d violations: %+v", len(got), got)
	}
}

// TestScanApiModuleDepsMax8_Boundary8 verifies that the
// 8-field cap is inclusive — 8 fields is the boundary, NOT
// a violation. Mirrors the "max_struct_deps: 8" policy meaning
// (≤8 is the legal band).
func TestScanApiModuleDepsMax8_Boundary8(t *testing.T) {
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": `package test

type Dependencies struct {
	A int
	B int
	C int
	D int
	E int
	F int
	G int
	H int
}

func Build(deps Dependencies) error { return nil }
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 0 {
		t.Fatalf("8-field boundary should pass; got %d violations: %+v", len(got), got)
	}
}

// TestScanApiModuleDepsMax8_OverCap verifies that 9 fields
// trigger exactly one violation with the correct count surface.
func TestScanApiModuleDepsMax8_OverCap(t *testing.T) {
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": `package test

type Dependencies struct {
	A int
	B int
	C int
	D int
	E int
	F int
	G int
	H int
	I int
}

func Build(deps Dependencies) error { return nil }
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 1 {
		t.Fatalf("9-field over-cap should produce exactly 1 violation; got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.ActualCount != 9 {
		t.Fatalf("ActualCount: got %d, want 9", v.ActualCount)
	}
	if v.AllowedCount != apiModuleDepsMaxCap {
		t.Fatalf("AllowedCount: got %d, want %d", v.AllowedCount, apiModuleDepsMaxCap)
	}
	if v.MatchedRule != "deps_count_over_8" {
		t.Fatalf("MatchedRule: got %q, want deps_count_over_8", v.MatchedRule)
	}
	if !strings.Contains(v.Note, "actual field count: 9") {
		t.Fatalf("Note should surface actual count 9: %q", v.Note)
	}
}

// TestScanApiModuleDepsMax8_Bypass9Fields verifies that 9 fields
// in a bypass-listed module.go produces exactly one WARN and
// ZERO violations. Mirrors godlike/07 residue-accounting
// discipline for the bypass surface.
func TestScanApiModuleDepsMax8_Bypass9Fields(t *testing.T) {
	bypass := apiModuleDepsMaxBypassRelPaths[0] // upper ClipsModule w/ 34 fields
	root := testPercheckDepsRoot(t, map[string]string{
		bypass: `package clips

type Dependencies struct {
	A int
	B int
	C int
	D int
	E int
	F int
	G int
	H int
	I int
}

func Build(deps Dependencies) error { return nil }
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 0 {
		t.Fatalf("bypass-list hit should produce 0 violations; got %d: %+v", len(got), got)
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("bypass-list hit should produce 1 WARN; got %d", len(r.Warnings))
	}
	if !strings.Contains(r.Warnings[0], bypass) {
		t.Fatalf("WARN should reference the bypass path; got %q", r.Warnings[0])
	}
}

// TestScanApiModuleDepsMax8_TypeAlias verifies that
// `type Dependencies = OtherType` is NOT detected as a
// Dependencies struct (no Field count trivially). The no-shadow-
// enum companion gate covers the alias-drift contract
// separately.
func TestScanApiModuleDepsMax8_TypeAlias(t *testing.T) {
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": `package test

type Foo struct {
	A int
	B int
}

type Dependencies = Foo

func Build(deps Dependencies) error { return nil }
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 0 {
		t.Fatalf("type alias should not detect; got %d: %+v", len(got), got)
	}
}

// TestScanApiModuleDepsMax8_DepsAltName verifies that the
// alternative name `Deps` is covered (mirrors the legacy
// pattern in some pre-Card-10 module.go files).
func TestScanApiModuleDepsMax8_DepsAltName(t *testing.T) {
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": `package test

type Deps struct {
	A int
	B int
	C int
	D int
	E int
	F int
	G int
	H int
	I int
}

func Build(deps Deps) error { return nil }
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 1 {
		t.Fatalf("Deps-name 9 fields should produce 1 violation; got %d: %+v", len(got), got)
	}
	if got[0].ActualCount != 9 {
		t.Fatalf("ActualCount: got %d, want 9", got[0].ActualCount)
	}
}

// TestScanApiModuleDepsMax8_NoDeps verifies that a module.go
// without any `Dependencies` or `Deps` struct produces zero
// violations (not every API module uses the Build-pattern —
// some are pure helper rows and don't carry a typed bag).
func TestScanApiModuleDepsMax8_NoDeps(t *testing.T) {
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": `package test

type Config struct {
	A int
	B int
	C int
	D int
	E int
	F int
	G int
	H int
	I int
}

func init() {}
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 0 {
		t.Fatalf("non-Deps Config should produce zero; got %d: %+v", len(got), got)
	}
}

// TestScanApiModuleDepsMax8_ParseFail verifies that an
// irrecoverable syntax error in a module.go surfaces as a
// violations entry with MatchedRule=deps_count_parse_fail
// (operator-visible infra failure, NOT a silent pass-through).
func TestScanApiModuleDepsMax8_ParseFail(t *testing.T) {
	broken := `package test

type Dependencies struct {
	A int
	B int
	THIS IS NOT VALID GO
}
`
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": broken,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 1 {
		t.Fatalf("parse fail should surface as 1 violation; got %d: %+v", len(got), got)
	}
	if got[0].MatchedRule != "deps_count_parse_fail" {
		t.Fatalf("MatchedRule: got %q, want deps_count_parse_fail", got[0].MatchedRule)
	}
}

// TestScanApiModuleDepsMax8_EmbeddedField verifies that an
// embedded field counts as 1 (DI perspective: one injected
// fact at the module boundary, not a flattening of its underlying
// fields). Counting math (precise):
//   - `Inner` (embedded, len(Names) == 0) → 1
//   - `A, B, C, D, E, F, G, H int` (1 Field with 8 Names) → 8
//   - Total: 1 + 8 = 9 (over the 8 cap, must violate)
func TestScanApiModuleDepsMax8_EmbeddedField(t *testing.T) {
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": `package test

type Inner struct{}

type Dependencies struct {
	Inner        // embedded → 1 field
	A, B, C, D, E, F, G, H int // 8 grouped
}

func Build(deps Dependencies) error { return nil }
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 1 {
		t.Fatalf("embedded (1) + 8 grouped = 9 should produce exactly 1 violation; got %d: %+v", len(got), got)
	}
	if got[0].ActualCount != 9 {
		t.Fatalf("ActualCount: got %d, want 9", got[0].ActualCount)
	}
} // TestScanApiModuleDepsMax8_GroupedMultiDecl verifies that
// `A, B Service` counts as 2 per Field.Names (Go AST split).
// This is an explicit defence against regex over-/under-
// counting on grouped multi-decl.
//
// Counting math (precise):
//   - `A, B struct{}` → 2 named fields in 1 GroupedDecl
//   - `C, D int`      → 2 named fields in 1 GroupedDecl
//   - `E int`         → 1 named field
//   - `F int`         → 1 named field
//   - `G int`         → 1 named field
//   - `H int`         → 1 named field
//   - `I int`         → 1 named field
//   - `X`  (embedded) → 1 anonymous field
//     Total: 2 + 2 + 5 + 1 = 10 (over the 8 cap, must violate)
func TestScanApiModuleDepsMax8_GroupedMultiDecl(t *testing.T) {
	root := testPercheckDepsRoot(t, map[string]string{
		"internal/api/test/module.go": `package test

type X struct{}

type Dependencies struct {
	A, B struct{}
	C, D int
	E    int
	F    int
	G    int
	H    int
	I    int
	X
}

func Build(deps Dependencies) error { return nil }
`,
	})
	r := &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
		Warnings: []string{},
	}
	ScanApiModuleDepsMax8(root, nil, r)
	got := filterDepsMaxViolations(r)
	if len(got) != 1 {
		t.Fatalf("grouped (4) + single (5) + embedded (1) = 10 total should produce 1 violation; got %d: %+v", len(got), got)
	}
	if got[0].ActualCount != 10 {
		t.Fatalf("ActualCount: got %d, want 10", got[0].ActualCount)
	}
}

// TestCountStructFields_Unit is a focused unit test on the
// counting math alone (no file IO, no walker). Defence against
// silent over-/under-counting on grouped multi-decl +
// embedded field combinations.
func TestCountStructFields_Unit(t *testing.T) {
	src := `package test
type S struct {
	A int
	B, C int
	D int
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	st, ok := f.Decls[0].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type.(*ast.StructType)
	if !ok {
		t.Fatalf("expected struct type")
	}
	got := countStructFields(st)
	// A=1 + (B,C)=2 + D=1 = 4
	if got != 4 {
		t.Fatalf("countStructFields: got %d, want 4", got)
	}
}

// TestIsApiModuleDepsBypass verifies the bypass-list membership
// check works for both membership and non-membership paths.
func TestIsApiModuleDepsBypass(t *testing.T) {
	for _, rel := range apiModuleDepsMaxBypassRelPaths {
		if !isApiModuleDepsBypass(rel) {
			t.Errorf("bypass-list entry %q should match", rel)
		}
	}
	nonMembers := []string{
		"internal/api/assets/module.go",
		"internal/api/script/module.go",
		"internal/api/assets/clips/submodule/module.go",
	}
	for _, rel := range nonMembers {
		if isApiModuleDepsBypass(rel) {
			t.Errorf("%q should NOT be in bypass list", rel)
		}
	}
}

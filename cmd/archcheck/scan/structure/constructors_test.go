// Package scan — constructor-deps scanner tests.
//
// Black-box test for ScanConstructors:
//   - Construct 3 synthetic constructor signatures (7 / 8 / 9 params) plus
//     a method with receiver (must be skipped) and a generic type-param
//     block (must count only real params, not the [T any] block).
//   - Layout them under <tmp>/internal/synthpkg/ so ScanConstructors's
//     fixed `internal/` walk picks them up exactly like real sources.
//   - With pol.MaxConstructorDeps=8, assert ONLY the 9-deps ctor fires.
//
// Cross-references:
//   - cmd/archcheck/scan/constructors.go: the scanner under test
//   - cmd/archcheck/policy/model.go: Policy.MaxConstructorDeps knob
//   - cmd/archcheck/report/model.go: Violation fields asserted below
package structure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeCtor lays down <root>/internal/<rel>/<file>.go with the
// supplied source body. The directory layout mirrors the real
// codebase so ScanConstructors's fixed `internal/` walk picks up
// the fixtures exactly like real sources.
func writeCtor(t *testing.T, root, rel, file, body string) {
	t.Helper()
	dir := filepath.Join(root, "internal", rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	target := filepath.Join(dir, file)
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}

// minimalPolicy returns a Policy with only the constructor-deps
// knob set; everything else is zero (ScanPackages / ScanCommandBinaries
// etc. would no-op but this test only calls ScanConstructors).
func minimalPolicy(maxCtorDeps int) *policy.Policy {
	return &policy.Policy{
		MaxConstructorDeps: maxCtorDeps,
	}
}

// emptyReport returns a Report with an initialised Summary so
// ScanConstructors can append Violations without nil-map panics.
func emptyReport(pol *policy.Policy) *report.Report {
	return &report.Report{
		Mode:    "unit-test",
		Policy:  pol,
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
}

// TestScanConstructorsCap_8_7_9 — PRIMARY user-spec test.
//
// Cap = 8. Three constructors: 7 deps (under cap, no violation),
// 8 deps (exactly at cap, no violation), 9 deps (over cap, 1 violation).
func TestScanConstructorsCap_8_7_9(t *testing.T) {
	root := t.TempDir()

	// 7 deps — under cap.
	writeCtor(t, root, "synthpkg", "seven.go", `package synthpkg

func NewSeven(a, b, c, d, e, f, g int) *Seven { return nil }

// foo bar
type Seven struct{}
`)

	// 8 deps — exactly at cap.
	writeCtor(t, root, "synthpkg", "eight.go", `package synthpkg

func NewEight(a, b, c, d, e, f, g, h int) *Eight { return nil }

type Eight struct{}
`)

	// 9 deps — over cap, the only one we expect to flag.
	writeCtor(t, root, "synthpkg", "nine.go", `package synthpkg

func NewNine(a, b, c, d, e, f, g, h, i int) *Nine { return nil }

type Nine struct{}
`)

	pol := minimalPolicy(8)
	r := emptyReport(pol)
	ScanConstructors(root, pol, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("want 1 violation (from 9-deps ctor alone), got %d:\n%s",
			got, dumpViolations(r.Violations))
	}
	v := r.Violations[0]
	if v.MatchedRule != "max_constructor_deps" {
		t.Errorf("MatchedRule = %q, want %q", v.MatchedRule, "max_constructor_deps")
	}
	if v.Rule != "constructor_deps" {
		t.Errorf("Rule = %q, want %q", v.Rule, "constructor_deps")
	}
	if v.Severity != "warn" {
		t.Errorf("Severity = %q, want %q", v.Severity, "warn")
	}
	if v.ActualCount != 9 {
		t.Errorf("ActualCount = %d, want 9", v.ActualCount)
	}
	if v.AllowedCount != 8 {
		t.Errorf("AllowedCount = %d, want 8", v.AllowedCount)
	}
	if !strings.HasSuffix(v.File, "nine.go") {
		t.Errorf("File = %q, want suffix nine.go", v.File)
	}
	if !strings.Contains(v.Note, "9 parameters") || !strings.Contains(v.Note, "max 8") {
		t.Errorf("Note = %q, want substring `9 parameters` + `max 8`", v.Note)
	}
}

// TestScanConstructorsMethodSkipped — methods with receivers must NOT
// be counted (the rule family is package-level constructors only).
func TestScanConstructorsMethodSkipped(t *testing.T) {
	root := t.TempDir()

	writeCtor(t, root, "synthpkg", "method.go", `package synthpkg

type Receiver struct{}

func (r *Receiver) NewWithReceiver(a, b, c, d, e, f, g, h, i, j int) *Receiver {
	return nil
}
`)

	pol := minimalPolicy(8)
	r := emptyReport(pol)
	ScanConstructors(root, pol, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("methods with receiver must be skipped; got %d violations: %s",
			got, dumpViolations(r.Violations))
	}
}

// TestScanConstructorsGenericTypeParams — a ctor with a same-line
// `[T any]` generic block must count ONLY real params, not the type
// parameter (which lives inside brackets, not parentheses).
func TestScanConstructorsGenericTypeParams(t *testing.T) {
	root := t.TempDir()

	// 3 params in parens, plus type param block [T any].
	// Under cap (8), must NOT fire even with generic block.
	writeCtor(t, root, "synthpkg", "generic.go", `package synthpkg

type Repo[T any] struct{ v T }

func NewRepo[T any](ctx context.Context, repo T, log *Logger) *Repo[T] {
	return &Repo[T]{v: repo}
}

type Logger struct{}
`)

	pol := minimalPolicy(8)
	r := emptyReport(pol)
	ScanConstructors(root, pol, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("generic [T any] block must not confuse param count; got %d violations: %s",
			got, dumpViolations(r.Violations))
	}
}

// TestScanConstructorsMultiLineSignature — a ctor whose parameter list
// spans multiple lines must be parsed correctly (parenDepth tracks the
// balance until it closes).
func TestScanConstructorsMultiLineSignature(t *testing.T) {
	root := t.TempDir()

	// 10 params, spread over 11 lines (10 declarations + closing paren).
	// No trailing comma after the last param (Go function signatures
	// don't allow trailing commas — the scanner must not over-count a
	// stray trailing comma). Over cap (8), should fire.
	writeCtor(t, root, "synthpkg", "multiline.go", `package synthpkg

func NewMulti(
	a int,
	b int,
	c int,
	d int,
	e int,
	f int,
	g int,
	h int,
	i int,
	j int
) *Multi { return nil }

type Multi struct{}
`)

	pol := minimalPolicy(8)
	r := emptyReport(pol)
	ScanConstructors(root, pol, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("multi-line 10-deps ctor over cap=8 should fire once; got %d violations: %s",
			got, dumpViolations(r.Violations))
	}
	if r.Violations[0].ActualCount != 10 {
		t.Errorf("ActualCount = %d, want 10", r.Violations[0].ActualCount)
	}
}

// TestScanConstructorsOptOut — when MaxConstructorDeps <= 0 the scanner
// must be a no-op (the policy opts out of the rule family).
func TestScanConstructorsOptOut(t *testing.T) {
	root := t.TempDir()

	writeCtor(t, root, "synthpkg", "many.go", `package synthpkg

func NewMany(a, b, c, d, e, f, g, h, i, j, k, l int) *Many { return nil }
type Many struct{}
`)

	pol := minimalPolicy(0)
	r := emptyReport(pol)
	ScanConstructors(root, pol, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("MaxConstructorDeps=0 must opt out; got %d violations", got)
	}
}

// dumpViolations returns a human-readable dump of the violations slice
// for failure messages — keeps the failing test output actionable.
func dumpViolations(vs []report.Violation) string {
	var b strings.Builder
	for i, v := range vs {
		b.WriteString("  [")
		b.WriteString(string(rune('0' + i)))
		b.WriteString("] ")
		b.WriteString(v.File)
		b.WriteString(": ")
		b.WriteString(v.Note)
		b.WriteString("\n")
	}
	return b.String()
}

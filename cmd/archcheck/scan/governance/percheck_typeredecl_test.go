package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFileFixture is a tiny helper that writes content to a
// path relative to the temp dir, creating any missing parent
// directories. Test files use it to lay out minimal internal/
// trees without dragging in real project fixtures.
func writeFileFixture(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// TestScanTypeRedeclarations_NoDuplicates is the negative case:
// a clean tree with no duplicate types produces zero violations.
// The seed tree is two packages (foo + bar) each declaring a
// distinct type — no collision.
func TestScanTypeRedeclarations_NoDuplicates(t *testing.T) {
	root := t.TempDir()
	writeFileFixture(t, root, "internal/foo/types.go", `package foo

type Alpha struct{}
type Beta struct{}
`)
	writeFileFixture(t, root, "internal/bar/types.go", `package bar

type Alpha struct{}
`)
	r := &report.Report{Violations: nil}
	ScanTypeRedeclarations(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("clean tree should produce 0 violations, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTypeRedeclarations_SamePackageDuplicates is the load-bearing
// positive case: two files in the same Go package declaring the same
// exported type must produce ONE violation (per (pkg, type) pair, not
// per file). Mirrors the shell check's `(count=N in same package)`
// diagnostic shape.
func TestScanTypeRedeclarations_SamePackageDuplicates(t *testing.T) {
	root := t.TempDir()
	writeFileFixture(t, root, "internal/foo/a.go", `package foo

type Dup struct {
	A int
}
`)
	writeFileFixture(t, root, "internal/foo/b.go", `package foo

type Dup struct {
	B string
}
`)
	r := &report.Report{}
	ScanTypeRedeclarations(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 {
		t.Fatalf("expected 1 violation for 2-file redeclaration, got %d: %+v", len(r.Violations), r.Violations)
	}
	v := r.Violations[0]
	if v.Rule != "percheck_type_redecl" {
		t.Errorf("Rule = %q, want percheck_type_redecl", v.Rule)
	}
	if v.Severity != string(report.SeverityError) {
		t.Errorf("Severity = %q, want %q", v.Severity, report.SeverityError)
	}
	if !strings.Contains(v.Note, "foo.Dup") {
		t.Errorf("Note should mention foo.Dup, got %q", v.Note)
	}
	if !strings.Contains(v.Note, "count=2") {
		t.Errorf("Note should report count=2, got %q", v.Note)
	}
	// Both file sites must appear in the diagnostic.
	if !strings.Contains(v.Note, "a.go") || !strings.Contains(v.Note, "b.go") {
		t.Errorf("Note should reference both a.go and b.go, got %q", v.Note)
	}
}

// TestScanTypeRedeclarations_AllowlistRespected pins the canonical
// godlike/08 zero-baseline rule semantics: a (pkg, TypeName) pair
// listed in the allowlist is exempt. The allowlist path is
// docs/migrations/duplicate-types-allowlist.txt (matches
// the shell check's `if [ -f ... ]` guard).
func TestScanTypeRedeclarations_AllowlistRespected(t *testing.T) {
	root := t.TempDir()
	writeFileFixture(t, root, "internal/foo/a.go", `package foo

type Dup struct{ A int }
`)
	writeFileFixture(t, root, "internal/foo/b.go", `package foo

type Dup struct{ B string }
`)
	// Seed the allowlist with the (pkg, type) pair.
	writeFileFixture(t, root, "docs/migrations/duplicate-types-allowlist.txt",
		"foo:Dup   # transitional — owner: @test, deadline: 2026-09-01\n")
	r := &report.Report{}
	ScanTypeRedeclarations(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("allowlisted (foo, Dup) should produce 0 violations, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTypeRedeclarations_CrossPackageSameName is the negative
// sibling: same type name in different packages is NOT a violation.
// The shell awk's `same-package` group-by is package-name-keyed, so
// distinct packages produce distinct keys.
func TestScanTypeRedeclarations_CrossPackageSameName(t *testing.T) {
	root := t.TempDir()
	writeFileFixture(t, root, "internal/foo/types.go", `package foo

type Dup struct{ A int }
`)
	writeFileFixture(t, root, "internal/bar/types.go", `package bar

type Dup struct{ B string }
`)
	r := &report.Report{}
	ScanTypeRedeclarations(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("same-name cross-package types should produce 0 violations, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTypeRedeclarations_SkipsTestFiles pins the test-file
// exclusion: a duplicate type declared in a *_test.go file is
// not a production violation. Mirrors the shell check's
// --glob '!**/*_test.go' allowlist.
func TestScanTypeRedeclarations_SkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	writeFileFixture(t, root, "internal/foo/types.go", `package foo

type Dup struct{ A int }
`)
	writeFileFixture(t, root, "internal/foo/dup_test.go", `package foo

type Dup struct{ B int } // test-only redeclaration
`)
	r := &report.Report{}
	ScanTypeRedeclarations(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("test-file-only redeclaration should produce 0 violations, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTypeRedeclarations_TypeAlias pins the type-alias case:
// `type X = Y` is captured under the alias name (X), matching the
// shell awk regex `^type[[:space:]]+[A-Z]`. Two alias declarations
// of the same name in the same package produce a violation.
func TestScanTypeRedeclarations_TypeAlias(t *testing.T) {
	root := t.TempDir()
	writeFileFixture(t, root, "internal/foo/a.go", `package foo

type Dup = int
`)
	writeFileFixture(t, root, "internal/foo/b.go", `package foo

type Dup = string
`)
	r := &report.Report{}
	ScanTypeRedeclarations(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 {
		t.Fatalf("expected 1 violation for 2 alias redeclarations, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTypeRedeclarations_NonExportedSkipped pins the
// exported-only filter: lowercase (unexported) types are
// skipped per the shell awk regex `^type[[:space:]]+[A-Z]`
// (capital-letter initial = exported).
func TestScanTypeRedeclarations_NonExportedSkipped(t *testing.T) {
	root := t.TempDir()
	writeFileFixture(t, root, "internal/foo/a.go", `package foo

type dup struct{ A int } // unexported
`)
	writeFileFixture(t, root, "internal/foo/b.go", `package foo

type dup struct{ B int } // unexported
`)
	r := &report.Report{}
	ScanTypeRedeclarations(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("unexported duplicate types should produce 0 violations, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTypeRedeclarations_LoadAllowlistMissing pins the
// missing-allowlist fallback: when the allowlist file is not
// present, the scanner treats it as zero entries (matches the
// shell `if [ -f ... ]` guard). A redeclaration in this case
// still surfaces as a violation.
func TestScanTypeRedeclarations_LoadAllowlistMissing(t *testing.T) {
	root := t.TempDir()
	// No allowlist file. Only the internal/ tree.
	writeFileFixture(t, root, "internal/foo/a.go", `package foo

type Dup struct{ A int }
`)
	writeFileFixture(t, root, "internal/foo/b.go", `package foo

type Dup struct{ B int }
`)
	r := &report.Report{}
	ScanTypeRedeclarations(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 {
		t.Fatalf("missing allowlist should not suppress violations; got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestLoadDuplicateTypesAllowlist pins the parser contract:
// comment lines are skipped, blank lines are skipped, and the
// first whitespace-separated token is consumed verbatim. A
// missing file returns an empty map (no exceptions).
func TestLoadDuplicateTypesAllowlist(t *testing.T) {
	t.Run("missing file returns empty set", func(t *testing.T) {
		root := t.TempDir()
		got := loadDuplicateTypesAllowlist(root)
		if len(got) != 0 {
			t.Errorf("missing allowlist should return empty set, got %v", got)
		}
	})
	t.Run("parses first whitespace token per line", func(t *testing.T) {
		root := t.TempDir()
		writeFileFixture(t, root, "docs/migrations/duplicate-types-allowlist.txt",
			"# header comment\n"+
				"\n"+
				"foo:Alpha   # rationale + owner + deadline\n"+
				"bar:Beta\n"+
				"  baz:Gamma  \n", // leading whitespace + trailing whitespace
		)
		got := loadDuplicateTypesAllowlist(root)
		if len(got) != 3 {
			t.Fatalf("expected 3 entries, got %d: %v", len(got), got)
		}
		for _, k := range []string{"foo:Alpha", "bar:Beta", "baz:Gamma"} {
			if !got[k] {
				t.Errorf("allowlist missing key %q; got %v", k, got)
			}
		}
	})
}

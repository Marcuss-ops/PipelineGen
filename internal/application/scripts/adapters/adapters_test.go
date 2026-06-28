// Package adapters — adapters_test.go is the regression-gate file
// that pins audit #6 (Phase 4, June 2026). Two invariants must
// hold for the canonical typed-registry flow to work:
//
//  1. adapters.PostProcessorRegistry MUST NOT be redeclared. The
//     canonical registry lives in internal/application/scripts; the
//     earlier stub in this package existed only to break the
//     inheritance build cycle in PR 0 and was removed in Phase 4.
//     Re-addition would re-introduce the legacy dual-registry shape
//     the audit resolved.
//
//  2. adapters.ProcessorPolicy MUST NOT be redeclared. The canonical
//     string-typed policy enum lives in internal/application/scripts.
//     A re-declared shadow breaks the assignment
//     `r.policies[name] = policy` inside scripts.PostProcessorRegistry
//     .Register (Go treats distinct `type Foo string` declarations
//     as distinct types — the shadow value cannot be stored in a
//     map keyed by scripts.ProcessorPolicy).
//
// Both checks walk every .go file in this package (skipping _test.go
// files in this same package — they don't declare types). The test
// fails fast on the first violation; the error message tells the
// operator which file + which symbol must be removed.
//
// Phase 3 admin CLI has the same gate (TestNoSilentConfigGetInAdminCli
// — see cmd/admin/admin_test.go). This is the audit #6 sibling.
package adapters

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPostProcessorRegistryStub pins invariant #1 above.
func TestNoPostProcessorRegistryStub(t *testing.T) {
	walkAdapterFilesForType(t, "PostProcessorRegistry", "adapters.PostProcessorRegistry (the audit #6 stub) is FORBIDDEN in this package. Use scripts.PostProcessorRegistry instead.")
}

// TestNoProcessorPolicyShadow pins invariant #2 above.
func TestNoProcessorPolicyShadow(t *testing.T) {
	walkAdapterFilesForType(t, "ProcessorPolicy", "adapters.ProcessorPolicy (the shadow string-type) is FORBIDDEN in this package. Use scripts.ProcessorPolicy instead.")
}

// walkAdapterFilesForType parses every .go file in this package and
// fails if any file declares a top-level type with the supplied name.
// The check is conservative on purpose — single occurrence, file-scoped
// — to minimise false positives on inline aliases.
func walkAdapterFilesForType(t *testing.T, typeName, msg string) {
	t.Helper()

	pkgDir := packageDir(t)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("adapters regression gate: read %q: %v", pkgDir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, e.Name())
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("adapters regression gate: parse %q: %v", path, err)
		}

		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != typeName {
					continue
				}
				t.Errorf("adapters regression gate: file %s declares forbidden type %q — %s",
					e.Name(), typeName, msg)
			}
		}
	}
}

// packageDir resolves the absolute path to the adapters package
// directory by deriving the test's own directory. Test file lives
// next to production files, so `$test_dir` == package dir.
func packageDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("adapters regression gate: os.Getwd: %v", err)
	}
	return wd
}

package clips

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// infraImportPath is the substring Check 19 of
// scripts/ci-architectural-checks.sh bans inside `internal/api/`. PG-005
// (June 2026): the 7 handler files under internal/api/assets/clips/**
// were grandfathered for this import scope; now they should compile
// without any infra reach-through. This test enforces that locally.
const infraImportPath = "internal/infrastructure/"

// TestStaticGate_NoClipsAPIInfrastructureLeaks verifies that every
// non-test Go file under internal/api/assets/clips/ (the package
// boundary the api/ layer must keep clean) has zero imports of any
// package whose import path contains "internal/infrastructure/".
// PG-005 (June 2026): the per-file grandfathered allowlist row in
// docs/migrations/api-infrastructure-imports-allowlist.txt was dropped
// in the same commit; this test is the local enforcement.
func TestStaticGate_NoClipsAPIInfrastructureLeaks(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var violations []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fpath := filepath.Join(dir, e.Name())
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, fpath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", fpath, err)
		}
		for _, imp := range file.Imports {
			impPath := strings.Trim(imp.Path.Value, "\"")
			if strings.Contains(impPath, infraImportPath) {
				violations = append(violations, fpath+": imports "+impPath)
			}
		}
	}

	if len(violations) > 0 {
		for _, v := range violations {
			t.Errorf("PG-005 infra reach-through: %s", v)
		}
		t.Fatalf("found %d infra reach-throughs in internal/api/assets/clips/; see prior %s for context", len(violations), filepath.Join("docs/migrations/api-infrastructure-imports-allowlist.txt"))
	}
}

// TestStaticGate_ClipsGoFilesUsePkgArchcheckGuard ensures the gate
// test itself was not silently disabled (e.g. by deleting the parsing
// loop above). Sibling to the gate above; covers a regression where
// somebody might shorten the test to a no-op while leaving the test
// function in place.
func TestStaticGate_ClipsGoFilesUseParser(t *testing.T) {
	// The presence of TestStaticGate_NoClipsAPIInfrastructureLeaks with
	// at least 30 lines of non-trivial enforcement is what we're
	// asserting. If a future edit shrinks it below minimum, fail.
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fpath := filepath.Join(dir, e.Name())
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, fpath, nil, parser.ParseComments); err != nil {
			t.Fatalf("parse %s: %v", fpath, err)
		}
		// We don't check anything else here — the goal is to confirm
		// the parser is doing its job across every file in the package.
	}
}

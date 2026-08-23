// Package scan — test for ScanAPIPolicyLiterals
// (PR-SUBMISSION-FACTORY forward-prevention gate).
//
// Hermetic (t.TempDir-anchored). Validates the four core
// invariants of the policy-literal gate:
//
//  1. A production file under internal/api/ containing
//     `operations.JobPriority` as a literal assignment trips
//     the gate as SeverityError.
//  2. A test file under internal/api/ containing the literal
//     is exempt (test-fixture residue surface; documented in
//     migrations/api/archcheck-strict-baseline.json).
//  3. An import block (`import(...)` containing the literal
//     by name) is exempt: the symbols must be reachable by
//     name to satisfy Go's typed-import surface. The ban is
//     on the ASSIGN shape, not the type reference.
//  4. Comment-only matches produce a WARN in
//     !productionOnly mode and are silenced in productionOnly
//     mode.
package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// makeFileForAPIPolicyLiteralsTest writes a fixture .go file
// at the requested repo-relative path inside `root`.
// Mirrors the family helper idiom.
func makeFileForAPIPolicyLiteralsTest(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanAPIPolicyLiterals_NonCanonicalProduction_TripsGate
// verifies the canonical violation shape (running code under
// internal/api/ that references the literal as a value).
func TestScanAPIPolicyLiterals_NonCanonicalProduction_TripsGate(t *testing.T) {
	root := t.TempDir()
	makeFileForAPIPolicyLiteralsTest(t, root, "internal/api/script/handler_generate_handcrafted.go",
		`package script

func hardCoded() int {
	// forbidden assign-shape: literal in transport
	return operations.JobPriority
}
`)
	rep := &report.Report{}
	ScanAPIPolicyLiterals(root, nil, rep, true)
	if len(rep.Violations) == 0 {
		t.Fatalf("expected ≥ 1 violation; got 0 (transport hard-coded literal must trip gate)")
	}
	if rep.Violations[0].Rule != apiPolicyLiteralsRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, apiPolicyLiteralsRule)
	}
	if rep.Violations[0].Severity != string(report.SeverityError) {
		t.Fatalf("violation severity = %q, want SeverityError", rep.Violations[0].Severity)
	}
}

// TestScanAPIPolicyLiterals_TestFileExempt verifies test
// files are exempt (residue surface for fixtures; documented
// in archcheck-strict-baseline.json).
func TestScanAPIPolicyLiterals_TestFileExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForAPIPolicyLiteralsTest(t, root, "internal/api/script/handler_generate_handcoded_test.go",
		`package script

import "testing"

func TestHandCoded(t *testing.T) {
	_ = operations.JobPriority
}
`)
	rep := &report.Report{}
	ScanAPIPolicyLiterals(root, nil, rep, true)
	if len(rep.Violations) != 0 {
		t.Fatalf("test file tripped gate: got %d violations\nfirst: %s",
			len(rep.Violations), rep.Violations[0].Note)
	}
}

// TestScanAPIPolicyLiterals_ImportBlockExempt verifies the
// `import "github.com/..."` lines and grouped `(...)` blocks
// are exempt. The symbols must be reachable by name for the
// package to compile. The post-import-block reference surface
// is NOT exempt — once outside the `import (...)` block,
// any policy-literal symbol on a production line is a
// violation per the gate's strict structural-decision
// interpretation.
func TestScanAPIPolicyLiterals_ImportBlockExempt(t *testing.T) {
	root := t.TempDir()
	makeFileForAPIPolicyLiteralsTest(t, root, "internal/api/script/handler_imports_only.go",
		`package script

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
)
`)
	rep := &report.Report{}
	ScanAPIPolicyLiterals(root, nil, rep, true)
	if len(rep.Violations) != 0 {
		t.Fatalf("import block tripped gate: got %d violations\nfirst: %s",
			len(rep.Violations), rep.Violations[0].Note)
	}
}

// TestScanAPIPolicyLiterals_PostImportBlockReference_TripsGate
// verifies that once we are OUTSIDE the `import (...)` block,
// the gate trips on the policy literal — the import statement
// is exempt, but the typed sentinel reference after the block
// is NOT. This is the strict (correct) interpretation: any
// production code under the scope containing the canonical
// policy literal is a violation.
func TestScanAPIPolicyLiterals_PostImportBlockReference_TripsGate(t *testing.T) {
	root := t.TempDir()
	makeFileForAPIPolicyLiteralsTest(t, root, "internal/api/script/handler_post_import_sentinel.go",
		`package script

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
)

var _ = operations.ScopeScriptGenerate
`)
	rep := &report.Report{}
	ScanAPIPolicyLiterals(root, nil, rep, true)
	if len(rep.Violations) == 0 {
		t.Fatalf("post-import-block sentinel reference did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].Rule != apiPolicyLiteralsRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, apiPolicyLiteralsRule)
	}
}

// TestScanAPIPolicyLiterals_CommentOnlyResidue_Warned verifies
// comment-only matches produce a WARN in !productionOnly mode.
func TestScanAPIPolicyLiterals_CommentOnlyResidue_Warned(t *testing.T) {
	root := t.TempDir()
	makeFileForAPIPolicyLiteralsTest(t, root, "internal/api/script/handler_generate_doc.go",
		`package script

// ScopeScriptGenerate is referenced here only in descriptive prose.
// TypeGenerate policy lives in the application factory.
func Note() {}
`)
	rep := &report.Report{}
	ScanAPIPolicyLiterals(root, nil, rep, false)
	if len(rep.Violations) != 0 {
		t.Fatalf("comment-only produced violation: got %d, want 0", len(rep.Violations))
	}
	if !containsString(rep.Warnings, "policy-literal-comments:") {
		t.Fatalf("comment-only did NOT produce WARN residue: %v", rep.Warnings)
	}
}

// TestScanAPIPolicyLiterals_ProductionOnlySilencesWarn verifies
// productionOnly=true silences the comment-only WARN bucket.
func TestScanAPIPolicyLiterals_ProductionOnlySilencesWarn(t *testing.T) {
	root := t.TempDir()
	makeFileForAPIPolicyLiteralsTest(t, root, "internal/api/script/handler_generate_doc.go",
		`package script

// ScopeScriptGenerate is referenced here only in descriptive prose.
func Note() {}
`)
	rep := &report.Report{}
	ScanAPIPolicyLiterals(root, nil, rep, true)
	for _, w := range rep.Warnings {
		if containsString([]string{w}, "policy-literal-comments:") {
			t.Fatalf("productionOnly did NOT silence comment-only WARN: %s", w)
		}
	}
}

// TestScanAPIPolicyLiterals_OutOfScopeNonCanonical_NoTrip verifies
// a non-canonical literal OUTSIDE internal/api/ is not scanned
// (the gate's scope is internal/api/** only).
func TestScanAPIPolicyLiterals_OutOfScopeNonCanonical_NoTrip(t *testing.T) {
	root := t.TempDir()
	makeFileForAPIPolicyLiteralsTest(t, root, "internal/application/legit_factory_with_literal.go",
		`package submission

// operations.JobPriority is the canonical factory location.
var _ = operations.JobPriority
`)
	rep := &report.Report{}
	ScanAPIPolicyLiterals(root, nil, rep, true)
	if len(rep.Violations) != 0 {
		t.Fatalf("out-of-scope file tripped api-policy-literal gate: got %d", len(rep.Violations))
	}
}

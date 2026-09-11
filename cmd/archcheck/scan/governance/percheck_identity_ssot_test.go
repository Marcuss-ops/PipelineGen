// Package scan — percheck_identity_ssot_test.go
//
// Pins the GODLIKE/06 "one owner per fact" gate for canonical identity
// literals (outbox event names, envelope schema versions, shared job types).
//
// The load-bearing assertions are:
//   - a re-declaration outside the owner package trips the gate;
//   - a declaration inside the owner package does not;
//   - usages (comparisons / struct fields) are not declarations;
//   - the vocabulary is read from the owner registries, never copied.
//
// godlike/07 fail-fast: every fixture lives in t.TempDir(); no production
// file is touched at test time.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func writeIdentityFixture(t *testing.T, tempDir, relPath, body string) string {
	t.Helper()
	dir := filepath.Join(tempDir, filepath.Dir(relPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	path := filepath.Join(tempDir, relPath)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func newIdentityReport() *report.Report {
	return &report.Report{
		Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}
}

// TestScanIdentitySSOT_RuleIdStable pins the rule id so a rename surfaces as
// a loud failure against checks.go::CheckSpec.Name.
func TestScanIdentitySSOT_RuleIdStable(t *testing.T) {
	const want = "percheck_identity_ssot"
	if identitySSOTRule != want {
		t.Errorf("identitySSOTRule = %q, want %q (checks.go CheckSpec.Name lockstep)", identitySSOTRule, want)
	}
}

// TestScanIdentitySSOT_OwnerDeclarationPasses verifies the owner package may
// declare its own literal.
func TestScanIdentitySSOT_OwnerDeclarationPasses(t *testing.T) {
	tempDir := t.TempDir()
	eventLit := event.AssetIndexRequested
	writeIdentityFixture(t, tempDir, "internal/kernel/event/identities.go",
		"package event\n\nconst AssetIndexRequested = \""+eventLit+"\"\n")

	r := newIdentityReport()
	ScanIdentitySSOT(tempDir, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("owner package MUST be exempt; got %d violations: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanIdentitySSOT_DirtyEventRedeclarationFails is the load-bearing
// forward-prevention assertion for event identities: re-declaring
// asset.index.requested outside internal/kernel/event MUST trip.
func TestScanIdentitySSOT_DirtyEventRedeclarationFails(t *testing.T) {
	tempDir := t.TempDir()
	writeIdentityFixture(t, tempDir, "internal/platform/sqlite/outboxevents/registry.go",
		"package outboxevents\n\nconst EventAssetIndexRequested = \""+event.AssetIndexRequested+"\"\n")

	r := newIdentityReport()
	ScanIdentitySSOT(tempDir, &policy.Policy{}, r)

	found := 0
	for _, v := range r.Violations {
		if v.Rule != identitySSOTRule {
			continue
		}
		found++
		if v.MatchedRule != "identity_literal_redeclared_outside_owner" {
			t.Errorf("MatchedRule = %q, want identity_literal_redeclared_outside_owner", v.MatchedRule)
		}
		if !strings.Contains(v.Note, "internal/kernel/event") {
			t.Errorf("Note must name the owner package; got %q", v.Note)
		}
		if !strings.Contains(v.Note, event.AssetIndexRequested) {
			t.Errorf("Note must include the literal; got %q", v.Note)
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 violation; got %d (%+v)", found, r.Violations)
	}
}

// TestScanIdentitySSOT_DirtyJobTypeRedeclarationFails covers the second
// vocabulary: a shared job type re-declared outside internal/kernel/job.
func TestScanIdentitySSOT_DirtyJobTypeRedeclarationFails(t *testing.T) {
	tempDir := t.TempDir()
	writeIdentityFixture(t, tempDir, "internal/capabilities/scripts/job_types.go",
		"package scripts\n\nconst TypeGenerate = \""+job.TypeScriptGenerate+"\"\n")

	r := newIdentityReport()
	ScanIdentitySSOT(tempDir, &policy.Policy{}, r)

	found := 0
	for _, v := range r.Violations {
		if v.Rule == identitySSOTRule {
			found++
			if !strings.Contains(v.Note, "internal/kernel/job") {
				t.Errorf("Note must name the job owner; got %q", v.Note)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly 1 violation; got %d (%+v)", found, r.Violations)
	}
}

// TestScanIdentitySSOT_TypedDeclarationFails pins that a typed const
// (`Name Type = "literal"`) is still a declaration.
func TestScanIdentitySSOT_TypedDeclarationFails(t *testing.T) {
	tempDir := t.TempDir()
	writeIdentityFixture(t, tempDir, "internal/capabilities/operations/types.go",
		"package operations\n\ntype Scope string\n\nconst ScopeScriptGenerate Scope = \""+job.TypeScriptGenerate+"\"\n")

	r := newIdentityReport()
	ScanIdentitySSOT(tempDir, &policy.Policy{}, r)

	if len(r.Violations) != 1 {
		t.Errorf("typed declaration MUST trip; got %d (%+v)", len(r.Violations), r.Violations)
	}
}

// TestScanIdentitySSOT_UsageIsNotDeclaration pins declaration scoping: a
// comparison and a struct field are consumers, not re-declarations.
func TestScanIdentitySSOT_UsageIsNotDeclaration(t *testing.T) {
	tempDir := t.TempDir()
	body := "package handler\n\n" +
		"type envelope struct {\n" +
		"\tSchemaVersion string\n" +
		"}\n\n" +
		"func isIndexEvent(eventType string) bool {\n" +
		"\tif eventType == \"" + event.AssetIndexRequested + "\" {\n" +
		"\t\treturn true\n" +
		"\t}\n" +
		"\treturn env.SchemaVersion == \"" + event.AssetIndexRequestedV1Schema + "\"\n" +
		"}\n"
	writeIdentityFixture(t, tempDir, "internal/capabilities/jobs/indexing.go", body)

	r := newIdentityReport()
	ScanIdentitySSOT(tempDir, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == identitySSOTRule {
			t.Errorf("usages MUST NOT trip the declaration gate; got %s:%d %s", v.File, v.Line, v.Note)
		}
	}
}

// TestScanIdentitySSOT_TestFileExempt verifies the regression-guard
// allowlist: test fixtures legitimately pin wire strings.
func TestScanIdentitySSOT_TestFileExempt(t *testing.T) {
	tempDir := t.TempDir()
	writeIdentityFixture(t, tempDir, "internal/capabilities/jobs/indexing_test.go",
		"package jobs\n\nconst pinnedWire = \""+event.AssetIndexRequested+"\"\n")

	r := newIdentityReport()
	ScanIdentitySSOT(tempDir, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("_test.go MUST be exempt; got %+v", r.Violations)
	}
}

// TestScanIdentitySSOT_ScannerSourceExempt verifies the scanner package can
// reference literals as documentation.
func TestScanIdentitySSOT_ScannerSourceExempt(t *testing.T) {
	tempDir := t.TempDir()
	writeIdentityFixture(t, tempDir, policy.ScannerSourcePrefix+"/doc.go",
		"package scan\n\nconst documented = \""+event.AssetIndexRequested+"\"\n")

	r := newIdentityReport()
	ScanIdentitySSOT(tempDir, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("scanner source MUST be exempt; got %+v", r.Violations)
	}
}

// TestIdentityOwnersIsDataDriven pins the architectural invariant: the gate's
// vocabulary resolves entirely from the owner registries, so every registry
// entry has an owner and no literal is hardcoded in the scanner.
func TestIdentityOwnersIsDataDriven(t *testing.T) {
	owners := identityOwners()
	if len(owners) < len(event.Canonical()) {
		t.Fatalf("event registry entries missing from owner map: have %d, want >= %d", len(owners), len(event.Canonical()))
	}
	for _, id := range event.Canonical() {
		if got, ok := owners[id.Literal]; !ok || got.Owner != identitySSOTOwnerEvent {
			t.Errorf("event identity %s (%s) not mapped to owner %s (got %+v)", id.Const, id.Literal, identitySSOTOwnerEvent, got)
		}
	}
	for _, id := range job.CanonicalTypeIdentities() {
		if got, ok := owners[id.Literal]; !ok || got.Owner != identitySSOTOwnerJob {
			t.Errorf("job identity %s (%s) not mapped to owner %s (got %+v)", id.Const, id.Literal, identitySSOTOwnerJob, got)
		}
	}
}

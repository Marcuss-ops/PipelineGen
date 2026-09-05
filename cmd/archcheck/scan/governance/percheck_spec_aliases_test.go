// Package scan — hermetic TDD coverage for the spec_aliases
// territory gate (percheck_spec_aliases).
//
// percheck_spec_aliases_test.go exercises the spec_aliases.go
// filename gate via synthetic file fixtures in t.TempDir. The
// tests are decoupled from the real repo state so a future
// refactor of the approved-directory list surfaces as a test
// failure here BEFORE it would land in production.
//
// Coverage matrix:
//
//  1. Approved territory files are NOT flagged (generated/ and
//     retrieved/ are the two canonical homes).
//
//  2. Test files named spec_aliases.go are NOT flagged
//     (regression guards legitimately create spec_aliases.go
//     fixtures for invariant pinning).
//
//  3. Production file named spec_aliases.go in an unapproved
//     territory IS flagged (forward-prevention: catches the
//     "I copy-pasted spec_aliases.go into a new module"
//     anti-pattern before it lands in production).
//
//  4. Clean run with no spec_aliases.go files produces zero
//     violations and zero warnings.
//
//  5. Mixed repo layout: realistic scenario with the two
//     canonical approved files + a test fixture + an
//     unapproved drift file. Only the drift is flagged.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFixtureSpecAliases writes a synthetic file at the given
// repo-relative path inside root, creating parent directories
// as needed. Returns the absolute file path. Mirrors the
// writeFixture pattern from percheck_player_client_test.go +
// percheck_script_docs_route_test.go.
func writeFixtureSpecAliases(t *testing.T, root, relPath, content string) string {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", abs, err)
	}
	return abs
}

// newEmptyReportSpecAliases returns a freshly-initialised Report
// with canonical non-nil summary maps. Mirrors newEmptyReport
// from percheck_player_client_test.go.
func newEmptyReportSpecAliases() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

// TestScanSpecAliases_ApprovedTerritoryIsNotFlagged is the
// load-bearing exemption test: the two canonical approved
// directories MUST be able to host spec_aliases.go without
// the gate flagging them.
func TestScanSpecAliases_ApprovedTerritoryIsNotFlagged(t *testing.T) {
	root := t.TempDir()

	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/generated/spec_aliases.go",
		`package generated
type Provider = GenerationProvider
`)
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/retrieved/spec_aliases.go",
		`package retrieved
type Provider = RetrievalProvider
`)
	// Also add a non-spec_aliases.go file in an approved dir to
	// verify the walker doesn't get confused.
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/generated/provider.go",
		`package generated
type GenerationProvider interface { Generate() error }
`)

	r := newEmptyReportSpecAliases()
	ScanSpecAliasesTerritory(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("approved territory files MUST NOT be flagged; got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanSpecAliases_TestFileSuffixIsExcludedByBasenameGate pins
// the natural exclusion: a file named `spec_aliases_test.go` has
// a different basename than `spec_aliases.go`, so the basename
// gate already excludes it without needing a separate suffix check.
// The scanner only acts on files whose basename is literally
// `spec_aliases.go` — `_test.go` variants are invisible.
func TestScanSpecAliases_TestFileSuffixIsExcludedByBasenameGate(t *testing.T) {
	root := t.TempDir()

	// A test-filename variant in an unapproved directory — the
	// basename gate (specAliasesFilename = "spec_aliases.go") already
	// excludes this file. No separate _test.go suffix check is needed.
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/bar/spec_aliases_test.go",
		`package bar_test
import "testing"
func TestSpecAliases(t *testing.T) {}
`)

	r := newEmptyReportSpecAliases()
	ScanSpecAliasesTerritory(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("_test.go variant MUST NOT be flagged (excluded by basename gate); got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanSpecAliases_UnapprovedTerritoryIsFlagged is the
// load-bearing forward-prevention test: a production
// spec_aliases.go file in an unapproved directory MUST be
// flagged.
func TestScanSpecAliases_UnapprovedTerritoryIsFlagged(t *testing.T) {
	root := t.TempDir()

	relPath := "internal/capabilities/voiceover/service/spec_aliases.go"
	writeFixtureSpecAliases(t, root, relPath,
		`package voiceover
// Drift: spec_aliases.go copy-pasted into the voiceover module.
type VoiceSpec struct{}
`)

	r := newEmptyReportSpecAliases()
	ScanSpecAliasesTerritory(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("unapproved spec_aliases.go MUST be flagged exactly once; got %d violation(s): %+v", got, r.Violations)
	}
	v := r.Violations[0]
	if v.File != relPath {
		t.Errorf("violation file = %q, want %q", v.File, relPath)
	}
	if v.Rule != "percheck_spec_aliases" {
		t.Errorf("violation rule = %q, want percheck_spec_aliases", v.Rule)
	}
	if v.Severity != string(report.SeverityError) {
		t.Errorf("violation severity = %q, want error (forward-prevention gate)", v.Severity)
	}
	if v.MatchedRule != "spec_aliases_territory_gate" {
		t.Errorf("violation matched_rule = %q, want spec_aliases_territory_gate", v.MatchedRule)
	}
	if !strings.Contains(v.Note, "approved territories") {
		t.Errorf("violation Note must reference the approved territories concept; got: %s", v.Note)
	}
	if !strings.Contains(v.Note, "internal/capabilities/images/search/") {
		t.Errorf("violation Note must reference the canonical retrieval spec surface directory; got: %s", v.Note)
	}
	if !strings.Contains(v.Note, "PR-AUDIT-8") {
		t.Errorf("violation Note must reference PR-AUDIT-8 for historical context; got: %s", v.Note)
	}
}

// TestScanSpecAliases_CleanRunHasNoViolations is the negative
// baseline: a repo with no spec_aliases.go files (outside
// approved territories) must produce zero violations.
func TestScanSpecAliases_CleanRunHasNoViolations(t *testing.T) {
	root := t.TempDir()

	// Only add the two approved files.
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/generated/spec_aliases.go",
		`package generated
type Foo = Bar
`)
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/retrieved/spec_aliases.go",
		`package retrieved
type Baz = Qux
`)
	// Some unrelated production files.
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/foo/service.go",
		`package foo
func Do() {}
`)

	r := newEmptyReportSpecAliases()
	ScanSpecAliasesTerritory(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("clean run MUST NOT flag approved files; got %d violation(s): %+v", got, r.Violations)
	}
}

// TestScanSpecAliases_MixedRepoLayout exercises the realistic
// case: a synthetic repo with the two canonical approved files,
// a test-file spec_aliases.go fixture, and an unapproved drift
// spec_aliases.go. Only the drift should be flagged.
func TestScanSpecAliases_MixedRepoLayout(t *testing.T) {
	root := t.TempDir()

	// (1) Approved generated/ spec_aliases.go — silent.
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/generated/spec_aliases.go",
		`package generated
type GenSpec struct{}
`)

	// (2) Approved retrieved/ spec_aliases.go — silent.
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/retrieved/spec_aliases.go",
		`package retrieved
type RetSpec struct{}
`)

	// (3) Test-file spec_aliases_test.go — silent (suffix exemption).
	writeFixtureSpecAliases(t, root, "internal/capabilities/images/workflow/generated/spec_aliases_test.go",
		`package generated_test
import "testing"
func TestX(t *testing.T) {}
`)

	// (4) Unapproved drift — THE violation.
	driftRelPath := "internal/capabilities/voiceover/service/spec_aliases.go"
	writeFixtureSpecAliases(t, root, driftRelPath,
		`package voiceover
// Drift: spec_aliases.go copy-pasted into the voiceover module.
type VoiceSpec struct{}
`)

	// (5) Regular file — irrelevant.
	writeFixtureSpecAliases(t, root, "internal/capabilities/voiceover/service/service.go",
		`package voiceover
func Do() {}
`)

	r := newEmptyReportSpecAliases()
	ScanSpecAliasesTerritory(root, &policy.Policy{}, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("mixed layout MUST surface exactly 1 violation (the drift file); got %d: %+v", got, r.Violations)
	}
	if r.Violations[0].File != driftRelPath {
		t.Errorf("violation file = %q, want %q", r.Violations[0].File, driftRelPath)
	}
}

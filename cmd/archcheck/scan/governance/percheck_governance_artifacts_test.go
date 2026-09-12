// Package scan — tests for ScanGovernanceArtifacts
// (percheck_governance_artifacts forward-prevention gate).
//
// Hermetic (t.TempDir-anchored). Pins each failure mode independently so a
// future refactor cannot silently drop one:
//
//  1. orphan_allowlist       — an allowlist no consumer opens.
//  2. allowlist_missing      — a consumer referencing a file that is gone.
//  3. allowlist_entry_ghost  — an entry pointing at a deleted path, both in
//     the path form and in the `<dir>/<pkg>:<Type>` key form.
//  4. deadline_expired       — a hotspot/active-registry deadline in the past.
//  5. The happy path: a consumed allowlist with live entries and future
//     deadlines produces zero violations.
package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// governanceFixture writes a file at the requested repo-relative path inside
// root, creating parent directories.
func governanceFixture(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// governanceRules returns the MatchedRule set of a report.
func governanceRules(rep *report.Report) map[string]int {
	out := map[string]int{}
	for _, v := range rep.Violations {
		if v.Rule != governanceArtifactsRule {
			continue
		}
		out[v.MatchedRule]++
	}
	return out
}

// TestScanGovernanceArtifacts_OrphanAllowlist — an allowlist nobody opens is
// dead governance and must fail closed.
func TestScanGovernanceArtifacts_OrphanAllowlist(t *testing.T) {
	root := t.TempDir()
	governanceFixture(t, root, "docs/migrations/orphan-allowlist.txt", "internal/foo/bar.go\n")
	governanceFixture(t, root, "cmd/tool/main.go", "package main\n")

	rep := &report.Report{}
	ScanGovernanceArtifacts(root, &policy.Policy{}, rep)
	if got := governanceRules(rep)["orphan_allowlist"]; got != 1 {
		t.Fatalf("orphan_allowlist violations = %d, want 1 (report=%+v)", got, rep.Violations)
	}
}

// TestScanGovernanceArtifacts_DanglingReference — a consumer pointing at a
// deleted allowlist must fail closed instead of exempting nothing silently.
func TestScanGovernanceArtifacts_DanglingReference(t *testing.T) {
	root := t.TempDir()
	governanceFixture(t, root, "cmd/tool/main.go",
		"package main\n\nconst allowlist = \"docs/migrations/deleted-allowlist.txt\"\n")

	rep := &report.Report{}
	ScanGovernanceArtifacts(root, &policy.Policy{}, rep)
	if got := governanceRules(rep)["allowlist_missing"]; got != 1 {
		t.Fatalf("allowlist_missing violations = %d, want 1 (report=%+v)", got, rep.Violations)
	}
}

// TestScanGovernanceArtifacts_GhostEntries — both entry shapes must be
// validated against the filesystem.
func TestScanGovernanceArtifacts_GhostEntries(t *testing.T) {
	root := t.TempDir()
	governanceFixture(t, root, "cmd/tool/main.go",
		"package main\n\nconst allowlist = \"docs/migrations/live-allowlist.txt\"\n")
	governanceFixture(t, root, "internal/capabilities/live/live.go", "package live\n")
	governanceFixture(t, root, "docs/migrations/live-allowlist.txt",
		"# comment\n"+
			"internal/capabilities/live/live.go   # owner=@x deadline=2999-01-01\n"+
			"internal/capabilities/live/gone.go   # owner=@x deadline=2999-01-01\n"+
			"internal/capabilities/live:Widget    # owner=@x deadline=2999-01-01\n"+
			"internal/capabilities/deleted:Widget # owner=@x deadline=2999-01-01\n")

	rep := &report.Report{}
	ScanGovernanceArtifacts(root, &policy.Policy{}, rep)
	if got := governanceRules(rep)["allowlist_entry_ghost"]; got != 2 {
		t.Fatalf("allowlist_entry_ghost violations = %d, want 2 (report=%+v)", got, rep.Violations)
	}
}

// TestScanGovernanceArtifacts_ExpiredDeadlines — a passed deadline in either
// registry is a hard failure.
func TestScanGovernanceArtifacts_ExpiredDeadlines(t *testing.T) {
	root := t.TempDir()
	governanceFixture(t, root, "cmd/tool/main.go", "package main\n")
	governanceFixture(t, root, "architecture/package_hotspots.json", `{
  "version": 1,
  "hotspots": [
    {"path": "internal/app", "owner": "app", "deadline": "2000-01-01"},
    {"path": "internal/kernel", "owner": "kernel", "deadline": "2999-01-01"}
  ]
}
`)
	governanceFixture(t, root, "architecture/current.yaml",
		"# generated\n- id: \"OLD-WORK\"\n  status: \"in_progress\"\n  deadline: \"2000-01-01T00:00:00Z\"\n")

	rep := &report.Report{}
	ScanGovernanceArtifacts(root, &policy.Policy{}, rep)
	if got := governanceRules(rep)["deadline_expired"]; got != 2 {
		t.Fatalf("deadline_expired violations = %d, want 2 (report=%+v)", got, rep.Violations)
	}
}

// TestScanGovernanceArtifacts_CleanLedger — a consumed allowlist with live
// entries and future deadlines is green (no false positives).
func TestScanGovernanceArtifacts_CleanLedger(t *testing.T) {
	root := t.TempDir()
	governanceFixture(t, root, "cmd/tool/main.go",
		"package main\n\nconst allowlist = \"docs/migrations/live-allowlist.txt\"\n")
	governanceFixture(t, root, "internal/capabilities/live/live.go", "package live\n")
	governanceFixture(t, root, "docs/migrations/live-allowlist.txt",
		"# header\ninternal/capabilities/live/live.go\n")
	governanceFixture(t, root, "architecture/package_hotspots.json",
		"{\"version\":1,\"hotspots\":[{\"path\":\"internal/app\",\"owner\":\"app\",\"deadline\":\"2999-01-01\"}]}\n")
	governanceFixture(t, root, "architecture/current.yaml",
		"- id: \"LIVE-WORK\"\n  status: \"in_progress\"\n  deadline: \"2999-01-01T00:00:00Z\"\n")

	rep := &report.Report{}
	ScanGovernanceArtifacts(root, &policy.Policy{MaxLinesStrictAllowlist: "docs/migrations/live-allowlist.txt"}, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("clean ledger produced %d violations: %+v", len(rep.Violations), rep.Violations)
	}
}

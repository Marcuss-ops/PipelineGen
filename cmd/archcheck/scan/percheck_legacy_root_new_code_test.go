package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanLegacyRootNewCodeRejectsAddedLegacyFile(t *testing.T) {
	root := t.TempDir()
	mustRunGit(t, root, "init")
	mustRunGit(t, root, "config", "user.email", "archcheck@example.invalid")
	mustRunGit(t, root, "config", "user.name", "archcheck")

	writeLegacyCodeFixture(t, root, "internal/application/existing.go")
	mustRunGit(t, root, "add", ".")
	mustRunGit(t, root, "commit", "-m", "baseline")

	writeLegacyCodeFixture(t, root, "internal/application/new.go")
	mustRunGit(t, root, "add", "internal/application/new.go")
	r := &report.Report{}
	ScanLegacyRootNewCode(root, &policy.Policy{LegacyInternalRoots: []string{"application", "api", "infrastructure", "domain"}}, r)

	if len(r.Violations) != 1 {
		t.Fatalf("expected one new legacy-root violation, got %#v", r.Violations)
	}
	if r.Violations[0].Rule != legacyRootNewCodeRule || r.Violations[0].File != "internal/application/new.go" {
		t.Fatalf("unexpected violation: %#v", r.Violations[0])
	}
}

func TestScanLegacyRootNewCodeIgnoresAddedFileMovedOutOfLegacyRoot(t *testing.T) {
	root := t.TempDir()
	mustRunGit(t, root, "init")
	mustRunGit(t, root, "config", "user.email", "archcheck@example.invalid")
	mustRunGit(t, root, "config", "user.name", "archcheck")

	writeLegacyCodeFixture(t, root, "internal/application/moved.go")
	if err := os.MkdirAll(filepath.Join(root, "internal", "capabilities"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, root, "add", ".")
	mustRunGit(t, root, "commit", "-m", "baseline")
	mustRunGit(t, root, "mv", "internal/application/moved.go", "internal/capabilities/moved.go")

	r := &report.Report{}
	ScanLegacyRootNewCode(root, &policy.Policy{LegacyInternalRoots: []string{"application"}}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("moved legacy file is not production code at its old path, got %#v", r.Violations)
	}
}

func TestScanLegacyRootNewCodeAllowsEditToExistingLegacyFile(t *testing.T) {
	root := t.TempDir()
	mustRunGit(t, root, "init")
	mustRunGit(t, root, "config", "user.email", "archcheck@example.invalid")
	mustRunGit(t, root, "config", "user.name", "archcheck")

	path := filepath.Join(root, "internal", "application", "existing.go")
	writeLegacyCodeFixture(t, root, "internal/application/existing.go")
	mustRunGit(t, root, "add", ".")
	mustRunGit(t, root, "commit", "-m", "baseline")
	if err := os.WriteFile(path, []byte("package application\n\n// migration edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanLegacyRootNewCode(root, &policy.Policy{LegacyInternalRoots: []string{"application"}}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("existing legacy-root edits must remain allowed, got %#v", r.Violations)
	}
}

func writeLegacyCodeFixture(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package application\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRunGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

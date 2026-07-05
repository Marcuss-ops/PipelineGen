package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshot is the load-bearing regression guard for FASE 1.C PR1:
// builds the archcheck binary, runs it on the repository root with the
// canonical policy.yaml, and asserts the stdout report is byte-
// identical to testdata/report_golden.json.
//
// Why build + exec instead of importing the package and calling main():
//   - `package main` cannot be imported from a test in the same
//     directory (Go forbids importing a `main` package). Building a
//     separate binary and exec'ing it is the canonical Go pattern
//     for snapshot tests on command binaries.
//   - The exec path exercises the SAME code path as a real
//     `go run ./cmd/archcheck` invocation — flag parsing, policy
//     load, scan dispatch, JSON marshal, stdout emit — so the test
//     catches regressions in any of those layers, not just the
//     scan functions.
//
// Golden file location: cmd/archcheck/testdata/report_golden.json.
// The file is the post-PR1 report (re-baselined at the end of
// FASE 1.C PR1). The ONLY known diff vs the pre-PR1 report is the
// file_size violation for cmd/archcheck/main.go (actual_lines
// 996 -> 782, expected outcome of the refactor). All other 38
// violations + the policy_snapshot + summary counts are byte-
// identical to the pre-PR1 report.
//
// Update cadence: when the report JSON shape changes (new field,
// removed field, renamed tag), re-run the test with
// `UPDATE_GOLDEN=1` to regenerate the golden file, inspect the
// diff, and commit the new golden in the same PR. Manual
// regeneration:
//
//	$ go run ./cmd/archcheck --root=. --policy=architecture/policy.yaml --phase=0 \
//	    > cmd/archcheck/testdata/report_golden.json
//
// Test isolation: uses t.TempDir() for the test binary so parallel
// test runs don't collide. The test is safe to run with
// `go test ./cmd/archcheck/...` from the repository root.
func TestSnapshot(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// pkgDir is cmd/archcheck/ when `go test` is invoked. The
	// binary needs the PROJECT ROOT (two levels up: cmd/archcheck/
	// -> cmd/ -> <repo>) as its CWD so the relative --root= and
	// --policy= paths resolve to the canonical
	// architecture/policy.yaml. The `..` walk happens here, not
	// via a string-relative-path like "../.." — filepath.Dir is
	// OS-agnostic and handles trailing slashes correctly.
	projectRoot := filepath.Dir(filepath.Dir(pkgDir))

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "archcheck_snapshot_test")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = pkgDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n--- build output ---\n%s", err, out)
	}

	run := exec.Command(binPath,
		"--root=.",
		"--policy=architecture/policy.yaml",
		"--phase=0",
	)
	run.Dir = projectRoot
	out, err := run.Output()
	if err != nil {
		t.Fatalf("archcheck run failed: %v (run.Dir=%s)\n--- stdout ---\n%s", err, projectRoot, out)
	}

	// Optional: allow re-baselining via UPDATE_GOLDEN=1 for the next
	// time the report shape changes (new field, removed field, etc.).
	// Pattern matches FASE 1.B's scripts/archcheck/snapshot_test.go
	// (see that file for the rationale on the wait-then-close
	// idiom; runner_test.go uses Output() so the pattern is
	// unnecessary here — exec.Output drains the pipe and
	// returns).
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		goldenPath := filepath.Join(pkgDir, "testdata", "report_golden.json")
		if err := os.WriteFile(goldenPath, out, 0644); err != nil {
			t.Fatalf("UPDATE_GOLDEN: write %s: %v", goldenPath, err)
		}
		t.Logf("UPDATE_GOLDEN: rewrote %s (%d bytes)", goldenPath, len(out))
		return
	}

	golden, err := os.ReadFile(filepath.Join(pkgDir, "testdata", "report_golden.json"))
	if err != nil {
		t.Fatalf("read testdata/report_golden.json: %v (the golden file is required for the snapshot test; regenerate via `go run ./cmd/archcheck ... > testdata/report_golden.json` or set UPDATE_GOLDEN=1 to auto-rewrite)", err)
	}

	if string(out) != string(golden) {
		// Emit a focused diff instead of dumping both files. Use
		// the stdlib-only `diff` shell command when available; fall
		// back to a length+prefix summary otherwise. The intent
		// is to make CI failure logs actionable without dragging
		// in a third-party diff library.
		diffCmd := exec.Command("diff", "-u",
			filepath.Join(pkgDir, "testdata", "report_golden.json"),
			"/dev/stdin",
		)
		diffCmd.Stdin = strings.NewReader(string(out))
		diffOut, _ := diffCmd.CombinedOutput()
		t.Errorf("snapshot mismatch: archcheck output (%d bytes) differs from testdata/report_golden.json (%d bytes)\n--- diff (truncated to 200 lines) ---\n%s\n--- end diff ---\nTo re-baseline: UPDATE_GOLDEN=1 go test ./cmd/archcheck/...",
			len(out), len(golden), firstNLines(string(diffOut), 200))
	}
}

// TestProjectRootContainsPolicyFile is a defensive sanity check:
// the snapshot test above depends on architecture/policy.yaml being
// reachable from the project root. If a future restructure moves
// the policy file (or the test runs from a different CWD), this
// test surfaces the issue immediately rather than at snapshot-
// diff time. Cheap (one os.Stat) and orthogonal to the snapshot
// itself; keep it co-located so the two tests fail together.
func TestProjectRootContainsPolicyFile(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Walk up two levels: cmd/archcheck/ -> cmd/ -> <repo>.
	projectRoot := filepath.Dir(filepath.Dir(pkgDir))
	polPath := filepath.Join(projectRoot, "architecture", "policy.yaml")
	if _, err := os.Stat(polPath); err != nil {
		t.Fatalf("expected %s to exist (project root is %s): %v", polPath, projectRoot, err)
	}
}

// firstNLines returns the first n lines of s, appending a truncation
// marker if the input was longer. Used by TestSnapshot to cap the
// diff output in CI failure logs (a 437-line JSON diff is too long
// to scroll through in a CI dashboard).
func firstNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n... [%d more lines truncated]", len(lines)-n)
}

// Package scan (test) — percheck_dual_mode_sync_test.go is the
// hermetic TDD coverage for the dual-mode-sync forward-
// prevention gate.
//
// Mirrors percheck_player_client_test.go layout:
//   - Per-substring probes (one Test per probe surface)
//   - Comment-only probe (godlike/07 residue accounting)
//   - Self-exemption probe (cmd/archcheck/scan)
//   - Allow-list probe (response_test.go)
//   - Whole-repo end-to-end probe (no violation on real repo
//     post-PR-morti-sync)
//
// godlike/06 SSOT: the test surface IS the canonical contract
// for the forward-prevention gate. Any future refactor that
// changes the substring probe list MUST also update this test
// set — the godlike/06 carry-forward convention is that the
// test failure message documents the gate trip in CI output
// for downstream agents.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ── Probe probes (one Test per match surface) ────────────────────────────

// TestScanDualModeSync_NoSyncSingleCall_Passes: a file that does
// not reference .syncSingle( anywhere emits 0 violations.
func TestScanDualModeSync_NoSyncSingleCall_Passes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "clean.go"),
		"package p\nfunc OK() { o := &Other{}; o.Do() }\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("clean.go violations = %d, want 0.\nViolations: %v", got, r.Violations)
	}
}

// TestScanDualModeSync_SyncSingleCall_Fails: a production-code
// line containing `.syncSingle(` (real call site) emits 1
// violation with the canonical rule + Note.
func TestScanDualModeSync_SyncSingleCall_Fails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "evil.go"),
		"package p\n"+
			"func caller(r *GenerateResponse) {\n"+
			"\tr.syncSingle(\"X\", 1, \"t\", \"en\", \"m\", \"cs\", false)\n"+
			"}\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("evil.go violations = %d, want 1.\nViolations: %v", got, r.Violations)
	}
	v := r.Violations[0]
	if v.Rule != "percheck_dual_mode_sync" {
		t.Fatalf("Violations[0].Rule = %q, want %q", v.Rule, "percheck_dual_mode_sync")
	}
	if v.MatchedRule != "dual_mode_sync_gate" {
		t.Fatalf("Violations[0].MatchedRule = %q, want %q", v.MatchedRule, "dual_mode_sync_gate")
	}
	if !strings.Contains(v.Note, ".syncSingle(") {
		t.Fatalf("Violations[0].Note should reference the offending literal; got: %q", v.Note)
	}
}

// TestScanDualModeSync_SyncMultiCall_Fails: parallel test for
// the multi-item helper re-introduction.
func TestScanDualModeSync_SyncMultiCall_Fails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "evil.go"),
		"package p\n"+
			"func caller(r *GenerateResponse) {\n"+
			"\tr.syncMulti(3, 5, nil)\n"+
			"}\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("syncMulti violation count = %d, want 1.\nViolations: %v", got, r.Violations)
	}
	if !strings.Contains(r.Violations[0].Note, ".syncMulti(") {
		t.Fatalf("Note should reference .syncMulti(; got: %q", r.Violations[0].Note)
	}
}

// TestScanDualModeSync_SyncSingleDef_Fails: a method-receiver
// definition line for syncSingle emits a violation (catches
// the PRODUCER side of re-introduction — putting the helper
// back on the canonical struct).
func TestScanDualModeSync_SyncSingleDef_Fails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "revival.go"),
		"package p\n"+
			"func (r *GenerateResponse) syncSingle(script string, count int) {\n"+
			"\tr.OK = true\n"+
			"\tr.Script = script\n"+
			"\tr.WordCount = count\n"+
			"}\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("syncSingle definition violation count = %d, want 1.\nViolations: %v",
			got, r.Violations)
	}
	if !strings.Contains(r.Violations[0].Note, "func (r *GenerateResponse) syncSingle") {
		t.Fatalf("Note should reference the offending definition pattern; got: %q",
			r.Violations[0].Note)
	}
}

// TestScanDualModeSync_SyncMultiDef_Fails: parallel for syncMulti
// method-receiver definition re-introduction.
func TestScanDualModeSync_SyncMultiDef_Fails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "revival.go"),
		"package p\n"+
			"func (r *GenerateResponse) syncMulti(count, total int, results []GenerateResponse) {\n"+
			"\tr.OK = true\n"+
			"\tr.Count = count\n"+
			"}\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 1 {
		t.Fatalf("syncMulti definition violation count = %d, want 1.\nViolations: %v",
			got, r.Violations)
	}
	if !strings.Contains(r.Violations[0].Note, "func (r *GenerateResponse) syncMulti") {
		t.Fatalf("Note should reference the offending definition pattern; got: %q",
			r.Violations[0].Note)
	}
}

// ── Comment-only probe (godlike/07 residue accounting) ───────────────────

// TestScanDualModeSync_CommentOnly_WarnsNotFails: a line
// starting with // that mentions one of the probe literals
// contributes 0 violations + 1 warning. Mirrors
// percheck_player_client.go + percheck_monitor.go / Check 54
// godlike/07 residue-accounting pattern.
func TestScanDualModeSync_CommentOnly_WarnsNotFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docstring.go"),
		"package p\n"+
			"// Before PR-morti-sync this used resp.syncSingle(\"...\") — retired.\n"+
			"func OK() {}\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("comment-only probe should NOT fail; violations = %d, want 0.\nViolations: %v",
			got, r.Violations)
	}
	if got := len(r.Warnings); got != 1 {
		t.Fatalf("comment-only probe should emit exactly 1 WARN; warnings = %d, want 1.\nWarnings: %v",
			got, r.Warnings)
	}
	if !strings.Contains(r.Warnings[0], "comment-only reference") {
		t.Fatalf("warning should mention comment-only residue; got: %q", r.Warnings[0])
	}
}

// ── Skip-list probes ─────────────────────────────────────────────────────

// TestScanDualModeSync_SkipDirs_NotScanned: a .go file with a
// probe-tripling line inside a top-level skip-dir (.git/vendor/
// node_modules/node-scraper/examples/scripts) is NOT traversed
// at all. Mirrors percheck_player_client.go + percheck_monitor.go.
func TestScanDualModeSync_SkipDirs_NotScanned(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeFile(t, filepath.Join(gitDir, "evilstash.go"),
		"package p\n"+
			"func f() { _ = GenerateResponse{}.syncSingle(\"X\", 1, \"t\", \"en\", \"m\", \"cs\", false) }\n")
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	writeFile(t, filepath.Join(scriptsDir, "evil.go"),
		"package p\n"+
			"func g() { _ = GenerateResponse{}.syncMulti(3, 5, nil) }\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("skip-dir probe should yield 0 violations; got %d.\nViolations: %v",
			got, r.Violations)
	}
}

// TestScanDualModeSync_AllowListScannerSelf: a .go file under
// cmd/archcheck/scan/ with the literal is exempt (scanner
// self-exemption per the percheck_player_client.go precedent).
func TestScanDualModeSync_AllowListScannerSelf(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scanDir := filepath.Join(dir, "cmd", "archcheck", "scan")
	if err := os.MkdirAll(scanDir, 0755); err != nil {
		t.Fatalf("mkdir scan: %v", err)
	}
	writeFile(t, filepath.Join(scanDir, "self.go"),
		"package scan\n"+
			"var dualModeSyncProbes = []string{\".syncSingle(\"}\n"+
			"func (r *GenerateResponse) syncMulti(n int) {}\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("scanner self-exemption failed: violations = %d, want 0.\nViolations: %v",
			got, r.Violations)
	}
}

// TestScanDualModeSync_AllowListResponseTest: the canonical
// response_test.go exemption (the field-count lock test MAY
// reference the retired helper names in comment prose).
func TestScanDualModeSync_AllowListResponseTest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resTestDir := filepath.Join(dir, "internal", "api", "script")
	if err := os.MkdirAll(resTestDir, 0755); err != nil {
		t.Fatalf("mkdir internal/api/script: %v", err)
	}
	writeFile(t, filepath.Join(resTestDir, "response_test.go"),
		"package script\n"+
			"// Pre-morti-sync the canonical sync-path was resp.syncSingle(\"x\", ...)\n"+
			"// + resp.syncMulti(count, total, results) — both retired. See\n"+
			"// PR-morti-sync + the field-count lock test in this file.\n"+
			"func f() {}\n")

	r := &report.Report{}
	scanDirForTest(t, dir, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("response_test.go exemption failed: violations = %d, want 0.\nViolations: %v",
			got, r.Violations)
	}
	// Comment-only hits ARE present; sanity check that they
	// were logged as warnings (godlike/07 residue accounting).
	if got := len(r.Warnings); got == 0 {
		t.Fatalf("response_test.go exemption should still WARN comment-only residue; Warnings=0 (expected ≥1)")
	}
}

// TestScanDualModeSync_NoViolationsOnRealRepo: end-to-end probe
// against the canonical PipelineGen repo post-PR-morti-sync.
// Locked invariant: every active production .go file is clean.
// This is the load-bearing regression guard — if a future
// contributor re-introduces syncSingle / syncMulti without
// updating the allow-list, this test fails CI immediately.
func TestScanDualModeSync_NoViolationsOnRealRepo(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t, findTestWorkingDir(t))

	r := &report.Report{}
	ScanDualModeSync(repoRoot, &policy.Policy{}, r)

	if got := len(r.Violations); got != 0 {
		t.Fatalf("real-repo scan violations = %d, want 0.\nFirst 5 violations: %v",
			got, firstNViolations(r.Violations, 5))
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

// scanDirForTest invokes the canonical walker on `dir` (the
// in-isolation variant for hermetic unit-test dispatch; mirrors
// ScanDualModeSync logic but with a per-test root path).
func scanDirForTest(t *testing.T, dir string, r *report.Report) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if dualModeSyncSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(dir, path)
			relSlash := filepath.ToSlash(rel)
			if dualModeSyncAllowlistMatch(relSlash) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		relSlash := filepath.ToSlash(rel)
		if dualModeSyncAllowlistMatch(relSlash) {
			return nil
		}
		scanDualModeSyncFile(path, relSlash, r)
		return nil
	})
}

// writeFile creates a file at path with the given content and
// fails the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// findTestWorkingDir returns the cwd (the package's directory).
// Tests live in cmd/archcheck/scan so the repo root is 2 levels
// up; findRepoRoot handles the walk.
func findTestWorkingDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return wd
}

// findRepoRoot walks up from `start` until it finds go.mod or
// fails the test (signals a mis-routed test invocation).
func findRepoRoot(t *testing.T, start string) string {
	t.Helper()
	cur := start
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	t.Fatalf("findRepoRoot: go.mod not found starting from %s", start)
	return ""
}

// firstNViolations returns the first n violations as a slice
// (used for bounded error-message size in tests).
func firstNViolations(v []report.Violation, n int) []report.Violation {
	if len(v) <= n {
		return v
	}
	return v[:n]
}

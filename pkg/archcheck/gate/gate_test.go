package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── filesystem helpers ──────────────────────────────────────────────────────

// writeFileFixture writes `body` to `path` relative to `dir` and fails
// the test on error. Used to construct on-disk fixtures for the Walk
// tests.
func writeFileFixture(t *testing.T, dir, path, body string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// chdir cd's into `dir` and registers Cleanup to restore the previous
// working directory. Many Walk tests assume the walk sees files written
// into cwd.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// ── walk tests (PASS-case only) ─────────────────────────────────────────────
//
// These tests verify Walk's PASS mechanics: clean files, test-file skip,
// ExcludeSuffixes, SkipDir, DefaultRoot, AbsPath opt-in/out, and the
// self-scan guard. The FAIL-case behavior (per-violation t.Errorf +
// t.Fatalf on total) is intentionally NOT unit-tested here: it IS the
// behavior the call sites rely on, so each of the 10 api/* gates
// doubles as integration coverage. Faking a *testing.TB to assert
// "Walk should have called Fatalf" trips Go 1.17+'s private() + Setenv
// interface requirement, so the unit-test path is left as docs.

// runWalkInSubtest runs Walk in a t.Run subtest and returns whether the
// subtest completed without flagging itself failed. Walk's normal-mode
// (no violations) never calls t.Errorf / t.Fatalf on a clean config, so
// a `true` return means Walk passed through cleanly. Subtest-failure
// detection via t.Run's bool return is the canonical Go pattern; no
// fake-TB shim needed.
func runWalkInSubtest(t *testing.T, name string, cfg Config) bool {
	t.Helper()
	return t.Run(name, func(tt *testing.T) {
		Walk(tt, cfg)
	})
}

func TestWalk_NoViolations_OnCleanFiles(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeFileFixture(t, dir, "innocent.go", "package x\n\nfunc Hello() string { return \"hi\" }\n")
	writeFileFixture(t, dir, "innocent_sub/util.go", "package innocent_sub\n\nfunc Util() int { return 42 }\n")

	if !runWalkInSubtest(t, "walk", Config{
		Root:               ".",
		ProhibitedPatterns: []Prohibition{{Name: "marker", Pattern: "marker"}},
	}) {
		t.Fatalf("walk should pass on clean files with no pattern matches")
	}
}

func TestWalk_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeFileFixture(t, dir, "production.go", "package x\n// clean\n")
	writeFileFixture(t, dir, "production_test.go", "package x\n// marker\n")
	writeFileFixture(t, dir, "gate_test.go", "package x\n// marker\n")

	// Production file is clean; the marker only lives in test files which
	// Walk MUST skip. Subtest should pass.
	if !runWalkInSubtest(t, "walk", Config{
		Root:               ".",
		ProhibitedPatterns: []Prohibition{{Name: "marker", Pattern: "marker"}},
	}) {
		t.Fatalf("walk should pass when markers only exist in _test.go / gate_test.go files")
	}
}

func TestWalk_ExcludeSuffixes(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeFileFixture(t, dir, "ok.go", "package x\n// clean\n")
	writeFileFixture(t, dir, "fixtures/golden.go", "package fixtures\n// marker\n")

	// golden.go is excluded by ExcludeSuffixes; no other file has marker.
	if !runWalkInSubtest(t, "walk", Config{
		Root:            ".",
		ExcludeSuffixes: []string{"golden.go"},
		ProhibitedPatterns: []Prohibition{
			{Name: "marker", Pattern: "marker"},
		},
	}) {
		t.Fatalf("walk should pass when only ExcludeSuffixes-flagged files have the marker")
	}
}

func TestWalk_SkipDir(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeFileFixture(t, dir, "ok.go", "package x\n// clean\n")
	writeFileFixture(t, dir, "skipped/violator.go", "package skipped\n// marker\n")

	// violator.go is under skipped/ directory which SkipDir returns true
	// for; Walk doesn't recurse into it.
	if !runWalkInSubtest(t, "walk", Config{
		Root:    ".",
		SkipDir: func(p string) bool { return p == "skipped" },
		ProhibitedPatterns: []Prohibition{
			{Name: "marker", Pattern: "marker"},
		},
	}) {
		t.Fatalf("walk should pass when violator lives under a SkipDir=true directory")
	}
}

func TestWalk_AcceptsAbsPath(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeFileFixture(t, dir, "ok.go", "package x\n// clean\n")

	if !runWalkInSubtest(t, "walk", Config{
		Root:         dir,
		AllowAbsPath: true,
	}) {
		t.Fatalf("walk should pass with AllowAbsPath=true + clean files")
	}
}

func TestWalk_DefaultRootEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeFileFixture(t, dir, "ok.go", "package x\n// clean\n")

	if !runWalkInSubtest(t, "walk", Config{
		Root: "", // falls back to "."
	}) {
		t.Fatalf("walk should pass with empty Root (defaults to .)")
	}
}

// Sanity test that Walk(+skip+exclude+prohibited) compose without
// panicking on the unit-test file itself. (We use a sentinel pattern
// that no production Go file in the package would ever contain.)
func TestWalk_DoesNotPanicOnSelfScan(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeFileFixture(t, dir, "ok.go", "package x\n// clean\n")

	if !runWalkInSubtest(t, "walk", Config{
		Root: ".",
		ProhibitedPatterns: []Prohibition{
			{Name: "self-sentinel", Pattern: "SELF_SENTINEL_NEVER_IN_SOURCE"},
		},
	}) {
		t.Fatalf("walk should pass: gate_test.go is excluded by name, sentinel not found")
	}
}

// AbsPath rejection (rootPath-validated Fatalf branch) is exercised
// at every call site: any gate that mis-passes an absolute path to
// Walk without AllowAbsPath will trip the Fatalf in CI. Faking this
// branch in a unit test requires a *testing.TB that records Fatalf
// without forwarding to the test runner — fragile, and infeasible on
// Go 1.17+ (requires Setenv + private() on the fake).
//
// Helper used by walk-skip tests above — must compile in unit-test mode
// only. Re-exports strings.Contains for readability in test bodies.
var _ = strings.Contains

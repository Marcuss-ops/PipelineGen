// Package mutationprobe is a TEST-ONLY harness that proves an invariant test
// actually has teeth: for each declared probe it applies one source mutation,
// runs the target test, and FAILS when that test still passes.
//
// # Why this exists
//
// A test that asserts an invariant can pass for the wrong reason — because the
// guard it names was never reached, or because some other guard happened to
// catch the case. The only evidence that a test would fail when its property
// breaks is to break the property and watch it fail. That was done by hand once
// (the chunk-identity tests) and hand work is not repeatable, so the procedure
// is codified here and run as a gate.
//
// # It is opt-in, deliberately
//
// The harness REWRITES SOURCE FILES for the duration of one test run, so it is
// armed only by VELOX_MUTATION_PROBE=1:
//
//	cd refactored
//	VELOX_MUTATION_PROBE=1 go test ./internal/capabilities/cliprender/ -run TestChunkInvariantsHaveTeeth -v
//
// # Safety rules it enforces on itself
//
//   - the anchor text must occur EXACTLY ONCE, so a probe can never silently
//     mutate the wrong place or nothing at all (a stale probe is an error, not
//     a pass);
//   - the mutation must be a real edit, not a no-op;
//   - each file is restored immediately after its probe and again via defer, and
//     the restore is verified byte-for-byte before any assertion is reported;
//   - the child `go test` runs with the arm variable FORCED EMPTY, so a probe
//     can never recurse;
//   - a mutation that breaks the BUILD is reported as a broken probe, because a
//     build failure proves nothing about the invariant the test pins.
//
// It is never imported by production code.
package mutationprobe

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// EnvVar arms the harness. Opt-in because the probes rewrite source files.
const EnvVar = "VELOX_MUTATION_PROBE"

// Probe is one declared "break the property and the test must notice" experiment.
type Probe struct {
	// Name is the human label used in failure messages.
	Name string
	// File is the repo-relative path of the source file to mutate.
	File string
	// Old is the exact anchor text to replace. It must occur exactly once.
	Old string
	// New is the replacement. It must keep the package compiling: a build
	// failure proves nothing about the invariant, so it is reported as a broken
	// probe instead of a passing one.
	New string
	// TestPackage is the package spec to run, e.g.
	// "./internal/capabilities/cliprender".
	TestPackage string
	// TestName is the test that must FAIL under the mutation.
	TestName string
}

// Enabled reports whether the harness is armed.
func Enabled() bool { return strings.TrimSpace(os.Getenv(EnvVar)) == "1" }

// Run applies every probe in turn and fails the calling test when a mutated tree
// still lets the named test pass. When the harness is not armed it skips, with
// the exact command needed to arm it.
func Run(t *testing.T, probes ...Probe) {
	t.Helper()
	if !Enabled() {
		t.Skipf("mutation probes are opt-in: re-run with %s=1 (this harness rewrites source files for the duration of the run)", EnvVar)
	}
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("mutationprobe: %v", err)
	}
	if len(probes) == 0 {
		t.Fatal("mutationprobe: no probes declared")
	}
	for _, p := range probes {
		probe := p
		t.Run(probe.Name, func(t *testing.T) {
			runProbe(t, root, probe)
		})
	}
}

func runProbe(t *testing.T, root string, p Probe) {
	t.Helper()
	if strings.TrimSpace(p.File) == "" || p.Old == "" || strings.TrimSpace(p.TestPackage) == "" || strings.TrimSpace(p.TestName) == "" {
		t.Fatalf("mutationprobe: incomplete probe %+v", p)
	}
	path := filepath.Join(root, filepath.FromSlash(p.File))
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("mutationprobe: read %s: %v", p.File, err)
	}

	// Register the restore BEFORE writing, so a panic between write and the
	// explicit restore still puts the file back.
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatalf("mutationprobe: RESTORE FAILED for %s: %v — restore it from version control", p.File, err)
		}
	}
	defer restore()

	occurrences := strings.Count(string(original), p.Old)
	if occurrences != 1 {
		t.Fatalf("mutationprobe: anchor for %q occurs %d time(s) in %s, want exactly 1 — the probe is stale",
			p.Name, occurrences, p.File)
	}
	mutated := strings.Replace(string(original), p.Old, p.New, 1)
	if mutated == string(original) {
		t.Fatalf("mutationprobe: mutation for %q is a no-op", p.Name)
	}
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		t.Fatalf("mutationprobe: write mutation for %q: %v", p.Name, err)
	}

	out, runErr := runChildTest(root, p.TestPackage, p.TestName)

	// Restore BEFORE asserting, so failures are reported against a clean tree.
	restore()
	onDisk, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(onDisk, original) {
		t.Fatalf("mutationprobe: %s was not restored byte-for-byte", p.File)
	}

	if runErr == nil {
		t.Errorf("mutationprobe: %q mutated %s but %s STILL PASSED — that test does not pin the property it names.\n--- child output (tail) ---\n%s",
			p.Name, p.File, p.TestName, tail(out))
		return
	}
	if buildFailure(out) {
		t.Errorf("mutationprobe: %q broke the BUILD instead of the invariant — the mutation must keep the package compiling.\n--- child output (tail) ---\n%s",
			p.Name, tail(out))
	}
}

// runChildTest runs the target test in a child process with the arm variable
// forced empty.
func runChildTest(root, pkg, name string) (string, error) {
	cmd := exec.Command("go", "test", "-run", "^"+name+"$", "-count=1", pkg)
	cmd.Dir = root
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, EnvVar+"=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, EnvVar+"=")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildFailure distinguishes "the mutation did not compile" from "the test
// caught the mutation". Only the latter is evidence.
func buildFailure(out string) bool {
	for _, marker := range []string{
		"[build failed]",
		"imported and not used",
		"declared and not used",
		"undefined:",
		"syntax error",
		"cannot use",
	} {
		if strings.Contains(out, marker) {
			return true
		}
	}
	return false
}

// moduleRoot walks up from this file to the directory holding go.mod.
func moduleRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the harness source path")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// tail returns the last few lines of child output for a readable failure.
func tail(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	const keep = 12
	if len(lines) > keep {
		lines = append([]string{fmt.Sprintf("... (%d earlier line(s) omitted)", len(lines)-keep)}, lines[len(lines)-keep:]...)
	}
	return strings.Join(lines, "\n")
}

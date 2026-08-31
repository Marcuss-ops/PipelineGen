// architecture-aggregate byte-determinism test (Step 5, June 2026).
//
// Two assertions:
//
//  1. TestAggregate_OwnershipDeterministic — runs the generator twice
//     against a synthetic mini-fixture and asserts byte-identical output.
//     This catches non-determinism in the writer itself.
//
//  2. TestAggregate_OwnershipMatchesCommitted — runs the generator
//     against the on-disk architecture/ownership/*.yaml split files and
//     asserts the regenerated view is byte-identical to
//     architecture/ownership.generated.yaml. This is the canonical
//     SSOT check: if a human edits a split file in a way that changes
//     the generated view, this test fails until they regenerate + commit.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAggregate_OwnershipDeterministic(t *testing.T) {
	dir := t.TempDir()
	// Mini fixture: 2 files with predictably ordered content.
	// Mini fixture mirrors the canonical shard layout (modules, jobs,
	// services, app/kernel/capabilities/platform, packages). Keep in
	// sync with ownershipSplitFiles in main.go.
	files := map[string]string{
		"modules.yaml":      "# module section\nmodule_route_map: []\n",
		"jobs.yaml":         "# job section\njob_handler_map: []\n",
		"services.yaml":     "# service section\ncanonical_services: []\n",
		"app.yaml":          "# app section\ncomposition_root:\n  owner: placeholder\n",
		"kernel.yaml":       "# kernel section\ndomain_job:\n  owner: placeholder\n",
		"capabilities.yaml": "# capability sections\napplication_jobs:\n  owner: placeholder\n",
		"platform.yaml":     "# platform sections\ninfrastructure_db:\n  owner: placeholder\n",
		"packages.yaml":     "# pkg section\npkg:\n  rule: placeholder\n",
	}
	// Write into a temporary "ownership" subdir of the t.TempDir.
	subdir := filepath.Join(dir, "architecture", "ownership")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, content := range files {
		path := filepath.Join(subdir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	// Inject the temp dir as the working dir so aggregateOwnership() finds the fixture.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	// First run.
	first, err := aggregateOwnership()
	if err != nil {
		t.Fatalf("aggregate (1st run): %v", err)
	}
	// Second run.
	second, err := aggregateOwnership()
	if err != nil {
		t.Fatalf("aggregate (2nd run): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("generator is non-deterministic:\n--- 1st run ---\n%s\n--- 2nd run ---\n%s\n", first, second)
	}
	// Sanity: the concatenated output is non-empty + contains both expected anchors.
	if !bytes.Contains(first, []byte("module_route_map")) {
		t.Errorf("missing module_route_map marker in output:\n%s", first)
	}
	if !bytes.Contains(first, []byte("pkg:")) {
		t.Errorf("missing pkg marker in output:\n%s", first)
	}
}

// findProjectRoot walks up from the current directory until it finds a
// go.mod file. Returns the absolute path to the directory containing
// go.mod. T.Fatalf if no go.mod is found in any ancestor.
//
// Rationale: `go test ./cmd/architecture-aggregate/...` runs the test
// binary with cwd = the package directory (cmd/architecture-aggregate/),
// NOT the project root. aggregateOwnership() uses relative paths so the
// caller must walk up to the project root before invoking.
func findProjectRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cur := cwd
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			t.Fatalf("could not find project root (go.mod) starting from cwd=%s", cwd)
			return ""
		}
		cur = parent
	}
}

func TestAggregate_OwnershipMatchesCommitted(t *testing.T) {
	// Walk up to project root (containing go.mod) because go test runs
	// the test binary with cwd = package directory.
	projectRoot := findProjectRoot(t)
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("chdir %s: %v", projectRoot, err)
	}

	// Now aggregateOwnership() resolves ./architecture/ownership/*.yaml
	// against the project root.
	got, err := aggregateOwnership()
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(projectRoot, "architecture", "ownership.generated.yaml"))
	if err != nil {
		t.Fatalf("read committed file: %v", err)
	}
	if !bytes.Equal(got, committed) {
		t.Fatalf("regenerated output differs from committed architecture/ownership.generated.yaml\n"+
			"(regenerated=%d bytes, committed=%d bytes)\n",
			len(got), len(committed))
	}
}

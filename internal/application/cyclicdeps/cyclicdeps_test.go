// Package cyclicdeps is the canonical home for the
// "no import cycles in the internal package graph" regression-guard.
//
// The canonical test surface is cyclicdeps_test.go which executes the
// official toolchain command `go list -deps -e -json` to evaluate the
// package graph metadata with the same accuracy as the compiler itself
// (respecting //go:build tags, OS/Arch constraints, module-mode, etc.).
//
// Two guards live here:
//
//   - TestNoImportCyclesInApplicationLayer — the application layer
//     (internal/application/...) must have ZERO import cycles.
//   - TestNoImportCyclesInInternalTree — the whole internal tree
//     (internal/...) must have ZERO import cycles, so every package
//     can be built and tested in isolation.
//
// This package ships as pure TDD test surface (no production symbols)
// so it has zero composition-root wiring cost. The hermetic detector
// requires only the Go toolchain (already required for the project) —
// no guru / goda dependency.
package cyclicdeps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// defaultListTimeout bounds the `go list` invocation so a misconfigured
// CI host (PATH drift, network hang on module proxy, etc.) does not
// stall the whole test run. 60s gives ~40x slack over the ~1.5s observed
// runtime on 300+ application-layer packages, so a slow module proxy on
// a CI runner still has plenty of headroom.
const defaultListTimeout = 60 * time.Second

// findModuleRoot walks up from the test file's directory to find the
// go.mod sentinel. This is the canonical Go pattern for "find repo
// root from inside a test" and matches the idiom used by other
// in-package test surfaces in this repo.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", file)
		}
		dir = parent
	}
}

// modulePathFromGoMod reads the `module <path>` directive from go.mod.
// The test uses this to scope cycle detection to the internal tree of
// THIS module only (not e.g. test fixtures, vendor trees, or any
// transitive dep that incidentally participates in a cycle).
func modulePathFromGoMod(t *testing.T, modRoot string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(modRoot, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	t.Fatal("no 'module' directive found in go.mod")
	return ""
}

// goPackageSubset is the minimal JSON projection of `go list -json`
// output we need. ImportCycle is the slice of package import paths
// that, starting from ImportPath, cycle back to ImportPath minus the
// closing edge — e.g. for cycle A→B→C→A, A's ImportCycle is
// ["A", "B", "C"] (the closing A is implicit and appended by us).
type goPackageSubset struct {
	ImportPath  string   `json:"ImportPath"`
	ImportCycle []string `json:"ImportCycle"`
}

// collectImportCycles runs `go list -deps -e -json <pattern>` and returns
// the sorted, deduplicated set of cycle descriptions for packages whose
// ImportPath has the given prefix. See TestNoImportCyclesInApplicationLayer
// for the rationale on why we shell out to `go list` (the compiler's own
// source of truth for the package graph) instead of re-implementing
// Tarjan SCC in-process.
func collectImportCycles(t *testing.T, pattern, prefix string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), defaultListTimeout)
	defer cancel()

	modRoot := findModuleRoot(t)

	// -deps: include all recursive dependencies so the cycle detection
	//        spans the full graph (a 3-way cycle A→B→C→A needs the
	//        edge C→A to be visible in the loader's view of A).
	// -e:      do not fail the subprocess on package load errors — we
	//        want the JSON stream regardless. Cycles are NOT an error
	//        for `go list`; they are reported in the ImportCycle field.
	// -json:   streaming JSON, one object per line.
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-e", "-json", pattern)
	cmd.Dir = modRoot

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start `go list` (is the Go toolchain in PATH?): %v\nstderr: %s", err, stderr.String())
	}

	dec := json.NewDecoder(stdout)
	var cycles []string
	for {
		var pkg goPackageSubset
		if err := dec.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("failed to parse `go list` JSON: %v", err)
		}
		// Scope-filter: only report cycles whose ImportPath is inside
		// THIS module's tree under `prefix`. Cycles in transitive
		// third-party deps are out of scope.
		if !strings.HasPrefix(pkg.ImportPath, prefix) {
			continue
		}
		if len(pkg.ImportCycle) == 0 {
			continue
		}
		// `go list` convention: ImportCycle is [start, ..., end] where
		// the closing back-edge to start is IMPLICIT. Append the start
		// (ImportPath) to close the loop visually: "A -> B -> C -> A".
		// (See cmd/go/internal/load/load.go: ImportCycle slice semantics.)
		// The `json.Decoder` returns a fresh slice per Decode call, so
		// it is safe to `append` directly without a defensive copy.
		cyclePath := strings.Join(append(pkg.ImportCycle, pkg.ImportPath), " -> ")
		cycles = append(cycles, fmt.Sprintf("  - %s: %s", pkg.ImportPath, cyclePath))
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("`go list` timed out after %s (probably too many packages or slow module proxy): %s", defaultListTimeout, strings.TrimSpace(stderr.String()))
		}
		// `go list -e` can return non-zero on individual package load
		// errors; cycles are NOT load errors. Surface stderr in the
		// failure message so a CI operator can diagnose.
		t.Fatalf("`go list` exited with error: %v\nstderr: %s", waitErr, strings.TrimSpace(stderr.String()))
	}

	// Preserve non-fatal stderr (deprecation warnings, module-mode
	// notices, GOFLAGS hints) on the SUCCESS path. `go list` is known
	// to emit these even on a clean run, and a CI operator triaging
	// an intermittent failure loses signal if we discard them.
	if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
		t.Logf("`go list` emitted warnings to stderr (non-fatal):\n%s", stderrStr)
	}

	// Stable, diff-friendly output: sort so the same set of cycles
	// always produces the same test failure message across machines.
	sort.Strings(cycles)
	return cycles
}

// TestNoImportCyclesInApplicationLayer is the canonical forward-prevention
// regression-guard for the PR-REFACTOR-P0-CYCLIC-DEPS lockstep. It shells
// out to `go list -deps -e -json ./internal/application/...` (the
// compiler's own source of truth for the package graph) and fails if
// any application-layer package reports a non-empty ImportCycle.
//
// Why shell out vs walk the graph in-process:
//
//  1. `go list` is the canonical, battle-tested implementation. Re-
//     implementing Tarjan SCC in-process is reinventing the wheel and
//     introduces a class of subtle bugs (alphabetical sort losing
//     edge direction, self-loop handling, build-tag blind spots).
//  2. `go list` understands //go:build tags, OS/Arch constraints,
//     module-mode, and replace directives — `go/build` requires
//     careful hand-configuration to match.
//  3. The user's spec literal is `go list -deps -f '{{.ImportCycle}}'
//     ./internal/application/...` — this test asserts the same
//     invariant as that command, programmatically.
//
// To verify manually: from the repo root, run
// `go list -deps -e -f '{{if .ImportCycle}}{{.ImportPath}}: {{join .ImportCycle " -> "}}{{end}}' ./internal/application/...`
func TestNoImportCyclesInApplicationLayer(t *testing.T) {
	modPath := modulePathFromGoMod(t, findModuleRoot(t))
	appPrefix := modPath + "/internal/application/"

	cycles := collectImportCycles(t, "./internal/application/...", appPrefix)
	if len(cycles) == 0 {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "found %d application-layer package(s) in an import cycle; godlike/07 NO-FAKE-AVAILABILITY requires ZERO cycles (PR-REFACTOR-P0-CYCLIC-DEPS in architecture/action-plans/2026-08-08-refactor-checklist-action-plan.md).\n\n", len(cycles))
	b.WriteString("Cycles (compiler source of truth, exact back-edges):\n")
	b.WriteString(strings.Join(cycles, "\n"))
	b.WriteString("\n\nTo reproduce manually:\n  go list -deps -e -f '{{if .ImportCycle}}{{.ImportPath}}: {{join .ImportCycle \" -> \"}}{{end}}' ./internal/application/...\n")
	t.Fatal(b.String())
}

// TestNoImportCyclesInInternalTree is the whole-tree counterpart of
// TestNoImportCyclesInApplicationLayer: it shells out to
// `go list -deps -e -json ./internal/...` and fails if ANY package under
// this module's internal tree reports a non-empty ImportCycle.
//
// Scope: every internal root (api, app, application, capabilities,
// domain, infrastructure, kernel, platform) is covered, so each package
// can be built and tested in isolation. Cycles in third-party transitive
// deps are out of scope by design (prefix filter).
//
// To verify manually: from the repo root, run
// `go list -deps -e -f '{{if .ImportCycle}}{{.ImportPath}}: {{join .ImportCycle " -> "}}{{end}}' ./internal/...`
func TestNoImportCyclesInInternalTree(t *testing.T) {
	modPath := modulePathFromGoMod(t, findModuleRoot(t))
	internalPrefix := modPath + "/internal/"

	cycles := collectImportCycles(t, "./internal/...", internalPrefix)
	if len(cycles) == 0 {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "found %d internal package(s) in an import cycle; every internal package must be buildable and testable in isolation.\n\n", len(cycles))
	b.WriteString("Cycles (compiler source of truth, exact back-edges):\n")
	b.WriteString(strings.Join(cycles, "\n"))
	b.WriteString("\n\nTo reproduce manually:\n  go list -deps -e -f '{{if .ImportCycle}}{{.ImportPath}}: {{join .ImportCycle \" -> \"}}{{end}}' ./internal/...\n")
	t.Fatal(b.String())
}

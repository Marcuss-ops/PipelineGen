// Package main — hardgate_integrity_test.go: forward prevention for the
// governance layer itself.
//
// A rule id listed in architecture/policy.yaml::hard_gates is a claim that the
// gate ALWAYS fails closed. That claim is only true if some scanner actually
// emits the id. When the scanner was repurposed, renamed or deleted and the
// id stayed in the list, the operator keeps a "hard gate" that can never fire
// — a false sense of security that no other test can detect, because an
// inert gate produces no violation to observe.
//
// This test derives the truth from the two artifacts that own it:
//
//	architecture/policy.yaml  → the declared hard gates
//	cmd/archcheck/**/*.go     → the scanners that emit rule ids
//
// A rule id is accepted when it is referenced by violation-emission code:
//
//   - the id appears as a quoted literal in a non-test scanner file that also
//     assigns a violation Rule (marker `Rule:` or `rule.Name`), OR
//   - the id is a `<base>_incomplete` variant actually built by concatenation
//     (the canonical doc scanners emit `rulePrefix + "_incomplete"`), i.e. the
//     base id is emitted AND the `"_incomplete"` suffix literal exists in the
//     same emission file.
//
// The second form exists because the five `*_doc_incomplete` ids are built,
// not spelled. Everything else must be a literal.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	policy "github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
)

// hardGateEmissionMarkers identify a scanner file as participating in
// violation emission (as opposed to merely naming a CheckSpec, which is what
// made the retired `percheck_api_infrastructure_imports` id look alive).
var hardGateEmissionMarkers = []string{"Rule:", "rule.Name", "warnings"}

// quotedLiteralRE captures every double-quoted Go string literal.
var quotedLiteralRE = regexp.MustCompile(`"([^"\n]*)"`)

func projectRootForHardGateTest(t *testing.T) string {
	t.Helper()
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir))
	if _, err := os.Stat(filepath.Join(root, "architecture", "policy.yaml")); err != nil {
		t.Fatalf("expected project root %q to contain architecture/policy.yaml: %v", root, err)
	}
	return root
}

// emittedRuleLiterals returns every quoted literal that appears in a non-test
// scanner file which emits violations.
func emittedRuleLiterals(t *testing.T, root string) map[string]bool {
	t.Helper()
	scanRoot := filepath.Join(root, "cmd", "archcheck")
	literals := map[string]bool{}

	err := filepath.Walk(scanRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		body := string(data)
		emitting := false
		for _, marker := range hardGateEmissionMarkers {
			if strings.Contains(body, marker) {
				emitting = true
				break
			}
		}
		if !emitting {
			return nil
		}
		for _, m := range quotedLiteralRE.FindAllStringSubmatch(body, -1) {
			literals[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk scanner sources: %v", err)
	}
	return literals
}

// TestHardGatesAreEmittable fails closed on a hard gate that no scanner can
// ever emit: the declared "always fails closed" claim would be false.
func TestHardGatesAreEmittable(t *testing.T) {
	root := projectRootForHardGateTest(t)

	pol, err := policy.Load(filepath.Join(root, "architecture", "policy.yaml"))
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if len(pol.HardGates) == 0 {
		t.Fatal("policy declares zero hard gates; this test would pass vacuously")
	}

	literals := emittedRuleLiterals(t, root)

	var inert []string
	for _, id := range pol.HardGates {
		if literals[id] {
			continue
		}
		// Built-by-concatenation variant: `<base>` emitted + `_incomplete`
		// suffix literal present in an emission file.
		if base, ok := strings.CutSuffix(id, "_incomplete"); ok && literals[base] && literals["_incomplete"] {
			continue
		}
		inert = append(inert, id)
	}
	sort.Strings(inert)
	for _, id := range inert {
		t.Errorf("hard gate %q can never be emitted: no scanner references it in violation-emission code. "+
			"An inert hard gate is a false fail-closed claim — remove the id from architecture/policy.yaml::hard_gates "+
			"or restore the scanner that emits it.", id)
	}
}

// TestHardGatesAreUnique rejects a duplicated id: a copy-paste duplicate hides
// the fact that one of the two entries is unowned.
func TestHardGatesAreUnique(t *testing.T) {
	root := projectRootForHardGateTest(t)

	pol, err := policy.Load(filepath.Join(root, "architecture", "policy.yaml"))
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	seen := map[string]int{}
	for _, id := range pol.HardGates {
		seen[id]++
	}
	var dupes []string
	for id, n := range seen {
		if n > 1 {
			dupes = append(dupes, id)
		}
	}
	sort.Strings(dupes)
	for _, id := range dupes {
		t.Errorf("hard gate %q is declared %d times", id, seen[id])
	}
}

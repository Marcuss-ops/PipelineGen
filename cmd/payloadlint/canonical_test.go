package main

// canonical_test.go keeps the reference payload honest.
//
// ops/jobs/canonical/script-generate-reference-20261126.json is the drift-free
// example the schema page points operators at. It must decode through the same
// strict gate as the endpoint, pass gencore's validator and produce no semantic
// findings — otherwise the documentation recommends a payload the API rejects.
//
// The fixture lives in the OUTER project (refactored is published on its own),
// so the test skips when the checkout has no ops/ directory.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalReferencePayloadLintsClean(t *testing.T) {
	// refactored/cmd/payloadlint → refactored → <project root>
	dir := filepath.Join("..", "..", "..", "ops", "jobs", "canonical")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("canonical reference not present in this checkout: %v", err)
	}

	report, err := lintDirWithOptions(dir, true)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	if report.Checked == 0 {
		t.Fatal("no envelope found under ops/jobs/canonical")
	}
	for _, payload := range report.Payloads {
		for _, problem := range payload.Errors {
			t.Errorf("%s: %s", payload.File, problem)
		}
		for _, warning := range payload.Warnings {
			t.Errorf("%s: warning: %s", payload.File, warning)
		}
	}
	if !report.OK {
		t.Fatal("the canonical reference payload must lint clean, warnings included")
	}
}

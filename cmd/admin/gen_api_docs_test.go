package main

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestGenerateMarkdown_Deterministic verifies that the API docs generator
// produces identical output across two consecutive runs with the same
// input.  Non-determinism (unsorted map iteration) would cause spurious
// diffs that break CI checks.
func TestGenerateMarkdown_Deterministic(t *testing.T) {
	// A representative, semi-realistic route table.
	routes := []gin.RouteInfo{
		{Method: "GET", Path: "/health"},
		{Method: "GET", Path: "/ready"},
		{Method: "GET", Path: "/metrics"},
		{Method: "GET", Path: "/api/jobs"},
		{Method: "POST", Path: "/api/jobs"},
		{Method: "GET", Path: "/api/jobs/:id"},
		{Method: "POST", Path: "/api/jobs/:id/cancel"},
		{Method: "POST", Path: "/api/artlist/search"},
		{Method: "GET", Path: "/api/artlist/stats"},
		{Method: "GET", Path: "/api/scripts"},
		{Method: "GET", Path: "/api/scripts/:id"},
		{Method: "POST", Path: "/api/media/voiceover/generate"},
		{Method: "GET", Path: "/api/media/clips/:source/folders"},
		{Method: "DELETE", Path: "/api/media/clips/:source/clips/:id"},
	}

	// Run twice — output must be identical.
	out1, _ := generateMarkdown(routes)
	out2, _ := generateMarkdown(routes)

	if out1 != out2 {
		t.Errorf("generateMarkdown is NOT deterministic:\n--- Run 1 ---\n%s\n--- Run 2 ---\n%s\n", out1, out2)
	}

	// Quick sanity: output should contain expected structure.
	if out1 == "" {
		t.Error("generateMarkdown returned empty output")
	}
}

// TestGenerateMarkdown_GoldenFile writes a golden file on first run and
// asserts zero diff on subsequent runs.  To update the golden file,
// delete it and re-run the test (or run with UPDATE_GOLDEN=1).
func TestGenerateMarkdown_GoldenFile(t *testing.T) {
	goldenPath := "testdata/gen_api_docs.golden"

	routes := []gin.RouteInfo{
		{Method: "GET", Path: "/health"},
		{Method: "GET", Path: "/ready"},
		{Method: "GET", Path: "/metrics"},
		{Method: "GET", Path: "/api/jobs"},
		{Method: "POST", Path: "/api/jobs"},
		{Method: "GET", Path: "/api/jobs/:id"},
		{Method: "POST", Path: "/api/jobs/:id/cancel"},
		{Method: "POST", Path: "/api/artlist/search"},
		{Method: "GET", Path: "/api/artlist/stats"},
		{Method: "GET", Path: "/api/scripts"},
		{Method: "GET", Path: "/api/scripts/:id"},
		{Method: "POST", Path: "/api/media/voiceover/generate"},
		{Method: "GET", Path: "/api/media/clips/:source/folders"},
		{Method: "DELETE", Path: "/api/media/clips/:source/clips/:id"},
		// An undocumented route to test the MISSING DESCRIPTION sentinel.
		{Method: "POST", Path: "/api/some/new/endpoint"},
	}

	out, missing := generateMarkdown(routes)
	if missing != 1 {
		t.Errorf("expected 1 missing description, got %d", missing)
	}

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		os.MkdirAll("testdata", 0755)
		if err := os.WriteFile(goldenPath, []byte(out), 0644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden file written to %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("golden file %s does not exist — run with UPDATE_GOLDEN=1 to create it", goldenPath)
		}
		t.Fatalf("read golden: %v", err)
	}

	if string(want) != out {
		t.Errorf("golden file mismatch.\n=== EXPECTED (golden) ===\n%s\n=== GOT ===\n%s", string(want), out)
	}
}

// TestGetDescription_MethodKey verifies that METHOD+PATH keys distinguish
// GET and POST to the same route.
func TestGetDescription_MethodKey(t *testing.T) {
	// GET and POST to /api/jobs should have different descriptions.
	getDesc := getDescription("/api/jobs", "GET")
	postDesc := getDescription("/api/jobs", "POST")

	if getDesc == postDesc {
		t.Errorf("GET and POST /api/jobs have the same description: %q", getDesc)
	}
	if getDesc == descMissing {
		t.Error("GET /api/jobs missing description")
	}
	if postDesc == descMissing {
		t.Error("POST /api/jobs missing description")
	}
}

// TestGetDescription_PatternMatch verifies that path-pattern matching
// with :param segments works correctly with method discrimination.
func TestGetDescription_PatternMatch(t *testing.T) {
	// Pattern "/api/scripts/:id" should match "/api/scripts/abc123"
	// only for GET requests.
	desc := getDescription("/api/scripts/some-script-id", "GET")
	if desc == descMissing {
		t.Error("GET /api/scripts/:id pattern match failed")
	}

	// POST to same path should not match (no pattern for it).
	descPost := getDescription("/api/scripts/some-script-id", "POST")
	if descPost != descMissing {
		t.Errorf("POST /api/scripts/:id should not match GET-only pattern, got %q", descPost)
	}
}

// TestGetDescription_JobsStatsBug verifies the P0.4 fix: /api/jobs/stats
// must NOT be described as "Get job by ID" (the :id pattern-match bug).
func TestGetDescription_JobsStatsBug(t *testing.T) {
	desc := getDescription("/api/jobs/stats", "GET")
	if desc == "Get job by ID" {
		t.Error("BUG REGRESSION: GET /api/jobs/stats still described as 'Get job by ID' — the /api/jobs/:id pattern wrongly matches")
	}
	// The stats route should either have its own description or be MISSING.
	// Currently it's not in the map, so it should be MISSING.
	if desc != descMissing {
		// Accept any description that isn't the wrong one.
		t.Logf("GET /api/jobs/stats description: %q", desc)
	}
}

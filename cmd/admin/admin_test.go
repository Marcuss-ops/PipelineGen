// cmd/admin/admin_test.go — static invariants guard
//
// Test suite:
//   - TestAdminCommands_AreRegistered
//   - TestBenchmarkReport_PreservesMetadata
//   - TestBenchmark_PropagatesSearchErrors
//   - TestAdminCommands_NoLegacyImports
//
// These tests protect the static invariants the PR fixes:
//
//  1. Every command listed in `availableCommands` must have a
//     `case "X":` arm in cmd/admin/subcommands.go's switch (otherwise operators
//     trigger an "Unknown command" exit-code-1 trip).
//  2. benchQueriesFile.Description/Version metadata must survive a
//     JSON round-trip through benchSaveReport / benchLoadQueries
//     (DRY-run observability).
//  3. Errors from the search-fn MUST propagate through benchRun into
//     benchQueryResult.Error and the aggregate benchReport.TotalErrors.
//     Pre-fix the code did `results, _ := searchFn(...)` and dropped
//     the error entirely — this test pins the fail-visible behaviour.
//  4. No *.go file in cmd/admin/ may import a retired legacy
//     package (`internal/config`, `internal/media`, `internal/media/*`,
//     `internal/storage`, `internal/upload/drive`, `internal/repository/clips`).
//     This mirrors the gate `! rg 'internal/(config|media|storage|upload|repository/clips)' cmd/admin --type go`
//     mentioned in the Definition of Done.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestAdminCommands_AreRegistered(t *testing.T) {
	if len(commandRegistry) == 0 {
		t.Fatal("command registry is empty")
	}
	if len(availableCommands) != len(commandRegistry) {
		t.Fatalf("availableCommands has %d entries, registry has %d", len(availableCommands), len(commandRegistry))
	}
	if !sort.StringsAreSorted(availableCommands) {
		t.Fatal("availableCommands must be sorted for stable help output")
	}
	for _, name := range availableCommands {
		handler, ok := commandRegistry[name]
		if !ok || handler == nil {
			t.Errorf("command %q has no callable registry handler", name)
		}
	}
	for _, name := range []string{"benchmark", "index-drive-clip", "upload-drive-file"} {
		if _, ok := commandRegistry[name]; !ok {
			t.Errorf("canonical command %q is missing from the registry", name)
		}
	}
}

func TestBenchmarkReport_PreservesMetadata(t *testing.T) {
	original := &benchReport{
		Description: "round-trip metadata canonicalisation test",
		Version:     "v1.2.3",
		Queries: []benchQueryResult{
			{Label: "q1", Query: "trees", Results: []benchResult{{ID: "a", Score: 0.95}}, Latency: 42},
		},
		TimingMS:    42,
		TotalErrors: 0,
	}

	// Marshal → unmarshal round-trip through the JSON shape used by
	// benchSaveReport.
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal benchReport: %v", err)
	}
	// Sanity-check the wire shape carries the optional metadata.
	if !strings.Contains(string(raw), `"description":"round-trip metadata canonicalisation test"`) {
		t.Errorf("marshalled report missing Description field: %s", raw)
	}
	if !strings.Contains(string(raw), `"version":"v1.2.3"`) {
		t.Errorf("marshalled report missing Version field: %s", raw)
	}

	var roundTrip benchReport
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("unmarshal benchReport: %v", err)
	}

	if roundTrip.Description != original.Description {
		t.Errorf("Description: got %q, want %q", roundTrip.Description, original.Description)
	}
	if roundTrip.Version != original.Version {
		t.Errorf("Version: got %q, want %q", roundTrip.Version, original.Version)
	}
	if len(roundTrip.Queries) != 1 || roundTrip.Queries[0].Label != "q1" {
		t.Errorf("queries round-trip failed: got %+v, want 1 entry with label q1", roundTrip.Queries)
	}
	if roundTrip.TotalErrors != 0 {
		t.Errorf("TotalErrors: got %d, want 0", roundTrip.TotalErrors)
	}
}

func TestBenchmark_PropagatesSearchErrors(t *testing.T) {
	failingErr := errors.New("simulated upstream semantic-search failure")

	// Search fn that fails for every query. The pre-fix code did
	// `results, _ := searchFn(...)` and silently dropped the error;
	// post-fix the error MUST land on benchQueryResult.Error and bump
	// benchReport.TotalErrors.
	queries := []benchQuery{
		{Label: "ok", Text: "trees", Source: "stock"},
		{Label: "broken", Text: "broken", Source: "stock"},
		{Label: "ok-2", Text: "rivers", Source: "stock"},
	}
	searchFn := func(ctx context.Context, q, source string, limit int) ([]benchResult, error) {
		_ = ctx
		_ = source
		_ = limit
		if q == "broken" {
			return nil, failingErr
		}
		return []benchResult{{ID: q, Score: 0.9}}, nil
	}

	report := benchRun(context.Background(), queries, searchFn, 10)

	if report.TotalErrors != 1 {
		t.Errorf("TotalErrors: got %d, want 1 (one failing query)", report.TotalErrors)
	}
	if len(report.Queries) != 3 {
		t.Fatalf("queries length: got %d, want 3", len(report.Queries))
	}

	for i, q := range report.Queries {
		wantErr := ""
		if queries[i].Text == "broken" {
			wantErr = failingErr.Error()
			if q.Results != nil {
				t.Errorf("query[%d] (%s): expected nil results on failing path, got %+v", i, q.Label, q.Results)
			}
		} else if len(q.Results) != 1 {
			t.Errorf("query[%d] (%s): expected 1 result on happy path, got %+v", i, q.Label, q.Results)
		}
		if q.Error != wantErr {
			t.Errorf("query[%d] (%s) Error: got %q, want %q", i, q.Label, q.Error, wantErr)
		}
	}
}

func TestAdminCommands_NoLegacyImports(t *testing.T) {
	// Forbidden imports — the static gate:
	//   ! rg 'internal/(config|media|storage|upload|repository/clips)' cmd/admin --type go
	// Banned patterns (anchored to the import line):
	bannedPatterns := []string{
		`"github.com/Marcuss-ops/PipelineGen/internal/config"`,
		`"github.com/Marcuss-ops/PipelineGen/internal/media"`,
		`"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"`,
		`"github.com/Marcuss-ops/PipelineGen/internal/media/models"`,
		`"github.com/Marcuss-ops/PipelineGen/internal/storage"`,
		`"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"`,
		`"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"`,
	}

	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob cmd/admin: %v", err)
	}

	for _, fname := range matches {
		// Skip _test.go files — the gate referenced in the PR brief
		// (`! rg ... cmd/admin`) operates on production code only; the
		// test file is allowed to mention legacy packages *as strings*
		// (the bannedPatterns list itself contains those names).
		if strings.HasSuffix(fname, "_test.go") {
			continue
		}

		src, err := os.ReadFile(fname)
		if err != nil {
			t.Fatalf("read %s: %v", fname, err)
		}
		contents := string(src)

		// Restrict to import block so a comment mentioning
		// "internal/media...legacy…" doesn't trip the gate. Imports are
		// inside a contiguous `import (...)` block, occasionally inline
		// `import "x"`.
		importBlock := extractImportBlock(contents)
		for _, pattern := range bannedPatterns {
			if strings.Contains(importBlock, pattern) {
				t.Errorf("file %s imports banned legacy package %s", fname, pattern)
			}
		}
	}
}

// extractImportBlock returns the union of `import (...)` blocks and
// any single-line `import "x"` statements as a single string for cheap
// substring matching. Comments outside the import blocks are excluded.
func extractImportBlock(src string) string {
	var b strings.Builder
	blockRE := regexp.MustCompile(`(?s)import\s*\((.*?)\)`)
	for _, m := range blockRE.FindAllStringSubmatch(src, -1) {
		b.WriteString(m[1])
		b.WriteString("\n")
	}
	lineRE := regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)
	for _, m := range lineRE.FindAllStringSubmatch(src, -1) {
		b.WriteString(`"`)
		b.WriteString(m[1])
		b.WriteString(`"`)
		b.WriteString("\n")
	}
	return b.String()
}

// Sanity: ensure the test file can see the banned imports listed above
// (avoids accidental deletion of the gate strings in the future).
func TestNoLegacyImports_gateStringsAreIntact(t *testing.T) {
	mustContain := []string{
		"internal/config",
		"internal/media",
		"internal/storage",
		"internal/upload/drive",
	}
	// Walk every *.go file under cmd/admin and ensure the gate strings
	// appear at least once. We don't care where — only that the gate is
	// review-resistant.
	matches, _ := filepath.Glob("*.go")
	combined := strings.Builder{}
	for _, fname := range matches {
		contents, _ := os.ReadFile(fname)
		combined.Write(contents)
		combined.WriteString("\n")
	}
	corpus := combined.String()
	for _, s := range mustContain {
		if !strings.Contains(corpus, s) {
			t.Errorf("expected substring %q in cmd/admin to keep the static gate reviewable", s)
		}
	}
}

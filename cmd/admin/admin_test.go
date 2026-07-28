// cmd/admin/admin_test.go — static invariants guard.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAdminCommands_AreRegistered(t *testing.T) {
	if len(subcommandRegistry) != len(availableCommands) {
		t.Fatalf("registry/list length mismatch: registry=%d available=%d", len(subcommandRegistry), len(availableCommands))
	}
	if len(subcommandRegistry) != len(subcommandHandlers) {
		t.Fatalf("registry/index length mismatch: registry=%d handlers=%d", len(subcommandRegistry), len(subcommandHandlers))
	}

	seen := make(map[string]struct{}, len(subcommandRegistry))
	for index, entry := range subcommandRegistry {
		if strings.TrimSpace(entry.name) == "" {
			t.Errorf("registry[%d] has an empty name", index)
			continue
		}
		if entry.run == nil {
			t.Errorf("registry[%d] %q has a nil handler", index, entry.name)
		}
		if _, exists := seen[entry.name]; exists {
			t.Errorf("duplicate command %q", entry.name)
		}
		seen[entry.name] = struct{}{}

		if availableCommands[index] != entry.name {
			t.Errorf("availableCommands[%d]=%q, want %q", index, availableCommands[index], entry.name)
		}
		if subcommandHandlers[entry.name] == nil {
			t.Errorf("command %q missing from dispatch index", entry.name)
		}
	}

	err := dispatchSubcommand("definitely-not-a-command", nil)
	if !errors.Is(err, errUnknownCommand) {
		t.Fatalf("unknown command error = %v, want errUnknownCommand", err)
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

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal benchReport: %v", err)
	}
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
	queries := []benchQuery{
		{Label: "ok", Text: "trees", Source: "stock"},
		{Label: "broken", Text: "broken", Source: "stock"},
		{Label: "ok-2", Text: "rivers", Source: "stock"},
	}
	searchFn := func(ctx context.Context, query, source string, limit int) ([]benchResult, error) {
		_ = ctx
		_ = source
		_ = limit
		if query == "broken" {
			return nil, failingErr
		}
		return []benchResult{{ID: query, Score: 0.9}}, nil
	}

	report := benchRun(context.Background(), queries, searchFn, 10)
	if report.TotalErrors != 1 {
		t.Errorf("TotalErrors: got %d, want 1", report.TotalErrors)
	}
	if len(report.Queries) != len(queries) {
		t.Fatalf("queries length: got %d, want %d", len(report.Queries), len(queries))
	}

	for index, result := range report.Queries {
		wantErr := ""
		if queries[index].Text == "broken" {
			wantErr = failingErr.Error()
			if result.Results != nil {
				t.Errorf("query[%d]: expected nil results on failure, got %+v", index, result.Results)
			}
		} else if len(result.Results) != 1 {
			t.Errorf("query[%d]: expected one result, got %+v", index, result.Results)
		}
		if result.Error != wantErr {
			t.Errorf("query[%d] Error: got %q, want %q", index, result.Error, wantErr)
		}
	}
}

func TestAdminCommands_NoLegacyImports(t *testing.T) {
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
	for _, filename := range matches {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		source, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		importBlock := extractImportBlock(string(source))
		for _, pattern := range bannedPatterns {
			if strings.Contains(importBlock, pattern) {
				t.Errorf("file %s imports banned legacy package %s", filename, pattern)
			}
		}
	}
}

func extractImportBlock(source string) string {
	var builder strings.Builder
	blockRE := regexp.MustCompile(`(?s)import\s*\((.*?)\)`)
	for _, match := range blockRE.FindAllStringSubmatch(source, -1) {
		builder.WriteString(match[1])
		builder.WriteString("\n")
	}
	lineRE := regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)
	for _, match := range lineRE.FindAllStringSubmatch(source, -1) {
		builder.WriteString(`"`)
		builder.WriteString(match[1])
		builder.WriteString(`"`)
		builder.WriteString("\n")
	}
	return builder.String()
}

func TestNoLegacyImports_gateStringsAreIntact(t *testing.T) {
	mustContain := []string{
		"internal/config",
		"internal/media",
		"internal/storage",
		"internal/upload/drive",
	}
	matches, _ := filepath.Glob("*.go")
	var combined strings.Builder
	for _, filename := range matches {
		contents, _ := os.ReadFile(filename)
		combined.Write(contents)
		combined.WriteString("\n")
	}
	corpus := combined.String()
	for _, value := range mustContain {
		if !strings.Contains(corpus, value) {
			t.Errorf("expected substring %q in cmd/admin to keep the static gate reviewable", value)
		}
	}
}

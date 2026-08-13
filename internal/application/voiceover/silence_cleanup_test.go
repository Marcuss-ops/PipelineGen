package voiceover

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

func TestBuildSilenceCleanupReport_LeadingAndTrailingTrims(t *testing.T) {
	const (
		original = int64(45_210_000)
		clean    = int64(43_870_000)
	)
	edits := []audio.AudioEdit{
		{SourceStartUS: 0, SourceEndUS: 620_000},
		{SourceStartUS: 44_490_000, SourceEndUS: 45_210_000},
	}
	report := BuildSilenceCleanupReport(original, clean, edits)
	if report == nil {
		t.Fatal("expected a report")
	}
	if report.OriginalDurationUS != original || report.CleanDurationUS != clean {
		t.Fatalf("durations = %+v, want original=%d clean=%d", report, original, clean)
	}
	if report.TrimStartUS != 620_000 || report.TrimEndUS != 720_000 {
		t.Fatalf("trims = start %d end %d, want 620000 / 720000", report.TrimStartUS, report.TrimEndUS)
	}

	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"original_duration_us", "trim_start_us", "trim_end_us", "clean_duration_us"} {
		if !containsKey(t, raw, field) {
			t.Fatalf("report JSON missing field %q: %s", field, raw)
		}
	}
}

func TestBuildSilenceCleanupReport_NilWhenNoEdits(t *testing.T) {
	if report := BuildSilenceCleanupReport(10_000_000, 10_000_000, nil); report != nil {
		t.Fatalf("expected nil report for no edits, got %+v", report)
	}
}

func TestBuildSilenceCleanupReport_IgnoresMiddleAndMalformedEdits(t *testing.T) {
	edits := []audio.AudioEdit{
		{SourceStartUS: 1_000_000, SourceEndUS: 2_000_000}, // middle silence: not a trim
		{SourceStartUS: 3_000_000, SourceEndUS: 2_000_000}, // malformed: negative removed
	}
	report := BuildSilenceCleanupReport(10_000_000, 9_000_000, edits)
	if report == nil {
		t.Fatal("expected a report")
	}
	if report.TrimStartUS != 0 || report.TrimEndUS != 0 {
		t.Fatalf("middle/malformed edits must not count as trims: %+v", report)
	}
}

func containsKey(t *testing.T, raw []byte, key string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	_, ok := m[key]
	return ok
}

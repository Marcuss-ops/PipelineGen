package observability

import (
	"encoding/json"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int64) *int64       { return &v }
func boolPtr(v bool) *bool        { return &v }

func TestRunResourceReportDefaultsSchemaVersionOnJSON(t *testing.T) {
	report := RunResourceReport{
		RunID:     "run-1",
		JobID:     "job-1",
		AttemptID: "attempt-1",
		Samples: []ResourceSample{{
			SampleID:   "sample-1",
			ObservedAt: time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
			CPUAvgPct:  floatPtr(0),
			GPUUtilPct: floatPtr(0),
			Throttling: boolPtr(false),
		}},
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := int(decoded["schema_version"].(float64)); got != RunResourceReportSchemaVersion {
		t.Fatalf("schema_version=%d, want %d", got, RunResourceReportSchemaVersion)
	}
	var roundTrip RunResourceReport
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Samples[0].CPUAvgPct == nil || *roundTrip.Samples[0].CPUAvgPct != 0 {
		t.Fatal("observed zero CPU value must survive JSON as a non-nil value")
	}
	if roundTrip.Samples[0].GPUUtilPct == nil || *roundTrip.Samples[0].GPUUtilPct != 0 {
		t.Fatal("observed zero GPU value must survive JSON as a non-nil value")
	}
	if roundTrip.Samples[0].Throttling == nil || *roundTrip.Samples[0].Throttling {
		t.Fatal("observed false throttling value must survive JSON as a non-nil value")
	}
}

func TestRunResourceReportRejectsUnsupportedVersionAndDuplicateSamples(t *testing.T) {
	base := RunResourceReport{RunID: "run-1", JobID: "job-1", AttemptID: "attempt-1", SchemaVersion: RunResourceReportSchemaVersion}
	base.Samples = []ResourceSample{
		{SampleID: "sample-1", ObservedAt: time.Now().UTC()},
		{SampleID: "sample-1", ObservedAt: time.Now().UTC()},
	}
	if err := base.Validate(); err == nil {
		t.Fatal("duplicate sample IDs must be rejected")
	}
	base.Samples = nil
	base.SchemaVersion++
	if err := base.Validate(); err == nil {
		t.Fatal("future schema versions must be rejected")
	}
	if err := json.Unmarshal([]byte(`{"schema_version":99,"run_id":"r","job_id":"j","attempt_id":"a"}`), &RunResourceReport{}); err == nil {
		t.Fatal("future JSON schema versions must be rejected")
	}
}

func TestRunResourceReportAllowsUnavailableResources(t *testing.T) {
	report := RunResourceReport{
		SchemaVersion: RunResourceReportSchemaVersion,
		RunID:         "run-1",
		JobID:         "job-1",
		AttemptID:     "attempt-1",
		Samples: []ResourceSample{{
			SampleID:   "sample-1",
			ObservedAt: time.Now().UTC(),
			// GPU, disk, network and temperature are intentionally nil.
			RSSPeakBytes: intPtr(1024),
		}},
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
}

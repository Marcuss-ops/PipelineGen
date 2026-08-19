package multilingual

import (
	"context"
	"testing"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type fakeRegistry struct {
	run   *capperformance.Run
	steps []capperformance.Step
}

func (f *fakeRegistry) RecordRun(_ context.Context, r capperformance.Run) error {
	f.run = &r
	return nil
}
func (f *fakeRegistry) RecordStep(_ context.Context, s capperformance.Step) error {
	f.steps = append(f.steps, s)
	return nil
}
func (f *fakeRegistry) RecordArtifact(context.Context, capperformance.Artifact) error { return nil }
func (f *fakeRegistry) RegisterWorkload(context.Context, capperformance.Workload) error {
	return nil
}

type fakeOps struct {
	recorded []kernobs.MeasuredOperation
}

func (f *fakeOps) RecordOperationReport(_ context.Context, p kernobs.OperationReport) error {
	f.recorded = append(f.recorded, kernobs.MeasuredOperation{
		ObservationID: p.ObservationID, Operation: p.Operation,
		CacheHit: p.CacheHit, ElapsedMS: p.DurationMs,
	})
	return nil
}

func TestRecorder_RecordRunProjectsRunAndSteps(t *testing.T) {
	reg := &fakeRegistry{}
	rec := NewRecorder(reg, &fakeOps{}, nil)
	started := time.Now().UTC().Truncate(time.Second)
	rec.RecordRun(context.Background(), RunMetrics{
		JobID:          "asset:x",
		WorkloadID:     "multilingual-render",
		StartedAt:      started,
		CompletedAt:    started.Add(280 * time.Second),
		WallMS:         280499,
		CPUUserMS:      1234,
		CPUSystemMS:    567,
		PeakRSSBytes:   123456,
		ClipCount:      10,
		SuccessCount:   10,
		WorkerLimit:    4,
		SumOperationMS: 407434,
		CacheHits:      9,
		CacheMisses:    12,
		Steps: []StepMetrics{
			{Name: "render", DurationMS: 115719, CacheHits: 1, CacheMisses: 9},
		},
	})
	if reg.run == nil {
		t.Fatalf("expected run to be recorded")
	}
	if reg.run.RunID != "multilingual-render:asset:x" {
		t.Fatalf("run_id = %q", reg.run.RunID)
	}
	if reg.run.WallMS != 280499 {
		t.Fatalf("wall_ms = %d, want 280499", reg.run.WallMS)
	}
	if reg.run.PeakRSSBytes != 123456 {
		t.Fatalf("peak_rss_bytes = %d, want 123456", reg.run.PeakRSSBytes)
	}
	if len(reg.steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(reg.steps))
	}
	if reg.steps[0].StepID != "multilingual-render:asset:x:render" {
		t.Fatalf("step_id = %q", reg.steps[0].StepID)
	}
	if !containsJSONKey(reg.run.MetadataJSON, "sum_operation_ms") {
		t.Fatalf("metadata_json must embed sum_operation_ms, got %s", reg.run.MetadataJSON)
	}
	if !containsJSONKey(reg.run.MetadataJSON, "worker_limit") {
		t.Fatalf("metadata_json must embed worker_limit, got %s", reg.run.MetadataJSON)
	}
}

func TestRecorder_RecordOperation(t *testing.T) {
	ops := &fakeOps{}
	rec := NewRecorder(&fakeRegistry{}, ops, nil)
	rec.RecordOperation(context.Background(), kernobs.MeasuredOperation{
		Operation: "multilingual.render",
		ElapsedMS: 42000,
		CacheHit:  true,
	})
	if len(ops.recorded) != 1 {
		t.Fatalf("recorded = %d, want 1", len(ops.recorded))
	}
	if ops.recorded[0].Operation != "multilingual.render" || !ops.recorded[0].CacheHit {
		t.Fatalf("operation = %+v", ops.recorded[0])
	}
}

func TestProcessResources_NonNegative(t *testing.T) {
	u, s, rss := ProcessResources()
	if u < 0 || s < 0 || rss < 0 {
		t.Fatalf("ProcessResources must never return negatives: %d %d %d", u, s, rss)
	}
}

func containsJSONKey(jsonStr, key string) bool {
	// cheap substring check over the marshalled metadata keys
	return len(jsonStr) > 0 && (jsonStr == key || stringContains(jsonStr, `"`+key+`"`))
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

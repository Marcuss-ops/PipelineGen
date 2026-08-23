package stockpipeline

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// canonicalStageRecorder captures appended stage observations so contract
// tests can assert what the canonical kernel recorder persisted. It
// implements the LifecycleRecorder seam used by Run.recordStage.
type canonicalStageRecorder struct {
	stages     []kernobs.StageReport
	operations []kernobs.OperationReport
}

func (r *canonicalStageRecorder) SaveReport(_ context.Context, _ *kernobs.RunReport) error {
	return nil
}
func (r *canonicalStageRecorder) StartReport(_ context.Context, _ *kernobs.RunReport) error {
	return nil
}
func (r *canonicalStageRecorder) AppendStage(_ context.Context, _ string, st kernobs.StageReport) error {
	r.stages = append(r.stages, st)
	return nil
}
func (r *canonicalStageRecorder) AppendOperation(_ context.Context, _ string, op kernobs.OperationReport) error {
	r.operations = append(r.operations, op)
	return nil
}
func (r *canonicalStageRecorder) RecordChild(_ context.Context, _ *kernobs.RunReport) error {
	return nil
}

func TestStockPhaseMetricContract_AllRequestedPhases(t *testing.T) {
	cases := []struct {
		phase    string
		itemsIn  int64
		itemsOut int64
	}{
		{"stock.search", 3, 3},
		{"stock.stage_sources", 2, 2},
		{"stock.youtube_download", 2, 2},
		{"stock.extract", 2, 2},
		{"stock.compose", 2, 2},
		{"stock.database_save", 2, 2},
		{"stock.index", 2, 2},
	}

	recorder := &canonicalStageRecorder{}
	run := kernobs.NewRunObserver(recorder).StartRun(context.Background(), kernobs.RunInfo{
		JobID: "stock-contract-job", AttemptID: "stock-contract-attempt",
	})
	ctx := kernobs.WithRun(context.Background(), run)
	for _, tc := range cases {
		t.Run(tc.phase, func(t *testing.T) {
			handle := startStockPhase(ctx, nil, tc.phase)
			if handle == nil {
				t.Fatal("startStockPhase returned nil")
			}
			handle.SetItems(tc.itemsIn, tc.itemsOut)
			got := handle.End(nil)
			if got.Name != tc.phase || got.Status != kernobs.StageStatusCompleted {
				t.Fatalf("stage = %#v, want completed %s", got, tc.phase)
			}
			if got.ItemsInput != tc.itemsIn || got.ItemsCompleted != tc.itemsOut {
				t.Fatalf("stage items = %d/%d, want %d/%d", got.ItemsInput, got.ItemsCompleted, tc.itemsIn, tc.itemsOut)
			}
		})
	}

	if got, want := len(recorder.stages), len(cases); got != want {
		t.Fatalf("persisted canonical stages = %d, want %d", got, want)
	}
}

func TestPrepareStockDriveArtifact_UsesCanonicalStage(t *testing.T) {
	recorder := &canonicalStageRecorder{}
	run := kernobs.NewRunObserver(recorder).StartRun(context.Background(), kernobs.RunInfo{
		JobID: "stock-drive-job", AttemptID: "stock-drive-attempt",
	})
	ctx := kernobs.WithRun(context.Background(), run)
	runner := &publishFakeRunner{
		artifactPrep: &recordingArtifactPreparation{},
		state:        &RunState{},
	}
	artifact := finalization.VerifiedArtifact{
		ArtifactID: "stock:contract:chunk:0",
		Filename:   "clip_001.mp4",
		SizeBytes:  4096,
	}

	if _, err := prepareStockDriveArtifact(ctx, runner, artifact, nil); err != nil {
		t.Fatalf("prepareStockDriveArtifact: %v", err)
	}
	if len(recorder.stages) != 0 {
		t.Fatalf("persisted canonical stages = %d, want 0 for upload operation", len(recorder.stages))
	}
	if len(recorder.operations) != 1 {
		t.Fatalf("persisted canonical operations = %d, want 1", len(recorder.operations))
	}
	got := recorder.operations[0]
	if got.Stage != string(kernobs.StagePublish) || got.Component != string(kernobs.ComponentDrive) || got.Operation != string(kernobs.OperationUpload) || got.Status != kernobs.StageStatusCompleted {
		t.Fatalf("operation = %#v, want completed drive upload", got)
	}
	if got.Items != 1 || got.Bytes != artifact.SizeBytes {
		t.Fatalf("operation counters = %d/%d, want 1/%d", got.Items, got.Bytes, artifact.SizeBytes)
	}
}

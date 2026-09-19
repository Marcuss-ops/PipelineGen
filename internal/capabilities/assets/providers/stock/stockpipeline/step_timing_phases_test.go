// Package stockpipeline — step_timing_phases_test.go
// (PR-STOCK-PHASE-ATTRIBUTION, September 2026).
//
// Contract tests for the stock phase-attribution instrumentation. Before this
// change three spans of a stock run recorded NO top-level stage, so their wall
// time fell into the timing breakdown's `unattributed` bucket (observed 23-44%
// on live runs):
//
//   - stock.duration_probe — the serial pre-planning provider probe
//     (enrichDirectURLDurations);
//   - stock.publish        — the Drive publication ladder;
//   - stock.finalize       — manifest build + Projection + spine write.
//
// These tests pin that each span now brackets itself in a canonical stage when
// a run is bound to the context, and that the step behaviour is unchanged.
package stockpipeline

import (
	"context"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// newTimingObservedRun starts a run bound to a canonicalStageRecorder (declared
// in phase_metrics_contract_test.go) so a test can assert which stages a call
// site recorded.
func newTimingObservedRun(t *testing.T) (context.Context, *canonicalStageRecorder) {
	t.Helper()
	recorder := &canonicalStageRecorder{}
	run := kernobs.NewRunObserver(recorder).StartRun(context.Background(), kernobs.RunInfo{
		JobID: "stock-timing-job", AttemptID: "stock-timing-attempt",
	})
	return kernobs.WithRun(context.Background(), run), recorder
}

func hasRecordedStage(recorder *canonicalStageRecorder, name string) bool {
	for _, st := range recorder.stages {
		if st.Name == name {
			return true
		}
	}
	return false
}

func recordedStageNames(recorder *canonicalStageRecorder) []string {
	names := make([]string, 0, len(recorder.stages))
	for _, st := range recorder.stages {
		names = append(names, st.Name)
	}
	return names
}

// TestStockPublishStep_RecordsCanonicalStage pins that stock.publish brackets
// the step in a canonical stage. The fixture runs the ArtifactPreparation-nil
// early-return path, which is enough to prove the stage wraps the whole body.
func TestStockPublishStep_RecordsCanonicalStage(t *testing.T) {
	ctx, recorder := newTimingObservedRun(t)
	runner := newFakeRunner(nil, 5, "")
	runner.state.Published = []ChunkState{{Index: 0, LocalPath: "/tmp/clip.mp4"}}

	if err := (StockPublishStep{}).Run(ctx, runner); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasRecordedStage(recorder, StepKeyStockPublish) {
		t.Fatalf("stock.publish stage not recorded; got stages %v", recordedStageNames(recorder))
	}
}

// TestStockFinalizeStep_RecordsCanonicalStage pins that stock.finalize brackets
// the step in a canonical stage even on its early error path.
func TestStockFinalizeStep_RecordsCanonicalStage(t *testing.T) {
	ctx, recorder := newTimingObservedRun(t)
	runner := newFakeRunner(nil, 5, "")
	runner.runInput = nil // force the early nil-RunInput error path

	if err := (StockFinalizeStep{}).Run(ctx, runner); err == nil {
		t.Fatal("expected an error for nil RunInput")
	}
	if !hasRecordedStage(recorder, StepKeyStockFinalize) {
		t.Fatalf("stock.finalize stage not recorded; got stages %v", recordedStageNames(recorder))
	}
}

// TestEnrichDirectURLDurations_RecordsCanonicalStage pins that the pre-planning
// provider probe records the stock.duration_probe stage.
func TestEnrichDirectURLDurations_RecordsCanonicalStage(t *testing.T) {
	ctx, recorder := newTimingObservedRun(t)
	const url = "https://www.youtube.com/watch?v=jNQXAC9IVRw"
	lister := &queryResolutionLister{
		results: map[string][]VideoInfo{url: {{ID: "jNQXAC9IVRw", Title: "me at the zoo", Duration: 19.06}}},
		errors:  make(map[string]error),
	}
	svc := newQueryResolutionService(lister)
	input := &RunInput{DirectURLs: []string{url}}

	svc.enrichDirectURLDurations(ctx, input)

	if !hasRecordedStage(recorder, "stock.duration_probe") {
		t.Fatalf("stock.duration_probe stage not recorded; got stages %v", recordedStageNames(recorder))
	}
}

package scriptgeneration

import (
	"context"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestProjectMapRendererPhasesProjectsOwnerMeasuredSummary pins the projection
// contract: the dynamic map renderer's OWN measured phases (tile prefetch /
// plate prepare / render+encode wall) land as canonical operations under
// StageOverlayRender with the chronon component, in milliseconds. The encode
// wall is reported in SECONDS by the telemetry summary and must be converted —
// the projection is unit normalization only, never a second timer.
func TestProjectMapRendererPhasesProjectsOwnerMeasuredSummary(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-map", AttemptID: "attempt-map"})
	ctx := kernobs.WithRun(context.Background(), run)

	projectMapRendererPhases(ctx, 15700, 18100, 64.8)

	ops := run.Report().Operations
	want := map[string]int64{"prefetch": 15700, "prepare": 18100, "encode": 64800}
	if len(ops) != len(want) {
		t.Fatalf("recorded operations = %d, want %d: %+v", len(ops), len(want), ops)
	}
	for _, op := range ops {
		if op.Stage != string(kernobs.StageOverlayRender) {
			t.Errorf("operation %s stage = %q, want %q", op.Operation, op.Stage, kernobs.StageOverlayRender)
		}
		if op.Component != string(kernobs.ComponentChronon) {
			t.Errorf("operation %s component = %q, want %q", op.Operation, op.Component, kernobs.ComponentChronon)
		}
		if op.Status != kernobs.StageStatusCompleted {
			t.Errorf("operation %s status = %q, want completed", op.Operation, op.Status)
		}
		expected, ok := want[op.Operation]
		if !ok {
			t.Errorf("unexpected operation %q", op.Operation)
			continue
		}
		if op.DurationMs != expected {
			t.Errorf("operation %s duration = %d ms, want %d ms", op.Operation, op.DurationMs, expected)
		}
	}
}

// TestProjectMapRendererPhasesSkipsUnmeasuredPhases locks the fail-honest
// rule: a phase the summary did not measure (non-positive) is absent from the
// run — never recorded as a fake zero that would dilute the breakdown.
func TestProjectMapRendererPhasesSkipsUnmeasuredPhases(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-map", AttemptID: "attempt-map"})
	ctx := kernobs.WithRun(context.Background(), run)

	projectMapRendererPhases(ctx, 0, -1, 0)

	if ops := run.Report().Operations; len(ops) != 0 {
		t.Fatalf("unmeasured phases recorded operations: %+v", ops)
	}
}

// TestProjectMapRendererPhasesWithoutRunIsNoop pins the instrumentation
// safety rule: with no run bound the projection must be a no-op, never a
// behaviour change (the map render itself keeps working unmeasured).
func TestProjectMapRendererPhasesWithoutRunIsNoop(t *testing.T) {
	projectMapRendererPhases(context.Background(), 15700, 18100, 64.8)
}

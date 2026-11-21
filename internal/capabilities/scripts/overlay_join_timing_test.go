package scriptgeneration

import (
	"context"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestRecordFinalVideoJoinAttributesEveryBoundary pins the attribution that was
// missing from the r8 biography run: the local assembly's wall clock must land
// on the persistence stage as fetch + join + hash, not as an unexplained
// stage wall whose only operation is the 1 ms SQLite write.
func TestRecordFinalVideoJoinAttributesEveryBoundary(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		JobID: "job-join", AttemptID: "attempt-join",
	})
	ctx := kernobs.WithRun(context.Background(), run)

	recordFinalVideoJoin(ctx, finalVideoJoinTiming{
		clips: 13, bytes: 250026038,
		fetchMS: 1200, joinMS: 455555, hashMS: 700,
	})

	want := map[string]int64{
		string(kernobs.OperationFetch): 1200,
		string(kernobs.OperationJoin):  455555,
		string(kernobs.OperationHash):  700,
	}
	ops := run.Report().Operations
	if len(ops) != len(want) {
		t.Fatalf("recorded %d operations, want %d: %+v", len(ops), len(want), ops)
	}
	for _, op := range ops {
		if op.Stage != string(kernobs.StageRunPersistence) {
			t.Fatalf("operation %q recorded under stage %q, want %q", op.Operation, op.Stage, kernobs.StageRunPersistence)
		}
		if got, ok := want[op.Operation]; !ok || op.DurationMs != got {
			t.Fatalf("operation %q duration = %d, want %d", op.Operation, op.DurationMs, got)
		}
		if op.Operation == string(kernobs.OperationJoin) && (op.Items != 13 || op.Bytes != 250026038) {
			t.Fatalf("join operation did not carry items/bytes: %+v", op)
		}
	}
}

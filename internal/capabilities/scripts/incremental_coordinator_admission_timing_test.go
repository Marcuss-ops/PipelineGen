package scriptgeneration

import (
	"context"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type quickProviderResolver struct{}

func (quickProviderResolver) ResolveProviders(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	return segment, nil
}

// TestSearchProvidersMeasuresAdmissionWaitSeparately is ELIM-02's core: with a
// single fan-out slot already taken, the time the scene spends queued must be
// recorded as admission_wait, not folded into resolve.
func TestSearchProvidersMeasuresAdmissionWaitSeparately(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-admission", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	coordinator := NewVidRushIncrementalCoordinator(&fakeSegmentEnricher{errs: map[string]error{}}, nil, 1)
	coordinator.SetSegmentProviderResolver(quickProviderResolver{})
	coordinator.providerResolveBudget = time.Second
	coordinator.providerSegmentBudget = time.Second
	// One slot, already held: the next scene must queue on it.
	coordinator.providerSem = make(chan struct{}, 1)
	coordinator.providerSem <- struct{}{}
	go func() {
		time.Sleep(40 * time.Millisecond)
		<-coordinator.providerSem
	}()

	if _, err := coordinator.searchProviders(ctx, scriptpkg.VidRushSegmentResult{SegmentID: "seg-admission"}); err != nil {
		t.Fatalf("searchProviders: %v", err)
	}
	run.Finish()

	var admission, resolve, joinBarrier int64 = -1, -1, 0
	for _, op := range run.Report().Operations {
		switch {
		case op.Operation == string(kernobs.OperationAdmissionWait):
			admission = op.DurationMs
			if op.Stage != string(StageSceneAnalysis) {
				t.Fatalf("admission_wait stage = %q, want %q", op.Stage, StageSceneAnalysis)
			}
		case op.Operation == string(kernobs.OperationResolve):
			resolve = op.DurationMs
		case op.Operation == string(kernobs.OperationJoinBarrier):
			joinBarrier++
		}
	}
	if admission < 30 {
		t.Fatalf("admission_wait = %d ms, want >= 30 (the slot was held for 40 ms)", admission)
	}
	if resolve < 0 {
		t.Fatal("resolve must still be recorded next to admission_wait")
	}
	if resolve >= admission {
		t.Fatalf("resolve = %d ms must exclude the %d ms admission wait", resolve, admission)
	}
	if joinBarrier != 0 {
		t.Fatalf("searchProviders must not record join_barrier operations (got %d)", joinBarrier)
	}
}

// TestAttachStageResidualsExposesUnattributedPhaseWall pins the sidecar field:
// a stage with 100 ms wall and 60 ms of operations reports 40 ms residual, and
// parallel operations that exceed the wall clamp at 0.
func TestAttachStageResidualsExposesUnattributedPhaseWall(t *testing.T) {
	stages := []kernobs.StageReport{
		{Name: "scene_analysis", DurationMs: 100},
		{Name: "prepare_join", DurationMs: 100},
	}
	ops := []kernobs.OperationReport{
		{Stage: "scene_analysis", DurationMs: 60},
		{Stage: "prepare_join", DurationMs: 120},
	}
	kernobs.AttachStageResiduals(stages, ops)
	if stages[0].UnattributedMs != 40 {
		t.Fatalf("scene_analysis residual = %d ms, want 40", stages[0].UnattributedMs)
	}
	if stages[1].UnattributedMs != 0 {
		t.Fatalf("prepare_join residual = %d ms, want 0 (clamped)", stages[1].UnattributedMs)
	}
}

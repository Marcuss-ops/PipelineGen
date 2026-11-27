package cliprender

import (
	"context"
	"errors"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// failingSubmitExecutor lets the first submit attempt fail after the plan is
// sealed and the checkpoint is written, then succeed on the retry.
type failingSubmitExecutor struct {
	fail        bool
	submitCalls int
}

func (f *failingSubmitExecutor) Submit(_ context.Context, _ ClipRenderPlanV1) error {
	f.submitCalls++
	if f.fail {
		return errors.New("transient remote submit failure")
	}
	return nil
}

func (f *failingSubmitExecutor) Settle(context.Context, ClipRenderPlanV1) (*RenderOutcome, error) {
	return certifiedTestOutcome(fullRenderOutcome()), nil
}

func newCheckpointWorker(t *testing.T) (*Worker, *fakeContinuationStore, *fakeContinuationEnqueuer, *failingSubmitExecutor) {
	t.Helper()
	w, _, _ := newTestWorker(t)
	store := &fakeContinuationStore{}
	enqueuer := &fakeContinuationEnqueuer{}
	renderer := &failingSubmitExecutor{}
	w.WithContinuationStore(store).WithContinuationEnqueuer(enqueuer).WithRenderExecutor(renderer)
	return w, store, enqueuer, renderer
}

// The index must be written BEFORE the remote submit: that is what makes a
// failed submit retryable without repeating preparation.
func TestSubmitWritesCheckpointAndClearsItOnSuccess(t *testing.T) {
	w, store, enqueuer, _ := newCheckpointWorker(t)
	req := baseRenderRequest()
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-checkpoint", Payload: renderJobPayload(t, req)}, nil)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if store.checkpointPuts != 1 {
		t.Fatalf("checkpoint writes = %d, want 1", store.checkpointPuts)
	}
	if enqueuer.calls != 1 {
		t.Fatalf("settle enqueues = %d, want 1", enqueuer.calls)
	}
	if store.checkpointDeletes != 1 {
		t.Fatalf("checkpoint deletes = %d, want 1 after a successful submit", store.checkpointDeletes)
	}
	if _, ok := store.checkpoints["job-checkpoint"]; ok {
		t.Fatal("checkpoint still present after a successful submit")
	}
	if got, _ := result["prepared_from_checkpoint"].(bool); got {
		t.Fatal("first attempt reports prepared_from_checkpoint=true")
	}
}

// The core CRA-07 contract: a retry after the seal reuses the prepared plan
// instead of re-running preparation.
func TestRetryReusesCheckpointInsteadOfPreparing(t *testing.T) {
	w, store, _, renderer := newCheckpointWorker(t)
	renderer.fail = true
	req := baseRenderRequest()
	j := &job.Job{ID: "job-retry", Payload: renderJobPayload(t, req)}

	if _, err := w.Handle(context.Background(), j, nil); err == nil {
		t.Fatal("first submit should fail on the injected transient error")
	}
	if _, ok := store.checkpoints["job-retry"]; !ok {
		t.Fatal("failed submit did not leave the resume checkpoint for the retry")
	}
	if store.checkpointDeletes != 0 {
		t.Fatalf("checkpoint deletes = %d, want 0 after a failed submit", store.checkpointDeletes)
	}

	renderer.fail = false
	result, err := w.Handle(context.Background(), j, nil)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, _ := result["prepared_from_checkpoint"].(bool); !got {
		t.Fatal("retry reports prepared_from_checkpoint=false: preparation was repeated")
	}
	if renderer.submitCalls != 2 {
		t.Fatalf("submit calls = %d, want 2 (failed + retried)", renderer.submitCalls)
	}
	if store.checkpointDeletes != 1 {
		t.Fatalf("checkpoint deletes = %d, want 1 after the successful retry", store.checkpointDeletes)
	}
	if _, ok := store.checkpoints["job-retry"]; ok {
		t.Fatal("checkpoint still present after the successful retry")
	}
}

// The checkpoint hit must skip the expensive preparation phases, not merely
// report a flag: materialization and transcript lookup happen zero times on the
// retry.
func TestRetryReusesCheckpointWithoutMaterializing(t *testing.T) {
	ctx := context.Background()
	w, mat, tr := newTestWorker(t)
	store := &fakeContinuationStore{}
	enqueuer := &fakeContinuationEnqueuer{}
	renderer := &failingSubmitExecutor{fail: true}
	w.WithContinuationStore(store).WithContinuationEnqueuer(enqueuer).WithRenderExecutor(renderer)

	j := &job.Job{ID: "job-retry-no-prep", Payload: renderJobPayload(t, baseRenderRequest())}
	if _, err := w.Handle(ctx, j, nil); err == nil {
		t.Fatal("first submit should fail on the injected transient error")
	}
	wantMaterialize := len(mat.calls)
	wantLookups := tr.lookupCalls
	if wantMaterialize == 0 || wantLookups == 0 {
		t.Fatalf("first attempt did not prepare (materialize=%d lookup=%d)", wantMaterialize, wantLookups)
	}

	renderer.fail = false
	result, err := w.Handle(ctx, j, nil)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, _ := result["prepared_from_checkpoint"].(bool); !got {
		t.Fatal("retry did not report the checkpoint hit")
	}
	if len(mat.calls) != wantMaterialize {
		t.Fatalf("retry materialized %d assets, want 0 extra (had %d)", len(mat.calls)-wantMaterialize, wantMaterialize)
	}
	if tr.lookupCalls != wantLookups {
		t.Fatalf("retry ran %d extra transcript lookups, want 0", tr.lookupCalls-wantLookups)
	}
	// One checkpoint write per submit attempt is the measured index cost.
	if store.checkpointPuts != 2 {
		t.Fatalf("checkpoint writes = %d, want one per submit attempt (2)", store.checkpointPuts)
	}

	// The settle continuation produced by the retried submit still resumes the
	// same plan and publishes one artifact.
	settlePayload, err := encodeContinuationPayload(enqueuer.req.Continuation)
	if err != nil {
		t.Fatalf("encode settle payload: %v", err)
	}
	settled, err := w.Handle(ctx, &job.Job{ID: "job-retry-no-prep-settle", Payload: settlePayload}, nil)
	if err != nil {
		t.Fatalf("settle after checkpoint retry: %v", err)
	}
	if settled["phase"] != "rendered" {
		t.Fatalf("settle phase = %v, want rendered", settled["phase"])
	}
}

// A checkpoint that points at a plan for another RunID is a foreign entry and
// must be ignored (full preparation), never published as this job's plan.
func TestCheckpointForeignPlanFallsBackToPreparation(t *testing.T) {
	ctx := context.Background()
	w, store, _, renderer := newCheckpointWorker(t)
	renderer.fail = true
	j := &job.Job{ID: "job-foreign", Payload: renderJobPayload(t, baseRenderRequest())}
	if _, err := w.Handle(ctx, j, nil); err == nil {
		t.Fatal("first submit should fail on the injected transient error")
	}
	doc := store.doc
	doc.Plan.RunID = "another-job"
	store.doc = doc

	renderer.fail = false
	result, err := w.Handle(ctx, j, nil)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, _ := result["prepared_from_checkpoint"].(bool); got {
		t.Fatal("a foreign plan checkpoint must not be reused")
	}
}

// A corrupt/unreadable index is an accelerator miss, never a render failure.
func TestCheckpointReadErrorFallsBackToPreparation(t *testing.T) {
	w, store, _, _ := newCheckpointWorker(t)
	store.getCheckpointErr = errors.New("corrupt index")
	req := baseRenderRequest()
	result, err := w.Handle(context.Background(), &job.Job{ID: "job-corrupt", Payload: renderJobPayload(t, req)}, nil)
	if err != nil {
		t.Fatalf("a corrupt checkpoint must not fail the render: %v", err)
	}
	if got, _ := result["prepared_from_checkpoint"].(bool); got {
		t.Fatal("corrupt checkpoint must not be reported as reused")
	}
}

// A checkpoint whose document no longer matches the request must be ignored:
// reusing it would publish bytes for a different request.
func TestCheckpointRequestDriftFallsBackToPreparation(t *testing.T) {
	w, store, _, renderer := newCheckpointWorker(t)
	renderer.fail = true
	req := baseRenderRequest()
	j := &job.Job{ID: "job-drift", Payload: renderJobPayload(t, req)}
	if _, err := w.Handle(context.Background(), j, nil); err == nil {
		t.Fatal("first submit should fail on the injected transient error")
	}
	// Simulate an entry written for a different request while the job id is
	// unchanged (a corrupted/tampered index).
	drifted := store.doc
	drifted.Request.Output.ForegroundScalePercent = 90
	store.doc = drifted

	renderer.fail = false
	result, err := w.Handle(context.Background(), j, nil)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, _ := result["prepared_from_checkpoint"].(bool); got {
		t.Fatal("a drifted checkpoint must not be reused")
	}
}

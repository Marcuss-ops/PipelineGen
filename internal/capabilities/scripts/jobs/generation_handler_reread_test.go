package jobs

// Live regression, promoted from production: when the runner finished FAILING
// a run it had also exhausted the attempt's context, and the handler's
// follow-up `runRepo.Get(ctx, run.ID)` failed with "context canceled". The job
// then died on `read durable run result: context canceled` — a message about
// the READ, not about the run — while the durable ledger already held the real
// terminal outcome (error_code/error_message). The live ledger holds 363 such
// masked terminal messages.
//
// The contract these tests pin: the outcome read is DECORRELATED from the
// attempt's cancellation. The durable row owns the truth; the job must route
// on it.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// canceledCtxRunRepository behaves exactly like the SQLite adapter on this
// axis: a read whose ctx is already dead fails with ctx.Err(), while the same
// read on a live ctx returns the durable row.
type canceledCtxRunRepository struct {
	scriptgen.RunRepository
	run *scriptgen.GenerationRun
}

func (r *canceledCtxRunRepository) Get(ctx context.Context, _ string) (*scriptgen.GenerationRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.run, nil
}

// TestReadRunOutcomeDetached_SurvivesTheAttemptsCancellation is the fix: the
// re-read must return the REAL durable outcome even when the parent ctx is
// already canceled.
func TestReadRunOutcomeDetached_SurvivesTheAttemptsCancellation(t *testing.T) {
	stored := &scriptgen.GenerationRun{
		ID:           "run_masked",
		Status:       scriptgen.RunStatusFailed,
		FailedStage:  scriptgen.StageCompilingAudio,
		AttemptCount: 3,
		ErrorMessage: "images available = 1, expected at least 2",
	}
	repo := &canceledCtxRunRepository{run: stored}
	h := &GenerateJobHandler{runRepo: repo}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the runner finished failing: the attempt's ctx is already dead

	// Sanity: the direct read IS the misleading one.
	if _, err := repo.Get(ctx, stored.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("fixture must reproduce the masked read, got %v", err)
	}

	got, err := h.readRunOutcomeDetached(ctx, stored.ID)
	if err != nil {
		t.Fatalf("the detached re-read must survive the cancellation, got: %v", err)
	}
	if got == nil || got.ErrorMessage != stored.ErrorMessage {
		t.Fatalf("re-read must return the durable outcome, got %+v", got)
	}

	// And the job must route on the REAL terminal error, never on the read.
	jobErr := incompleteRunError(got)
	if strings.Contains(jobErr.Error(), "context canceled") {
		t.Fatalf("the terminal message must be the run's own, got %q", jobErr.Error())
	}
	if !strings.Contains(jobErr.Error(), "images available = 1, expected at least 2") {
		t.Fatalf("terminal message must carry the real cause, got %q", jobErr.Error())
	}
}

// TestReadRunOutcomeDetached_IsBounded pins that the detached read cannot hang
// forever: the WithoutCancel strip is paired with a timeout, not a naked
// detached context.
func TestReadRunOutcomeDetached_IsBounded(t *testing.T) {
	if durableRunOutcomeReadTimeout <= 0 || durableRunOutcomeReadTimeout > time.Minute {
		t.Fatalf("durableRunOutcomeReadTimeout = %s, want a small bounded read", durableRunOutcomeReadTimeout)
	}
	blocking := &blockingRunRepository{}
	h := &GenerateJobHandler{runRepo: blocking}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := h.readRunOutcomeDetached(ctx, "run_blocking")
	if err == nil {
		t.Fatal("a hung repository must surface the timeout, not success")
	}
	if elapsed := time.Since(start); elapsed > durableRunOutcomeReadTimeout+time.Second {
		t.Fatalf("detached read took %s, want <= %s", elapsed, durableRunOutcomeReadTimeout)
	}
}

type blockingRunRepository struct {
	scriptgen.RunRepository
}

func (r *blockingRunRepository) Get(ctx context.Context, _ string) (*scriptgen.GenerationRun, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

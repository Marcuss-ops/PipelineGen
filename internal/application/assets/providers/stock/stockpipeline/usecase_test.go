package stockpipeline

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

type recordingJobsEnqueuer struct {
	ctx              context.Context
	req              *jobservice.EnqueueRequest
	job              *jobservice.Job
	returnErr        error
	correlation      string
	activeDuringCall bool
}

func (r *recordingJobsEnqueuer) Enqueue(ctx context.Context, req *jobservice.EnqueueRequest) (*jobservice.Job, error) {
	r.ctx = ctx
	r.req = req
	r.correlation = corid.FromContext(ctx)
	r.activeDuringCall = ctx.Err() == nil
	if r.returnErr != nil {
		return nil, r.returnErr
	}
	if r.job == nil {
		r.job = &jobservice.Job{ID: "job-123"}
	}
	return r.job, nil
}

var _ jobsEnqueuer = (*recordingJobsEnqueuer)(nil)

func TestStockUseCase_SubmitAsync_DetachesFromCancelledContext(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(corid.WithCorrelationID(context.Background(), "stock-correlation-123"))
	cancel()

	enqueuer := &recordingJobsEnqueuer{}
	uc := NewStockUseCase(nil, enqueuer, zap.NewNop())

	jobID, err := uc.Submit(parent, &StockCommand{TotalMinutes: 5}, true)
	if err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
	if jobID != "job-123" {
		t.Fatalf("Submit returned jobID %q, want %q", jobID, "job-123")
	}
	if enqueuer.ctx == nil {
		t.Fatal("expected enqueue context to be recorded")
	}
	if !enqueuer.activeDuringCall {
		t.Fatal("expected detached enqueue context to remain active during Enqueue call")
	}
	if got := enqueuer.correlation; got != "stock-correlation-123" {
		t.Fatalf("expected correlation id to survive detach, got %q", got)
	}
	if enqueuer.req == nil || enqueuer.req.Type != "media.stock" {
		t.Fatalf("unexpected enqueue request: %#v", enqueuer.req)
	}
}

func TestStockUseCase_SubmitAsync_ReturnsJobsServiceRequiredWhenUnwired(t *testing.T) {
	t.Parallel()

	uc := NewStockUseCase(nil, nil, zap.NewNop())
	_, err := uc.Submit(context.Background(), &StockCommand{TotalMinutes: 5}, true)
	if !errors.Is(err, ErrJobsServiceRequired) {
		t.Fatalf("expected ErrJobsServiceRequired, got %v", err)
	}
}

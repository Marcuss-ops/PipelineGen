package queue

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type fixedRetryResolver int
func (r fixedRetryResolver) GetMaxRetries(string) (int, error) { return int(r), nil }

type duplicateBroker struct {
	job.JobBroker
	existing *job.Job
}
func (b *duplicateBroker) Create(context.Context, *job.Job) error { return job.ErrDuplicate }
func (b *duplicateBroker) FindByTypeAndCorrelation(context.Context, string, string) (*job.Job, error) { return b.existing, nil }
func (b *duplicateBroker) FindActiveByKey(context.Context, string) (*job.Job, error) { return nil, nil }
func (b *duplicateBroker) FindByClientAndIdempotencyKey(context.Context, string, string) (*job.Job, error) { return nil, nil }
func (b *duplicateBroker) Get(context.Context, string) (*job.Job, error) { return nil, nil }

func TestServiceEnqueue_DuplicateUsesKernelSentinelAndReturnsExisting(t *testing.T) {
	t.Parallel()
	existing := &job.Job{ID: "job-existing", Type: "demo.job", CorrelationID: "corr-1"}
	svc := NewService(&duplicateBroker{existing: existing}, fixedRetryResolver(3), nil, zap.NewNop())
	got, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: "demo.job", CorrelationID: "corr-1", Payload: map[string]any{"x": 1}})
	if err != nil { t.Fatalf("Enqueue() error = %v", err) }
	if got == nil || got.ID != existing.ID { t.Fatalf("Enqueue() = %#v, want existing %#v", got, existing) }
}

func TestServiceEnqueue_DuplicateWithoutRecoverableKeyPropagates(t *testing.T) {
	t.Parallel()
	svc := NewService(&duplicateBroker{}, fixedRetryResolver(3), nil, zap.NewNop())
	_, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: "demo.job"})
	if err == nil || !errors.Is(err, job.ErrDuplicate) { t.Fatalf("Enqueue() error = %v, want job.ErrDuplicate", err) }
}

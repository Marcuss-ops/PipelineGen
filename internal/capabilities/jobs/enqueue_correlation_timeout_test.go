package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type correlationTimeoutBroker struct {
	createCalls int
	findCalls   int
	lastCreated *job.Job
}

func (m *correlationTimeoutBroker) Create(_ context.Context, j *job.Job) error {
	m.createCalls++
	m.lastCreated = j
	return nil
}

func (m *correlationTimeoutBroker) Get(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *correlationTimeoutBroker) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (m *correlationTimeoutBroker) FindActiveByKey(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *correlationTimeoutBroker) FindByTypeAndCorrelation(_ context.Context, _ string, _ string) (*job.Job, error) {
	m.findCalls++
	return nil, context.DeadlineExceeded
}
func (m *correlationTimeoutBroker) FindByClientAndIdempotencyKey(_ context.Context, _, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *correlationTimeoutBroker) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return nil, nil
}
func (m *correlationTimeoutBroker) Retry(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}
func (m *correlationTimeoutBroker) ClaimNext(_ context.Context, _ string, _ time.Duration, _ []string) (*job.Job, error) {
	return nil, nil
}
func (m *correlationTimeoutBroker) Complete(_ context.Context, _ string, _, _ string, _ int, _ json.RawMessage) error {
	return nil
}
func (m *correlationTimeoutBroker) Fail(_ context.Context, _ string, _, _ string, _ int, _ string) error {
	return nil
}
func (m *correlationTimeoutBroker) ScheduleRetry(_ context.Context, _ string, _, _ string, _ int, _ string, _ time.Duration) error {
	return nil
}
func (m *correlationTimeoutBroker) Cancel(_ context.Context, _ string) error { return nil }
func (m *correlationTimeoutBroker) SetProgress(_ context.Context, _ string, _ int, _ string) error {
	return nil
}
func (m *correlationTimeoutBroker) AddEvent(_ context.Context, _ string, _, _ string, _ map[string]any) error {
	return nil
}

// FASE 4(b) (July 2026): the canonical job.Store.RenewLease
// signature now returns the typed RenewLeaseResult envelope
// (LeaseStateContinue | CancelRequested | LeaseLost). The pre-Fase-4
// `error`-only return is gone. The stub returns LeaseStateContinue
// (the worker continues renewing) so the existing
// TestEnqueue_CorrelationLookupTimeoutDoesNotBlockCreate assertion
// is unaffected — the test exercises the correlation-lookup path,
// not the renew-loop path.
func (m *correlationTimeoutBroker) RenewLease(_ context.Context, _ string, _ string, _ time.Duration) (job.RenewLeaseResult, error) {
	return job.RenewLeaseResult{State: job.LeaseStateContinue}, nil
}
func (m *correlationTimeoutBroker) DeadLetter(_ context.Context, _ string, _ string) error {
	return nil
}

// FinalizeAttempt is a Push 4.6 stub — the canonical FinalizeAttempt is
// not exercised by this test (correlation-lookup-timeout fallback path).
// Push 4.6 (kernel-job-cutover) replaces this stub with a typed test
// double that records the FinalizeAttemptCommand as the
// Push 4.2 contract mandates for terminal-decision testing. Stub
// returns errors.New to fail-fast if a future regression accidentally
// exercises this codepath through the mock (godlike/07 fail-closed).
func (m *correlationTimeoutBroker) FinalizeAttempt(_ context.Context, _ job.FinalizeAttemptCommand) (job.FinalizeAttemptResult, error) {
	return job.FinalizeAttemptResult{}, errors.New("correlationTimeoutBroker.FinalizeAttempt: Push 4.6 stub (not exercised by TestEnqueue_CorrelationLookupTimeoutDoesNotBlockCreate)")
}

var _ job.JobBroker = (*correlationTimeoutBroker)(nil)

func TestEnqueue_CorrelationLookupTimeoutDoesNotBlockCreate(t *testing.T) {
	t.Parallel()

	reg := newWiringRegistry(t, time.Minute, 3)
	broker := &correlationTimeoutBroker{}
	// nil dispatcher: this test exercises the correlation-lookup
	// timeout fallback, not handler registration.
	svc, err := NewService(broker, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:          wiringTestType,
		CorrelationID: "timeout-correlation-key",
		Payload:       map[string]any{"hello": "world"},
	})
	if err != nil {
		t.Fatalf("Enqueue must ignore transient correlation lookup timeout and continue: %v", err)
	}
	if got == nil || got.ID == "" {
		t.Fatalf("expected a created job, got %#v", got)
	}
	if broker.findCalls != 1 {
		t.Fatalf("expected exactly one correlation lookup, got %d", broker.findCalls)
	}
	if broker.createCalls != 1 {
		t.Fatalf("expected Create to run after timeout fallback, got %d calls", broker.createCalls)
	}
	if broker.lastCreated.CorrelationID != "timeout-correlation-key" {
		t.Fatalf("correlation ID lost across fallback: got %q", broker.lastCreated.CorrelationID)
	}
}

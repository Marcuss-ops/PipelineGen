package queue

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// scheduledBroker records how Enqueue persisted the job. It embeds the
// JobBroker and ScheduleStore interfaces so only the methods the enqueue
// path actually calls need a body.
type scheduledBroker struct {
	job.JobBroker
	job.ScheduleStore
	created   *job.Job
	runAt     time.Time
	immediate bool
}

func (b *scheduledBroker) Create(_ context.Context, j *job.Job) error {
	b.created = j
	b.immediate = true
	return nil
}

func (b *scheduledBroker) CreateScheduled(_ context.Context, j *job.Job, runAt time.Time) error {
	b.created = j
	b.runAt = runAt
	return nil
}

func (b *scheduledBroker) FindActiveByKey(context.Context, string) (*job.Job, error) {
	return nil, nil
}

func (b *scheduledBroker) FindByTypeAndCorrelation(context.Context, string, string) (*job.Job, error) {
	return nil, nil
}

func (b *scheduledBroker) FindByClientAndIdempotencyKey(context.Context, string, string) (*job.Job, error) {
	return nil, nil
}

func (b *scheduledBroker) Get(context.Context, string) (*job.Job, error) { return nil, nil }

func TestEnqueueFutureScheduledAtPersistsScheduledStatus(t *testing.T) {
	b := &scheduledBroker{}
	svc := NewService(b, fixedRetryResolver(3), nil, zap.NewNop()).WithScheduler(b)
	runAt := time.Now().Add(time.Hour).Truncate(time.Second)

	j, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type: "video.create", Payload: map[string]any{"a": 1}, ScheduledAt: &runAt,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if j.Status != job.StatusScheduled {
		t.Fatalf("status = %s, want SCHEDULED", j.Status)
	}
	if b.immediate {
		t.Fatal("future schedule must not use the immediate Create path")
	}
	if !b.runAt.Equal(runAt) {
		t.Fatalf("runAt = %v, want %v", b.runAt, runAt)
	}
}

func TestEnqueueFutureScheduledAtFailsClosedWithoutStore(t *testing.T) {
	b := &scheduledBroker{}
	svc := NewService(b, fixedRetryResolver(3), nil, zap.NewNop()) // no WithScheduler
	runAt := time.Now().Add(time.Hour)

	if _, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: "video.create", ScheduledAt: &runAt}); err == nil {
		t.Fatal("a future ScheduledAt without a schedule store must fail closed")
	}
	if b.immediate {
		t.Fatal("fail-closed enqueue must not fall back to immediate Create")
	}
}

func TestEnqueuePastScheduledAtEnqueuesImmediately(t *testing.T) {
	b := &scheduledBroker{}
	svc := NewService(b, fixedRetryResolver(3), nil, zap.NewNop()).WithScheduler(b)
	past := time.Now().Add(-time.Hour)

	j, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{Type: "video.create", ScheduledAt: &past})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if j.Status != job.StatusQueued {
		t.Fatalf("status = %s, want QUEUED", j.Status)
	}
	if !b.immediate {
		t.Fatal("a past ScheduledAt must enqueue immediately")
	}
}

func TestEnqueueSchedulingLaneMustMatchPayloadPhase(t *testing.T) {
	t.Parallel()
	b := &scheduledBroker{}
	svc := NewService(b, fixedRetryResolver(3), nil, zap.NewNop())

	_, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:    job.TypeClipRender,
		Payload: map[string]any{"render_phase": "settle"},
		Scheduling: &job.SchedulingDescriptor{
			JobType: job.TypeClipRender, ExecutionLane: "submit",
		},
	})
	if err == nil {
		t.Fatal("mismatched scheduling lane must fail closed")
	}
	if b.created != nil {
		t.Fatal("mismatched scheduling lane must not reach persistence")
	}
}

func TestEnqueuePersistsExplicitSchedulingProjection(t *testing.T) {
	t.Parallel()
	b := &scheduledBroker{}
	svc := NewService(b, fixedRetryResolver(3), nil, zap.NewNop())

	created, err := svc.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:    job.TypeClipRender,
		Payload: map[string]any{"render_phase": "settle"},
		Scheduling: &job.SchedulingDescriptor{
			JobType: job.TypeClipRender, ResourceClass: "render", ExecutionLane: "settle",
		},
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if b.created == nil || b.created.Scheduling.ExecutionLane != "settle" ||
		b.created.Scheduling.ResourceClass != "render" || b.created.Scheduling.AvailableAt.IsZero() {
		t.Fatalf("persisted scheduling descriptor = %+v", b.created)
	}
	if created.Scheduling != b.created.Scheduling {
		t.Fatalf("returned scheduling descriptor = %+v, persisted = %+v", created.Scheduling, b.created.Scheduling)
	}
}

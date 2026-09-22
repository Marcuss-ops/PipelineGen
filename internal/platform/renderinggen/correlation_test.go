package renderinggen

import (
	"context"
	"errors"
	"sync"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// capturingClipQueue records the RenderQueueJob submitted by the executor. The
// production fake (fakeClipQueue) flattens the job into the transport shape, so
// it cannot observe the capability-level parent id.
type capturingClipQueue struct {
	mu   sync.Mutex
	last scriptgen.RenderQueueJob
}

func (q *capturingClipQueue) Submit(_ context.Context, job scriptgen.RenderQueueJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.last = job
	return nil
}

func (q *capturingClipQueue) Get(_ context.Context, _ string) (scriptgen.RenderQueueJob, error) {
	return scriptgen.RenderQueueJob{}, errors.New("not implemented")
}

func (q *capturingClipQueue) parentJobID() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.last.ParentJobID
}

// TestClipRenderExecutorPropagatesRunCorrelation pins that a localized clip
// render reaches the remote queue with the owning run's correlation id, so the
// queue record (GET /jobs/{id}) joins back to the master run.
func TestClipRenderExecutorPropagatesRunCorrelation(t *testing.T) {
	plan := validClipPlan(t)
	queue := &capturingClipQueue{}
	executor, err := NewClipRenderExecutor(queue)
	if err != nil {
		t.Fatal(err)
	}

	ctx := corid.WithCorrelationID(context.Background(), "run-corr-99")
	if err := executor.Submit(ctx, plan); err != nil {
		t.Fatalf("submit clip render: %v", err)
	}
	if got := queue.parentJobID(); got != "run-corr-99" {
		t.Fatalf("clip render job parent_job_id = %q, want the run correlation id", got)
	}

	// Without a correlation id the field stays empty: no fabricated identity.
	if err := executor.Submit(context.Background(), plan); err != nil {
		t.Fatalf("submit clip render without correlation: %v", err)
	}
	if got := queue.parentJobID(); got != "" {
		t.Fatalf("clip render job invented a parent_job_id: %q", got)
	}
}

package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// capturingQueueClient records the last submitted job and reports every job as
// immediately completed, so enqueue paths that wait for a render return.
type capturingQueueClient struct {
	mu      sync.Mutex
	last    RenderQueueJob
	submits int
}

func (c *capturingQueueClient) Submit(_ context.Context, job RenderQueueJob) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = job
	c.submits++
	return nil
}

func (c *capturingQueueClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	return RenderQueueJob{ID: id, State: RenderQueueStateCompleted}, nil
}

func (c *capturingQueueClient) lastJob(t *testing.T) RenderQueueJob {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.submits == 0 {
		t.Fatal("no job was submitted")
	}
	return c.last
}

// TestOverlayRenderQueueJobCarriesRunCorrelation pins the join key between a
// master run and its remote render job: the queue entry must carry the run
// correlation id as parent_job_id, otherwise a slow/failed remote render can
// only be traced by plan revision and never back to the run that asked for it.
func TestOverlayRenderQueueJobCarriesRunCorrelation(t *testing.T) {
	client := &capturingQueueClient{}
	enqueuer, err := NewQueueRenderEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.SetPollInterval(time.Millisecond)

	ctx := corid.WithCorrelationID(context.Background(), "run-corr-42")
	if _, err := enqueuer.EnqueueChrononPlan(ctx, capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatalf("enqueue overlay plan: %v", err)
	}
	if got := client.lastJob(t).ParentJobID; got != "run-corr-42" {
		t.Fatalf("overlay render job parent_job_id = %q, want the run correlation id", got)
	}

	// No correlation id in the context must NOT invent one: the queue then
	// stores an empty parent instead of a fabricated join key.
	if _, err := enqueuer.EnqueueChrononPlan(context.Background(), capoverlay.GoldenOverlayPlanV1()); err != nil {
		t.Fatalf("enqueue overlay plan without correlation: %v", err)
	}
	if got := client.lastJob(t).ParentJobID; got != "" {
		t.Fatalf("overlay render job invented a parent_job_id: %q", got)
	}
}

func TestOverlayPrepareQueueJobCarriesRunCorrelation(t *testing.T) {
	client := &capturingQueueClient{}
	enqueuer, err := NewQueuePrepareEnqueuer(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := corid.WithCorrelationID(context.Background(), "run-corr-7")
	if err := enqueuer.EnqueuePrepare(ctx, prepareTestRequest("plan-corr-7")); err != nil {
		t.Fatalf("enqueue prepare: %v", err)
	}
	if got := client.lastJob(t).ParentJobID; got != "run-corr-7" {
		t.Fatalf("prepare job parent_job_id = %q, want the run correlation id", got)
	}
}

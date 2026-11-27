package renderinggen

import (
	"context"
	"strings"
	"testing"

	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

type fakeCapabilitySource struct {
	capabilities []string
	err          error
}

func (f fakeCapabilitySource) Capabilities(context.Context) ([]string, error) {
	return f.capabilities, f.err
}

// clip.render is Chronon-Vulkan only. The submit must carry an explicit
// gpu_only demand; a nil tri-state derives gpu_preferred and lets a worker
// without the GPU capability claim a job that can only fail at completion.
func TestClipRenderExecutorSubmitCarriesGPUOnlyDemand(t *testing.T) {
	queue := &fakeClipQueue{}
	executor, err := NewClipRenderExecutor(queue)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Submit(context.Background(), validClipPlan(t)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if queue.submittedRequireGPU == nil {
		t.Fatal("submitted job has no RequireGPU demand: the queue would store gpu_preferred")
	}
	if !*queue.submittedRequireGPU {
		t.Fatal("RequireGPU = false, want true (gpu_only)")
	}
}

func TestClipRenderExecutorPreflightRejectsMissingGPU(t *testing.T) {
	queue := &fakeClipQueue{}
	executor, err := NewClipRenderExecutor(queue)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetCapabilitySource(fakeCapabilitySource{capabilities: []string{"overlay", "subtitles"}})

	err = executor.Submit(context.Background(), validClipPlan(t))
	if err == nil {
		t.Fatal("Submit succeeded without a live GPU capability")
	}
	if !strings.Contains(err.Error(), "EXECUTION_POLICY_INCOMPATIBLE") {
		t.Fatalf("error = %v, want EXECUTION_POLICY_INCOMPATIBLE", err)
	}
	if queue.submitted.ID != "" {
		t.Fatalf("queue received job %q after a failed preflight", queue.submitted.ID)
	}
}

func TestClipRenderExecutorPreflightAcceptsGPU(t *testing.T) {
	queue := &fakeClipQueue{}
	executor, err := NewClipRenderExecutor(queue)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetCapabilitySource(fakeCapabilitySource{capabilities: []string{renderqueue.CapabilityGPU}})

	if err := executor.Submit(context.Background(), validClipPlan(t)); err != nil {
		t.Fatalf("Submit with GPU inventory: %v", err)
	}
	if queue.submitted.ID == "" {
		t.Fatal("queue received no job")
	}
}

func TestClipRenderExecutorPreflightFailsClosedOnInventoryError(t *testing.T) {
	queue := &fakeClipQueue{}
	executor, err := NewClipRenderExecutor(queue)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetCapabilitySource(fakeCapabilitySource{err: context.DeadlineExceeded})

	err = executor.Submit(context.Background(), validClipPlan(t))
	if err == nil || !strings.Contains(err.Error(), "EXECUTION_POLICY_INCOMPATIBLE") {
		t.Fatalf("error = %v, want a typed incompatible-policy failure", err)
	}
	if queue.submitted.ID != "" {
		t.Fatal("queue must not receive a job when the inventory cannot be read")
	}
}

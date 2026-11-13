package scriptgeneration

import (
	"context"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

type itemEnqueuerStub struct {
	itemCalls int
	itemRef   RenderReference
	itemErr   error
}

func (e *itemEnqueuerStub) EnqueueChrononPlanItems(context.Context, capmodel.OverlayPlan) (RenderReference, error) {
	e.itemCalls++
	return e.itemRef, e.itemErr
}

func perItemTestPlan(t *testing.T) *capmodel.OverlayPlan {
	t.Helper()
	plan := manifestTestPlan()
	return &plan
}

// TestRenderOverlayPlanAtBoundaryAlwaysUsesPerItemRenderer pins the removal of
// the local timeline shape: even a run that did not request overlays_only must
// submit independent overlay items, never a scene or master canvas.
func TestRenderOverlayPlanAtBoundaryAlwaysUsesPerItemRenderer(t *testing.T) {
	stub := &itemEnqueuerStub{itemRef: RenderReference{JobID: "item-jobs", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(), GenerateRequest{}, perItemTestPlan(t))
	if err != nil {
		t.Fatalf("local overlay boundary: %v", err)
	}
	if stub.itemCalls != 1 {
		t.Fatalf("per-item submissions = %d, want 1", stub.itemCalls)
	}
	if ref.JobID != "item-jobs" {
		t.Fatalf("reference = %q, want the per-item submission", ref.JobID)
	}
}

// TestRenderOverlayPlanAtBoundaryFinalJobStaysPerItem pins that a final job
// also renders its overlay layers per item locally; only the certified item
// artifacts leave PipelineGen.
func TestRenderOverlayPlanAtBoundaryFinalJobStaysPerItem(t *testing.T) {
	stub := &itemEnqueuerStub{itemRef: RenderReference{JobID: "item-jobs", Status: "completed"}}
	runner := &Runner{overlayRenderEnqueuer: stub, log: zap.NewNop()}
	ref, err := runner.renderOverlayPlanAtBoundary(context.Background(),
		GenerateRequest{FinalJob: true, Render: scriptpkg.VideoRenderSpec{OverlaysOnly: true}}, perItemTestPlan(t))
	if err != nil {
		t.Fatalf("final-job local overlay boundary: %v", err)
	}
	if stub.itemCalls != 1 || ref.JobID != "item-jobs" {
		t.Fatalf("calls=%d ref=%q, want the per-item submission", stub.itemCalls, ref.JobID)
	}
}

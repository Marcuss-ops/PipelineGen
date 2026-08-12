package scriptgeneration

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

type captureRenderExecutor struct {
	plan  render.RenderPlan
	calls int
}

func (e *captureRenderExecutor) RenderCanonicalPlan(_ context.Context, plan render.RenderPlan) error {
	e.calls++
	e.plan = plan
	return nil
}

func TestCanonicalRenderEnqueuerForwardsAndValidatesPlan(t *testing.T) {
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationMS: 1000,
		Segments:   []audio.TimelineSegment{{ID: "scene", Index: 0, DurationMS: 1000, Audio: audio.AudioIntent{Mode: audio.AudioSilence}}},
	}
	plan, err := render.Compile(render.CompileInput{JobID: "job-render", Revision: "generation.v1", OutputPath: "final.mp4", FPS: 30, Timeline: timeline})
	if err != nil {
		t.Fatal(err)
	}
	executor := &captureRenderExecutor{}
	enqueuer, err := NewCanonicalRenderEnqueuer(executor)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.Enqueue(context.Background(), GenerateResult{RenderPlan: &plan})
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || executor.plan.PlanSHA256 != plan.PlanSHA256 || ref.JobID != "job-render" || ref.Status != "COMPLETED" {
		t.Fatalf("unexpected forwarding: calls=%d plan=%s ref=%+v", executor.calls, executor.plan.PlanSHA256, ref)
	}
	plan.PlanSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := enqueuer.Enqueue(context.Background(), GenerateResult{RenderPlan: &plan}); err == nil {
		t.Fatal("tampered plan must be rejected before executor")
	}
	if executor.calls != 1 {
		t.Fatal("tampered plan reached executor")
	}
}

package renderinggen

import (
	"context"
	"encoding/json"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

type fakeClipQueue struct {
	submitted queueclient.Job
	result    queueclient.Job
}

func (f *fakeClipQueue) Submit(_ context.Context, job scriptgen.RenderQueueJob) error {
	f.submitted = queueclient.Job{ID: job.ID, JobType: job.JobType, RenderPlan: job.OverlaySpec}
	return nil
}

func (f *fakeClipQueue) Get(_ context.Context, _ string) (scriptgen.RenderQueueJob, error) {
	return scriptgen.RenderQueueJob{State: string(f.result.State), Artifact: toScriptArtifact(f.result.Artifact)}, nil
}

func validClipPlan(t *testing.T) cliprender.ClipRenderPlanV1 {
	t.Helper()
	plan := cliprender.ClipRenderPlanV1{
		Version:    cliprender.PlanVersion,
		RunID:      "clip-1",
		Source:     cliprender.PlanSource{AssetID: "source", Path: "/tmp/source.mp4", SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeNone},
		Output:     cliprender.PlanOutput{ContractID: "VELOX_ASSEMBLY_READY_V1", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1},
		Audio:      cliprender.PlanAudio{Mode: cliprender.AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: "/tmp/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestClipRenderExecutorSubmitsOneCompleteSegment(t *testing.T) {
	q := &fakeClipQueue{result: queueclient.Job{State: queueclient.StateCompleted, Artifact: &queueclient.Artifact{
		ArtifactHash: "artifact-sha", ArtifactURL: "https://store/out.mp4", SizeBytes: 10,
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, DurationUS: 1_000_000,
		Backend: "chronon_vulkan", CopyEligible: true,
	}}}
	executor, err := NewClipRenderExecutor(q)
	if err != nil {
		t.Fatal(err)
	}
	plan := validClipPlan(t)
	outcome, err := executor.Render(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if q.submitted.JobType != "render_segment" || q.submitted.ID != plan.RunID {
		t.Fatalf("submitted job = %+v", q.submitted)
	}
	var submitted cliprender.ClipRenderPlanV1
	if err := json.Unmarshal(q.submitted.RenderPlan, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.RunID != plan.RunID || submitted.PlanSHA256 != plan.PlanSHA256 {
		t.Fatalf("submitted plan = %+v", submitted)
	}
	if outcome.Backend != cliprender.BackendChrononVulkan || outcome.SizeBytes != 10 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestClipRenderExecutorRejectsMissingCertifiedArtifact(t *testing.T) {
	q := &fakeClipQueue{result: queueclient.Job{State: queueclient.StateCompleted}}
	executor, _ := NewClipRenderExecutor(q)
	if _, err := executor.Render(context.Background(), validClipPlan(t)); err == nil {
		t.Fatal("expected missing artifact to fail closed")
	}
}

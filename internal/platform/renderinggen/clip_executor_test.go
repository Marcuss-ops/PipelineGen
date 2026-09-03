package renderinggen

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	sourcePath := t.TempDir() + "/source.mp4"
	sourceBytes := []byte("test source")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(sourceBytes))
	plan := cliprender.ClipRenderPlanV1{
		Version:    cliprender.PlanVersion,
		RunID:      "clip-1",
		DurationMS: 1000,
		Source:     cliprender.PlanSource{AssetID: "source-asset-001", Path: sourcePath, SHA256: sourceHash},
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeNone},
		Output:     cliprender.PlanOutput{ContractID: "VELOX_ASSEMBLY_READY_V1", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1},
		Audio:      cliprender.PlanAudio{Mode: cliprender.AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: t.TempDir() + "/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	return plan
}

// TestClipRenderExecutorSubmitsOverlayPlanV1 verifies the critical contract:
// the submitted JSON carries schema_version="renderinggen.overlay-plan.v1",
// NOT a raw ClipRenderPlanV1 (which would be silently pass-through'd by the
// RenderingGen worker and corrupt the render pipeline).
func TestClipRenderExecutorSubmitsOverlayPlanV1(t *testing.T) {
	artifactBytes := []byte("0123456789")
	artifactHash := fmt.Sprintf("%x", sha256.Sum256(artifactBytes))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(artifactBytes)
	}))
	defer server.Close()
	q := &fakeClipQueue{result: queueclient.Job{State: queueclient.StateCompleted, Artifact: &queueclient.Artifact{
		ArtifactHash: artifactHash, ArtifactURL: server.URL + "/out.mp4", SizeBytes: int64(len(artifactBytes)),
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
		t.Fatalf("submitted job metadata wrong: job_type=%q id=%q", q.submitted.JobType, q.submitted.ID)
	}

	// Decode as the overlay-plan wire type (NOT ClipRenderPlanV1).
	var overlay struct {
		SchemaVersion string `json:"schema_version"`
		PlanID        string `json:"plan_id"`
		VideoID       string `json:"video_id"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		FPSNum        int    `json:"fps_num"`
		FPSDen        int    `json:"fps_den"`
		Source        struct {
			AssetID string `json:"asset_id"`
			SHA256  string `json:"sha256"`
		} `json:"source"`
	}
	if err := json.Unmarshal(q.submitted.RenderPlan, &overlay); err != nil {
		t.Fatalf("submitted plan is not valid JSON: %v", err)
	}
	if overlay.SchemaVersion != SemanticSchema {
		t.Errorf("schema_version = %q, want %q (raw ClipRenderPlanV1 would pass-through to Chronon broken)",
			overlay.SchemaVersion, SemanticSchema)
	}
	if overlay.PlanID != plan.RunID {
		t.Errorf("plan_id = %q, want %q", overlay.PlanID, plan.RunID)
	}
	if overlay.VideoID != plan.Source.AssetID {
		t.Errorf("video_id = %q, want %q", overlay.VideoID, plan.Source.AssetID)
	}
	if overlay.FPSNum != plan.Output.FPSNum || overlay.FPSDen != plan.Output.FPSDen {
		t.Errorf("fps = %d/%d, want %d/%d", overlay.FPSNum, overlay.FPSDen, plan.Output.FPSNum, plan.Output.FPSDen)
	}
	if overlay.Source.SHA256 != plan.Source.SHA256 {
		t.Errorf("source.sha256 = %q, want %q", overlay.Source.SHA256, plan.Source.SHA256)
	}

	// Verify local VPS path is NOT in the submitted JSON.
	if strings.Contains(string(q.submitted.RenderPlan), "/vps/local/source.mp4") {
		t.Error("submitted plan must NOT contain local VPS path — must use hash-addressed object-store key")
	}

	if outcome.Backend != cliprender.BackendChrononVulkan || outcome.SizeBytes != 10 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// TestClipRenderExecutorAssetRefsAreHashAddressed verifies that asset logical
// paths use the content-addressed object-store format, not local VPS paths.
func TestClipRenderExecutorAssetRefsAreHashAddressed(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plan := cliprender.ClipRenderPlanV1{
		Version:    cliprender.PlanVersion,
		RunID:      "clip-asset-test",
		Source:     cliprender.PlanSource{AssetID: "src", Path: "/home/pierone/clips/source.mp4", SHA256: sha},
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeNone},
		Output:     cliprender.PlanOutput{ContractID: "C", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1},
		Audio:      cliprender.PlanAudio{Mode: cliprender.AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: "/tmp/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("no asset refs returned")
	}
	for _, ref := range refs {
		if strings.HasPrefix(ref.LogicalPath, "/") {
			t.Errorf("asset ref LogicalPath %q is a local path — must be hash-addressed (sha256/...)", ref.LogicalPath)
		}
		if !strings.HasPrefix(ref.LogicalPath, "assets/semantic/") {
			t.Errorf("asset ref LogicalPath %q is not a Chronon-mounted semantic path", ref.LogicalPath)
		}
		if !strings.Contains(ref.LogicalPath, "src/source.mp4") {
			t.Errorf("asset ref LogicalPath %q does not contain the source identity", ref.LogicalPath)
		}
	}
}

func TestClipRenderExecutorRejectsMissingCertifiedArtifact(t *testing.T) {
	q := &fakeClipQueue{result: queueclient.Job{State: queueclient.StateCompleted}}
	executor, _ := NewClipRenderExecutor(q)
	if _, err := executor.Render(context.Background(), validClipPlan(t)); err == nil {
		t.Fatal("expected missing artifact to fail closed")
	}
}

func TestClipRenderExecutorRejectsNonChrononArtifact(t *testing.T) {
	q := &fakeClipQueue{result: queueclient.Job{State: queueclient.StateCompleted, Artifact: &queueclient.Artifact{
		ArtifactHash: "artifact-sha", ArtifactURL: "https://store/out.mp4", SizeBytes: 10,
		Backend: "ffmpeg_fallback",
	}}}
	executor, err := NewClipRenderExecutor(q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Render(context.Background(), validClipPlan(t)); err == nil {
		t.Fatal("expected non-Chronon artifact to fail closed")
	}
}

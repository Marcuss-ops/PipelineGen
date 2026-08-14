package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

// renderTestFS is the real-filesystem render.FileSystem adapter for the
// enqueuer tests (kept local so this capability test does not import the
// platform adapter).
type renderTestFS struct{}

func (renderTestFS) Open(path string) (io.ReadCloser, error) { return os.Open(path) }

func (renderTestFS) Size(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

type captureRenderExecutor struct {
	plan  render.RenderPlan
	calls int
}

func (e *captureRenderExecutor) RenderCanonicalPlan(_ context.Context, validated render.ValidatedRenderPlan) error {
	e.calls++
	e.plan = validated.Plan()
	return nil
}

func TestCanonicalRenderEnqueuerForwardsAndValidatesPlan(t *testing.T) {
	path := t.TempDir() + "/clip.mp4"
	contents := []byte("canonical clip")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: 1000000,
		Segments: []audio.TimelineSegment{{ID: "scene", Index: 0, DurationUS: 1000000,
			Video: audio.VideoSegment{AssetID: "clip", SourceInUS: 0, SourceDurationUS: 1000000},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence}}},
	}
	plan, err := render.Compile(render.CompileInput{JobID: "job-render", Revision: "generation.v1", OutputPath: "final.mp4", FPS: 30, Timeline: timeline, Manifest: []render.AssetManifestEntry{{AssetID: "clip", Path: path, SHA256: hex.EncodeToString(sum[:]), FrameCount: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	executor := &captureRenderExecutor{}
	enqueuer, err := NewCanonicalRenderEnqueuer(executor, renderTestFS{})
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

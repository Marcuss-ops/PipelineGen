package scriptgeneration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
)

// fakeRenderQueueClient implements RenderQueueClient in-memory for the
// enqueuer tests. It lets a test pre-seed a job (to exercise the idempotent
// ErrJobExists path) or advance the job state between polls.
type fakeRenderQueueClient struct {
	jobs  map[string]RenderQueueJob
	calls int
}

func newFakeRenderQueueClient() *fakeRenderQueueClient {
	return &fakeRenderQueueClient{jobs: make(map[string]RenderQueueJob)}
}

func (f *fakeRenderQueueClient) Submit(_ context.Context, job RenderQueueJob) error {
	f.calls++
	if _, ok := f.jobs[job.ID]; ok {
		return ErrJobExists
	}
	f.jobs[job.ID] = job
	return nil
}

func (f *fakeRenderQueueClient) Get(_ context.Context, id string) (RenderQueueJob, error) {
	job, ok := f.jobs[id]
	if !ok {
		return RenderQueueJob{}, errors.New("job not found")
	}
	return job, nil
}

// validQueueTestPlan compiles a valid single-segment render plan backed by a
// real temp file so ValidateRenderPlan's manifest re-hashing passes.
func validQueueTestPlan(t *testing.T, jobID string) render.RenderPlan {
	t.Helper()
	path := t.TempDir() + "/clip.mp4"
	contents := []byte("clip content")
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
	plan, err := render.Compile(render.CompileInput{
		JobID:      jobID,
		Revision:   "generation.v1",
		OutputPath: "final.mp4",
		FPS:        30,
		Timeline:   timeline,
		Manifest:   []render.AssetManifestEntry{{AssetID: "clip", Path: path, SHA256: hex.EncodeToString(sum[:]), FrameCount: 100}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestQueueRenderEnqueuerWaitsForArtifact(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueueRenderEnqueuer(client, renderTestFS{})
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond

	plan := validQueueTestPlan(t, "job-render-1")
	// Pre-seed a completed job with a certified artifact.
	client.jobs[plan.JobID] = RenderQueueJob{
		ID:    plan.JobID,
		State: "completed",
		Artifact: &RenderArtifact{
			ID:                 "art-1",
			URL:                "https://store/overlay.mp4",
			SHA256:             "ab",
			ProfileID:          "velox-copy-v1",
			CopyEligible:       true,
			DurationUS:         1000000,
			FirstFrameKeyframe: true,
		},
	}

	ref, err := enqueuer.Enqueue(context.Background(), GenerateResult{RenderPlan: &plan})
	if err != nil {
		t.Fatal(err)
	}
	if ref.JobID != plan.JobID || ref.Status != "COMPLETED" {
		t.Fatalf("unexpected reference: %+v", ref)
	}
	if ref.Artifact == nil || ref.Artifact.ID != "art-1" || !ref.Artifact.CopyEligible || ref.Artifact.ProfileID != "velox-copy-v1" {
		t.Fatalf("artifact not propagated: %+v", ref.Artifact)
	}
}

func TestQueueRenderEnqueuerPropagatesFailure(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueueRenderEnqueuer(client, renderTestFS{})
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond

	plan := validQueueTestPlan(t, "job-render-2")
	client.jobs[plan.JobID] = RenderQueueJob{ID: plan.JobID, State: "failed", FailReason: "chronon exploded"}

	if _, err := enqueuer.Enqueue(context.Background(), GenerateResult{RenderPlan: &plan}); err == nil {
		t.Fatal("expected failure to propagate")
	}
}

func TestQueueRenderEnqueuerIdempotentReplay(t *testing.T) {
	client := newFakeRenderQueueClient()
	enqueuer, err := NewQueueRenderEnqueuer(client, renderTestFS{})
	if err != nil {
		t.Fatal(err)
	}
	enqueuer.pollInterval = time.Millisecond

	plan := validQueueTestPlan(t, "job-render-3")
	// First submit records the job; the enqueuer then waits and sees the
	// already-completed state.
	client.jobs[plan.JobID] = RenderQueueJob{ID: plan.JobID, State: "completed"}

	ref, err := enqueuer.Enqueue(context.Background(), GenerateResult{RenderPlan: &plan})
	if err != nil {
		t.Fatal(err)
	}
	if ref.JobID != plan.JobID || ref.Status != "COMPLETED" {
		t.Fatalf("unexpected reference: %+v", ref)
	}
	// The pre-seeded job made Submit return ErrJobExists; the enqueuer must
	// still have proceeded (idempotent replay), so Submit was attempted once.
	if client.calls != 1 {
		t.Fatalf("expected one submit attempt, got %d", client.calls)
	}
}

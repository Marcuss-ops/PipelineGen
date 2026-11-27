package renderinggen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	renderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

// fakeBatchSubmitter records the atomic family submission.
type fakeBatchSubmitter struct {
	family   []renderqueue.RenderQueueJob
	err      error
	jobs     map[string]renderqueue.RenderQueueJob
	artifact *renderqueue.RenderArtifact
}

func (f *fakeBatchSubmitter) SubmitBatch(_ context.Context, family []renderqueue.RenderQueueJob) error {
	f.family = append([]renderqueue.RenderQueueJob(nil), family...)
	return f.err
}

func (f *fakeBatchSubmitter) Submit(_ context.Context, job renderqueue.RenderQueueJob) error {
	if f.jobs == nil {
		f.jobs = make(map[string]renderqueue.RenderQueueJob)
	}
	job.State = "completed"
	job.Artifact = f.artifact
	if job.Artifact == nil {
		job.Artifact = &renderqueue.RenderArtifact{
			Kind: "prepared_render_package", StorageKey: strings.Repeat("a", 64),
			SHA256: strings.Repeat("a", 64), ChrononVersion: "0.1.0", ProfileID: "profile-1",
		}
	}
	f.jobs[job.ID] = job
	return nil
}

func TestChunkProducerRejectsInvalidPreparedPackageArtifactBeforeFamilySubmit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		storage  string
		sha      string
		profile  string
		wantText string
	}{
		{name: "non-canonical storage key", storage: strings.Repeat("g", 64), sha: strings.Repeat("g", 64), profile: "profile-1", wantText: "incomplete package identity"},
		{name: "storage and digest differ", storage: strings.Repeat("a", 64), sha: strings.Repeat("b", 64), profile: "profile-1", wantText: "conflicting package storage key and sha256"},
		{name: "missing output profile", storage: strings.Repeat("a", 64), sha: strings.Repeat("a", 64), wantText: "incomplete package identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := validClipPlan(t)
			plan.DurationMS = int64((2 * time.Minute) / time.Millisecond)
			if err := plan.Seal(); err != nil {
				t.Fatalf("seal long clip plan: %v", err)
			}
			rec := &storeRecorder{present: map[string][]byte{}, putBodies: map[string][]byte{}}
			srv := httptest.NewServer(http.HandlerFunc(rec.handler))
			defer srv.Close()
			t.Setenv("RENDERINGGEN_STORE_URL", srv.URL)
			submitter := &fakeBatchSubmitter{artifact: &renderqueue.RenderArtifact{
				Kind: "prepared_render_package", StorageKey: tc.storage,
				SHA256: tc.sha, ChrononVersion: "0.1.0", ProfileID: tc.profile,
			}}
			producer, err := NewChunkProducer(submitter)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := producer.Submit(context.Background(), plan, 2, 48); err == nil || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("Submit error = %v, want %q", err, tc.wantText)
			}
			if len(submitter.family) != 0 {
				t.Fatalf("submitted %d family jobs after rejecting the prepared package", len(submitter.family))
			}
		})
	}
}

func (f *fakeBatchSubmitter) Get(_ context.Context, id string) (renderqueue.RenderQueueJob, error) {
	return f.jobs[id], nil
}

func (f *fakeBatchSubmitter) WaitTerminal(ctx context.Context, id string) (renderqueue.RenderQueueJob, error) {
	return f.Get(ctx, id)
}

// TestChunkProducerChildrenAreVideoOnlyAndAnchorOwnsAudio pins the
// audio-aware chunk family shape (the sblocco of the dormant chunk policy):
// every child carries the VIDEO-ONLY mapping of the sealed plan — no audio
// block, so it renders a silent copy-eligible segment the packet-copy assembly
// can concatenate at keyframe boundaries — while the anchor carries the FULL
// mapping (audio block + source), which makes it the family's SINGLE audio
// owner: the finalizer muxes the source stream exactly once over the
// assembled video.
func TestChunkProducerChildrenAreVideoOnlyAndAnchorOwnsAudio(t *testing.T) {
	plan := validClipPlan(t)
	plan.DurationMS = int64((2 * time.Minute) / time.Millisecond)
	if err := plan.Seal(); err != nil {
		t.Fatalf("seal long clip plan: %v", err)
	}

	rec := &storeRecorder{present: map[string][]byte{}, putBodies: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()
	t.Setenv("RENDERINGGEN_STORE_URL", srv.URL)

	submitter := &fakeBatchSubmitter{}
	producer, err := NewChunkProducer(submitter)
	if err != nil {
		t.Fatal(err)
	}
	set, err := producer.Submit(context.Background(), plan, 2, 48)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if len(set.Chunks) != 2 {
		t.Fatalf("chunk set = %d windows, want 2", len(set.Chunks))
	}
	if len(submitter.family) != len(set.Chunks)+1 {
		t.Fatalf("family = %d jobs, want %d children + 1 anchor", len(submitter.family), len(set.Chunks))
	}

	// The anchor is assembly-only (no frame range) and owns the audio block.
	anchor := submitter.family[0]
	if anchor.PreparedRenderPackage != nil {
		t.Fatalf("assembly anchor must not reference a package compiled from the child plan: %+v", anchor.PreparedRenderPackage)
	}
	if anchor.ID != set.AnchorJobID() {
		t.Fatalf("anchor job ID = %q, want %q", anchor.ID, set.AnchorJobID())
	}
	if anchor.FrameRange != nil {
		t.Fatalf("anchor must be assembly-only (no frame range): %+v", anchor.FrameRange)
	}
	if anchor.RequireGPU == nil || !*anchor.RequireGPU {
		t.Fatalf("anchor RequireGPU = %v, want true (gpu_only): the anchor muxes on Chronon Vulkan", anchor.RequireGPU)
	}
	var anchorPlan struct {
		Audio *struct {
			Mode string `json:"mode"`
		} `json:"audio"`
		Source struct {
			SHA256 string `json:"sha256"`
		} `json:"source"`
	}
	if err := json.Unmarshal(anchor.OverlaySpec, &anchorPlan); err != nil {
		t.Fatalf("anchor plan does not decode: %v", err)
	}
	if anchorPlan.Audio == nil || anchorPlan.Audio.Mode != string(plan.Audio.Mode) {
		t.Fatalf("anchor must carry the audio block (family audio owner): got %+v", anchorPlan.Audio)
	}
	if anchorPlan.Source.SHA256 != plan.Source.SHA256 {
		t.Fatalf("anchor source = %q, want %q (the finalizer muxes audio from it)",
			anchorPlan.Source.SHA256, plan.Source.SHA256)
	}

	// Every child is video-only: the audio block is ABSENT, the source travels,
	// and the window is anchor-owned and frame-ranged.
	for i, child := range submitter.family[1:] {
		if child.RequireGPU == nil || !*child.RequireGPU {
			t.Fatalf("chunk %d RequireGPU = %v, want true (gpu_only)", i, child.RequireGPU)
		}
		if child.PreparedRenderPackage == nil || child.PreparedRenderPackage.PackageID != strings.Repeat("a", 64) {
			t.Fatalf("chunk %d package reference = %+v, want the shared prepared package", i, child.PreparedRenderPackage)
		}
		if i > 0 && child.PreparedRenderPackage != submitter.family[1].PreparedRenderPackage {
			t.Fatalf("chunk %d package reference differs from the family package", i)
		}
		var childPlan struct {
			Audio           json.RawMessage `json:"audio"`
			OutputProfileID string          `json:"output_profile_id"`
			Source          struct {
				SHA256 string `json:"sha256"`
			} `json:"source"`
		}
		if err := json.Unmarshal(child.OverlaySpec, &childPlan); err != nil {
			t.Fatalf("chunk %d plan does not decode: %v", i, err)
		}
		if len(childPlan.Audio) != 0 {
			t.Fatalf("chunk %d must be video-only (no audio block), got: %s", i, childPlan.Audio)
		}
		if childPlan.OutputProfileID != kernelmedia.OverlayVideoOnlyProfileID {
			t.Fatalf("chunk %d profile = %q, want %q (a silent chunk cannot claim an audio-bearing assembly profile)",
				i, childPlan.OutputProfileID, kernelmedia.OverlayVideoOnlyProfileID)
		}
		if childPlan.Source.SHA256 != plan.Source.SHA256 {
			t.Fatalf("chunk %d lost the source asset: %q", i, childPlan.Source.SHA256)
		}
		if child.ParentJobID != set.AnchorJobID() {
			t.Fatalf("chunk %d parent = %q, want anchor %q", i, child.ParentJobID, set.AnchorJobID())
		}
		if child.FrameRange == nil {
			t.Fatalf("chunk %d must carry its frame window: %+v", i, child)
		}
	}
}

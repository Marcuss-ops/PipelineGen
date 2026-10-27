package renderinggen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// fakeBatchSubmitter records the atomic family submission.
type fakeBatchSubmitter struct {
	family []scriptgen.RenderQueueJob
	err    error
}

func (f *fakeBatchSubmitter) SubmitBatch(_ context.Context, family []scriptgen.RenderQueueJob) error {
	f.family = append([]scriptgen.RenderQueueJob(nil), family...)
	return f.err
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
	if anchor.ID != set.AnchorJobID() {
		t.Fatalf("anchor job ID = %q, want %q", anchor.ID, set.AnchorJobID())
	}
	if anchor.FrameRange != nil {
		t.Fatalf("anchor must be assembly-only (no frame range): %+v", anchor.FrameRange)
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
		var childPlan struct {
			Audio  json.RawMessage `json:"audio"`
			Source struct {
				SHA256 string `json:"sha256"`
			} `json:"source"`
		}
		if err := json.Unmarshal(child.OverlaySpec, &childPlan); err != nil {
			t.Fatalf("chunk %d plan does not decode: %v", i, err)
		}
		if len(childPlan.Audio) != 0 {
			t.Fatalf("chunk %d must be video-only (no audio block), got: %s", i, childPlan.Audio)
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

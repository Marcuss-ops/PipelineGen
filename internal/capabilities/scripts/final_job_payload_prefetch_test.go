package scriptgeneration

import (
	"context"
	"sync"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// prefetchRecordingResolver is the stock-folder resolver with the optional
// warm-up path: it records the identities the builder asks it to warm.
type prefetchRecordingResolver struct {
	finalJobPayloadResolver
	mu   sync.Mutex
	seen []string
}

func (r *prefetchRecordingResolver) PrefetchFinalJobStockAssets(_ context.Context, ids []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, ids...)
}

func (r *prefetchRecordingResolver) prefetched() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

// TestBuildFinalJobPayloadsDoesNotPrefetchTheFolderItListed pins the current
// contract: final-job stock is passed by Drive reference, so building the
// payload must not eagerly resolve or stage the folder's sources.
func TestBuildFinalJobPayloadsDoesNotPrefetchTheFolderItListed(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 2_000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 2_000_000}}},
		FinalAudio:        certifiedFinalAudio(2000),
		Scenes: []Scene{{
			ID:    "scene-1",
			Clips: []*ClipReference{{ID: "stock-clip", AsStock: true, DurationUS: 10_000_000}},
			Stock: &scriptpkg.StockBinding{FolderID: "folder-1"},
		}},
	}
	resolver := &prefetchRecordingResolver{}
	if _, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, resolver); err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	seen := resolver.prefetched()
	if len(seen) != 0 {
		t.Fatalf("prefetched = %v, want no eager Drive warm-up; stock is passed by reference", seen)
	}
}

// TestBuildFinalJobPayloadsSkipsPrefetchForResolversWithoutIt pins the absence
// contract: a resolver that does not offer the warm-up keeps the serial handoff
// byte for byte, and the payload is identical.
func TestBuildFinalJobPayloadsSkipsPrefetchForResolversWithoutIt(t *testing.T) {
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 2_000_000, Segments: []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 2_000_000}}},
		FinalAudio:        certifiedFinalAudio(2000),
		Scenes: []Scene{{
			ID:    "scene-1",
			Clips: []*ClipReference{{ID: "stock-clip", AsStock: true, DurationUS: 10_000_000}},
			Stock: &scriptpkg.StockBinding{FolderID: "folder-1"},
		}},
	}
	pre, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{Title: "A story", SourceLanguage: "pt"}, result, finalJobPayloadResolver{})
	if err != nil {
		t.Fatalf("BuildFinalJobPayloads: %v", err)
	}
	if plan := decodeFinalJobPlanForTest(t, pre); len(plan.VideoTracks) != 1 || len(plan.VideoTracks[0].Segments) != 1 {
		t.Fatalf("serial resolver changed the payload: %+v", plan.VideoTracks)
	}
}

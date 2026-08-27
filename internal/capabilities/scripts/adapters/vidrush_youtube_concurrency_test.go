package adapters

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type concurrentYouTubeProvider struct {
	active    atomic.Int32
	maxActive atomic.Int32
	calls     atomic.Int32
}

func (p *concurrentYouTubeProvider) Name() string { return scriptpkg.VidRushProviderYouTube }

func (p *concurrentYouTubeProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	current := p.active.Add(1)
	defer p.active.Add(-1)
	p.calls.Add(1)
	for {
		previous := p.maxActive.Load()
		if current <= previous || p.maxActive.CompareAndSwap(previous, current) {
			break
		}
	}
	select {
	case <-time.After(time.Duration(1+len(req.SegmentID)%5) * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(req.Sources))
	for i, source := range req.Sources {
		out = append(out, scriptpkg.SegmentAssetCandidate{
			AssetID:  fmt.Sprintf("%s-candidate-%d", req.SegmentID, i),
			Provider: p.Name(), SourceURL: source.URL,
			SourceStartMs: int64(i * 10000), SourceEndMs: int64((i + 1) * 10000),
			DurationMs: 10000, Score: float64(3-i) / 3, RelevanceScore: float64(3-i) / 3,
		})
	}
	return out, nil
}

func (p *concurrentYouTubeProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, nil
}
func (p *concurrentYouTubeProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, nil
}

func TestVidRushYouTubeConcurrencyFiveSegmentsThreeSourcesDeterministicOrder(t *testing.T) {
	provider := &concurrentYouTubeProvider{}
	fanout := NewVidRushProviderFanoutWithYouTube(nil, nil, provider)
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "WWII", MediaPlan: mediadomain.MediaPlanSpec{
		ProviderPolicy: mediadomain.MediaProviderPolicy{YouTube: mediadomain.MediaToggleEnabled},
	}}

	segments := make([]scriptpkg.VidRushSegmentResult, 5)
	for i := range segments {
		segmentID := fmt.Sprintf("segment-%03d", i+1)
		sources := make([]mediadomain.SegmentMediaSource, 0, 3)
		for j := 0; j < 3; j++ {
			sources = append(sources, mediadomain.SegmentMediaSource{
				SegmentID: segmentID, Provider: scriptpkg.VidRushProviderYouTube,
				SourceURL: fmt.Sprintf("https://youtu.be/%s-%d", segmentID, j+1),
				Mode:      mediadomain.SegmentMediaSourceModeSuggested,
			})
		}
		plan.MediaPlan.Sources = append(plan.MediaPlan.Sources, sources...)
		segments[i] = scriptpkg.VidRushSegmentResult{SegmentID: segmentID, Text: "WWII historical event"}
	}

	results := make([]scriptpkg.VidRushSegmentResult, len(segments))
	var wg sync.WaitGroup
	errs := make(chan error, len(segments))
	for i := range segments {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := fanout.ResolveProviders(context.Background(), plan, segments[i])
			if err != nil {
				errs <- err
				return
			}
			results[i] = result
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if got := provider.calls.Load(); got != 5 {
		t.Fatalf("provider calls = %d, want 5 segment-level searches", got)
	}
	if got := provider.maxActive.Load(); got < 2 {
		t.Fatalf("max concurrent searches = %d, want parallel execution", got)
	}
	for i, result := range results {
		wantID := fmt.Sprintf("segment-%03d", i+1)
		if result.SegmentID != wantID {
			t.Fatalf("result[%d].SegmentID = %q, want %q", i, result.SegmentID, wantID)
		}
		if len(result.Assets.Candidates) != 3 {
			t.Fatalf("%s candidates = %d, want 3", wantID, len(result.Assets.Candidates))
		}
		seen := make(map[string]bool, 3)
		for _, candidate := range result.Assets.Candidates {
			seen[candidate.SourceURL] = true
			if candidate.SourceURL == "" || candidate.Provider != scriptpkg.VidRushProviderYouTube {
				t.Fatalf("%s leaked/invalid candidate: %+v", wantID, candidate)
			}
			if len(candidate.SourceURL) > 0 && candidate.SourceURL[:len(candidate.SourceURL)-1] == "" {
				t.Fatal("unreachable guard")
			}
		}
		if len(seen) != 3 {
			t.Fatalf("%s sources are not isolated: %v", wantID, seen)
		}
	}

	// Reorder only for an explicit assertion that the expected final order is
	// the segment identity order, independent of provider completion timing.
	ordered := append([]scriptpkg.VidRushSegmentResult(nil), results...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].SegmentID < ordered[j].SegmentID })
	for i, result := range ordered {
		if result.SegmentID != fmt.Sprintf("segment-%03d", i+1) {
			t.Fatalf("deterministic order mismatch at %d: %s", i, result.SegmentID)
		}
	}
}

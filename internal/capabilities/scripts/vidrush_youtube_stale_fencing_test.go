package scriptgeneration

import (
	"context"
	"sync"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type youtubeFencingEnricher struct {
	mu      sync.Mutex
	release chan struct{}
	calls   []scriptpkg.SpecScene
}

func (e *youtubeFencingEnricher) Enrich(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, scene)
	e.mu.Unlock()
	select {
	case <-e.release:
	case <-ctx.Done():
		return scriptpkg.VidRushSegmentResult{}, ctx.Err()
	}
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID, SceneID: scene.ID, Position: scene.Index,
		Text: scene.Text, TextHash: SceneTextHash(scene.Text),
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{{
			AssetID: "youtube-" + scene.ID, Provider: scriptpkg.VidRushProviderYouTube,
			SourceURL:     "https://www.youtube.com/watch?v=" + scene.ID,
			SourceStartMs: 151000, SourceEndMs: 161000, DurationMs: 10000,
		}}},
	}, nil
}

func TestVidRushYouTubeFencesStaleSceneRevisionResult(t *testing.T) {
	enricher := &youtubeFencingEnricher{release: make(chan struct{})}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 2)
	commit(t, coordinator, "run-youtube", "scene-1", 0, "Poland invasion", 1)
	commit(t, coordinator, "run-youtube", "scene-1", 0, "Poland invasion", 2)
	close(enricher.release)

	results, err := coordinator.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Text != "Poland invasion" {
		t.Fatalf("results = %+v, want only latest revision", results)
	}
	if coordinator.StaleResults() != 1 {
		t.Fatalf("stale results = %d, want 1", coordinator.StaleResults())
	}
}

func TestVidRushYouTubeFencesStaleTextHashResult(t *testing.T) {
	enricher := &youtubeFencingEnricher{release: make(chan struct{})}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 2)
	commit(t, coordinator, "run-youtube", "scene-1", 0, "Old paragraph", 1)
	commit(t, coordinator, "run-youtube", "scene-1", 0, "New paragraph", 2)
	close(enricher.release)

	results, err := coordinator.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Text != "New paragraph" || results[0].TextHash != SceneTextHash("New paragraph") {
		t.Fatalf("results = %+v, want only new text hash", results)
	}
	if results[0].Assets.Candidates[0].Provider != scriptpkg.VidRushProviderYouTube {
		t.Fatalf("latest result lost YouTube candidate: %+v", results[0].Assets.Candidates)
	}
	if coordinator.StaleResults() != 1 {
		t.Fatalf("stale results = %d, want 1", coordinator.StaleResults())
	}
}

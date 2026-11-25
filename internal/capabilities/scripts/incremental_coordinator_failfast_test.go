// Package scriptgeneration — incremental_coordinator_failfast_test.go pins the
// producer-side image gate: a scene with zero usable images must fail as soon
// as it is materialized, not after the whole run has paid for TTS and render.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// zeroImageEnricher returns a valid scene result with no image assets. It can
// hold one nominated scene so the test can prove the barrier does not wait for
// every enrichment after the early failure.
type zeroImageEnricher struct {
	mu           sync.Mutex
	blockSceneID string
	release      chan struct{}
	entered      chan struct{}
}

func (e *zeroImageEnricher) Enrich(ctx context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	if scene.ID == e.blockSceneID {
		if e.entered != nil {
			close(e.entered)
		}
		select {
		case <-e.release:
		case <-ctx.Done():
			return scriptpkg.VidRushSegmentResult{}, ctx.Err()
		}
	}
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID,
		SceneID:   scene.ID,
		Position:  scene.Index,
		Text:      scene.Text,
		TextHash:  SceneTextHash(scene.Text),
		Insights:  scriptpkg.SegmentInsights{SegmentID: scene.ID, TextHash: SceneTextHash(scene.Text)},
	}, nil
}

func TestIncrementalCoordinator_FailsFastOnZeroImageScene(t *testing.T) {
	enricher := &zeroImageEnricher{
		blockSceneID: "scene-1",
		release:      make(chan struct{}),
		entered:      make(chan struct{}),
	}
	coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 2)
	coordinator.SetImageFanoutSpec(mediacert.Spec{
		Segments:         2,
		ImagesPerSegment: 3,
		SegmentsExpected: []mediacert.SpecSegment{{ID: "scene-0"}, {ID: "scene-1"}},
	})
	defer close(enricher.release)

	require.NoError(t, coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
		RunID: "run-fail-fast", SceneID: "scene-0", SceneIndex: 0,
		Text: "No image scene", TextHash: SceneTextHash("No image scene"), Revision: 1, Language: "en",
	}))
	require.NoError(t, coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
		RunID: "run-fail-fast", SceneID: "scene-1", SceneIndex: 1,
		Text: "Blocked scene", TextHash: SceneTextHash("Blocked scene"), Revision: 1, Language: "en",
	}))

	select {
	case <-enricher.entered:
	case <-time.After(time.Second):
		t.Fatal("blocked enrichment did not start")
	}
	require.Eventually(t, func() bool { return coordinator.EarlyFailure() != nil }, time.Second, 10*time.Millisecond)
	early := coordinator.EarlyFailure()
	require.Error(t, early)
	assert.Contains(t, early.Error(), "IMAGE FANOUT")

	// The next commit must stop the generation stream immediately.
	err := coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
		RunID: "run-fail-fast", SceneID: "scene-2", SceneIndex: 2,
		Text: "Never generated", TextHash: SceneTextHash("Never generated"), Revision: 1, Language: "en",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "IMAGE FANOUT")

	// The barrier must return the deterministic failure without waiting for
	// scene-1, which is still blocked in enrichment.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = coordinator.WaitForVidRush(ctx, "run-fail-fast")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "IMAGE FANOUT")
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(started), 150*time.Millisecond)
}

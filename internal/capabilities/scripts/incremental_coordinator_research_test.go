// Package scriptgeneration — incremental_coordinator_research_test.go pins the
// SetSegmentResearcher seam. No SegmentResearcher implementation ships in this
// repository, so these tests are the only callers: they certify that the stage
// is a safe no-op when unwired and that a wired researcher's report lands on the
// segment insights before the provider fanout.
package scriptgeneration

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// fakeSegmentResearcher records each research call and returns one source so
// the merge into Insights.ResearchSources is observable.
type fakeSegmentResearcher struct {
	mu      sync.Mutex
	calls   int
	lastSeg string
}

func (f *fakeSegmentResearcher) ResearchSegment(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (*scriptpkg.ResearchReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastSeg = segment.SegmentID
	return &scriptpkg.ResearchReport{
		Status: "ok",
		Sources: []scriptpkg.ResearchWebSource{{
			ID:    "src-1",
			Title: "Primary source",
			URL:   "https://example.com/primary",
		}},
	}, nil
}

func (f *fakeSegmentResearcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestIncrementalCoordinator_ResearchStageRunsOnlyWhenResearcherIsWired(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}

	t.Run("unwired", func(t *testing.T) {
		coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 1)

		commit(t, coordinator, "run-research-none", "scene-0", 0, "First scene text", 1)

		result, err := coordinator.WaitForScene(context.Background(), "run-research-none", 0)
		require.NoError(t, err)
		assert.Empty(t, result.Insights.ResearchSources,
			"without a researcher the stage must leave the insights untouched")
	})

	t.Run("wired", func(t *testing.T) {
		researcher := &fakeSegmentResearcher{}
		coordinator := NewVidRushIncrementalCoordinator(enricher, nil, 1)
		coordinator.SetSegmentResearcher(researcher)

		commit(t, coordinator, "run-research-wired", "scene-0", 0, "First scene text", 1)

		result, err := coordinator.WaitForScene(context.Background(), "run-research-wired", 0)
		require.NoError(t, err)
		require.Equal(t, 1, researcher.callCount(), "each committed scene is researched exactly once")
		assert.Equal(t, "scene-0", researcher.lastSeg)
		require.Len(t, result.Insights.ResearchSources, 1)
		assert.Equal(t, "https://example.com/primary", result.Insights.ResearchSources[0].URL)
	})
}

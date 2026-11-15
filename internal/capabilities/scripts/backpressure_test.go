package scriptgeneration

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// recordingMaterializer records materialize calls to prove the stage is wired
// with its own bounded concurrency.
type recordingMaterializer struct {
	mu    sync.Mutex
	calls int
}

func (m *recordingMaterializer) Materialize(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return segment, nil
}

func (m *recordingMaterializer) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestCoordinator_BackpressureRunsAllThreeStages(t *testing.T) {
	enricher := &fakeSegmentEnricher{errs: map[string]error{}}
	resolver := &fakeSegmentProviderResolver{}
	materializer := &recordingMaterializer{}
	coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(enricher, nil, VidRushBackpressure{
		ExtractionLimit: 1, ProviderSearchLimit: 2, MaterializationLimit: 1,
	})
	coordinator.SetSegmentProviderResolver(resolver)
	coordinator.SetSegmentMaterializer(materializer)

	commit(t, coordinator, "run-1", "scene-0", 0, "First scene text", 1)
	commit(t, coordinator, "run-1", "scene-1", 1, "Second scene text", 1)

	results, err := coordinator.WaitForVidRush(context.Background(), "run-1")
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, 2, enricher.callCount(), "extraction must run once per scene")
	assert.Equal(t, 2, resolver.callCount(), "provider search must run once per scene")
	assert.Equal(t, 2, materializer.callCount(), "materialization must run once per scene")
}

package scriptgeneration

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type measuredSegmentEnricher struct {
	active atomic.Int32
	max    atomic.Int32
}

func (e *measuredSegmentEnricher) Enrich(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	active := e.active.Add(1)
	defer e.active.Add(-1)
	for old := e.max.Load(); active > old && !e.max.CompareAndSwap(old, active); old = e.max.Load() {
	}
	// A fixed delay makes the overlap observable without coupling the assertion
	// to scheduler timing or a wall-clock speed threshold.
	time.Sleep(20 * time.Millisecond)
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID,
		SceneID:   scene.ID,
		Position:  scene.Index,
		Text:      scene.Text,
		TextHash:  SceneTextHash(scene.Text),
		Insights: scriptpkg.SegmentInsights{
			SegmentID: scene.ID,
			TextHash:  SceneTextHash(scene.Text),
			Entities:  []scriptpkg.ExtractedEntity{{Value: "entity-" + scene.ID, Type: "PERSON"}},
		},
	}, nil
}

func TestIncrementalCoordinatorSerialAndConcurrentExtractionMergeIdentically(t *testing.T) {
	run := func(limit int) ([]scriptpkg.VidRushSegmentResult, int32, time.Duration) {
		enricher := &measuredSegmentEnricher{}
		backpressure := DefaultVidRushBackpressure()
		backpressure.ExtractionLimit = limit
		coordinator := NewVidRushIncrementalCoordinatorWithBackpressure(enricher, nil, backpressure)
		started := time.Now()
		for i := 0; i < 8; i++ {
			text := "Scene text " + string(rune('A'+i))
			require.NoError(t, coordinator.OnSceneCommitted(context.Background(), SceneCommitted{
				RunID: "serial-concurrent", SceneID: "scene-" + string(rune('0'+i)),
				SceneIndex: i, Text: text, TextHash: SceneTextHash(text), Revision: 1,
			}))
		}
		results, err := coordinator.Wait(context.Background())
		elapsed := time.Since(started)
		require.NoError(t, err)
		return results, enricher.max.Load(), elapsed
	}

	serial, serialMax, serialElapsed := run(1)
	parallel, parallelMax, parallelElapsed := run(4)
	serialJSON, err := json.Marshal(serial)
	require.NoError(t, err)
	parallelJSON, err := json.Marshal(parallel)
	require.NoError(t, err)
	require.Equal(t, string(serialJSON), string(parallelJSON), "worker width must not change extracted result bytes")
	require.EqualValues(t, 1, serialMax, "serial extraction must use one worker")
	require.Greater(t, parallelMax, int32(1), "parallel extraction must overlap scene work")
	t.Logf("synthetic eight-scene extraction: serial=%s parallel=%s max_parallel=%d", serialElapsed, parallelElapsed, parallelMax)
}

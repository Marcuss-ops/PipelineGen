package stockpipeline

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProductionStockOrchestratorRejectsImplicitWriter(t *testing.T) {
	_, err := newProductionStockOrchestrator(OrchestratorConfig{}, ProductionStockPipelineDeps{
		Planner:  resumeStubPlanner{},
		Stager:   resumeStubStager{},
		Cutter:   fakeSucceedingCutter{},
		Renderer: noopRenderer{},
		Builder:  stockManifestBuilder{},
		// Writer is intentionally omitted: production must not inherit
		// NewTestStockOrchestrator's noop writer.
	})
	require.ErrorIs(t, err, ErrProductionWriterMissing)
}

func TestNewTestStockOrchestratorOwnsFixtureDefaults(t *testing.T) {
	pipeline := NewTestStockOrchestrator(
		OrchestratorConfig{},
		resumeStubPlanner{},
		resumeStubStager{},
		fakeSucceedingCutter{},
		noopRenderer{},
	)

	require.NotNil(t, pipeline)
	require.NotNil(t, pipeline.stepStore)
	require.IsType(t, noopWriter{}, pipeline.writer)
	require.IsType(t, noopProjection{}, pipeline.projection)
}

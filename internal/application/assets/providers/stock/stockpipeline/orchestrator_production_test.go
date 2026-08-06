package stockpipeline

import (
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	"github.com/stretchr/testify/require"
)

func TestNewProductionStockOrchestratorRejectsImplicitWriter(t *testing.T) {
	_, err := NewProductionStockOrchestrator(OrchestratorConfig{}, ProductionStockPipelineDeps{
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

func TestNewProductionStockOrchestratorAcceptsCompleteDependencyGraph(t *testing.T) {
	pipeline, err := NewProductionStockOrchestrator(OrchestratorConfig{JobId: "production-fixture"}, ProductionStockPipelineDeps{
		Planner:             resumeStubPlanner{},
		Stager:              resumeStubStager{},
		Cutter:              fakeSucceedingCutter{},
		Renderer:            noopRenderer{},
		Builder:             stockManifestBuilder{},
		Writer:              noopWriter{},
		Projection:          noopProjection{},
		StepStore:           steps.NewInMemoryStore(),
		ArtifactPreparation: &recordingArtifactPreparation{},
		JobFinalizer:        stubJobFinalizer{},
		SourceProbe:         handleJobSourceProbe{},
		BatchRepository:     noopBatchRepository{},
		LocalFS:             newRealishFakeLocalFS(),
		Logger:              zap.NewNop(),
	})
	require.NoError(t, err)
	require.NotNil(t, pipeline)
	require.Equal(t, "production-fixture", pipeline.cfg.JobId)
	require.NotNil(t, pipeline.writer)
	require.NotNil(t, pipeline.projection)
	require.NotNil(t, pipeline.sourceProbe)
	require.NotNil(t, pipeline.batchRepository)
	require.NotNil(t, pipeline.localFS)
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

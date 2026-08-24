package ingest

import "github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"

// ResilienceDeps bundles test-only resilience ports for the fixture
// orchestrator. Production composition must use ProductionStockPipelineDeps
// and NewProductionStockOrchestrator instead.
type ResilienceDeps struct {
	Builder    ManifestBuilder
	Writer     TransactionalAssetWriter
	Projection ProjectionPort
}

// NewOrchestratorWithResilience builds a test fixture with selectively
// overridden resilience ports. Nil fields retain NewTestStockOrchestrator's
// fixture defaults; this helper is intentionally compiled only with tests.
func NewOrchestratorWithResilience(
	cfg OrchestratorConfig,
	planner ClipPlanner,
	stager acquisition.SourceStager,
	cutter VideoCutter,
	renderer StockRenderer,
	resilience ResilienceDeps,
) *Orchestrator {
	o := NewTestStockOrchestrator(cfg, planner, stager, cutter, renderer)
	if resilience.Builder != nil {
		o.builder = resilience.Builder
	}
	if resilience.Writer != nil {
		o.writer = resilience.Writer
	}
	if resilience.Projection != nil {
		o.projection = resilience.Projection
	}
	return o
}

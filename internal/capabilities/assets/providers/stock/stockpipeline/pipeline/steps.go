package pipeline

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/extract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/plan"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/publish"
)

// DefaultStockSteps returns the canonical typed step slice the Orchestrator
// iterates in RunResilient. The order is the canonical pipeline order:
// plan → stage_sources → extract_clips → publish → finalize.
//
// Future steps MUST be appended (preserving pipeline semantics) — never
// inserted mid-slice, because that would re-rank the checkpoint resume
// ordering and break the §12-3 contract.
func DefaultStockSteps() []core.Step {
	return []core.Step{
		plan.StockPlanStep{},
		ingest.StockStageSourcesStep{},
		extract.StockExtractClipsStep{},
		publish.StockPublishStep{},
		finalize.StockFinalizeStep{},
	}
}

// Compile-time assertions: every default Step struct satisfies core.Step.
var (
	_ core.Step = plan.StockPlanStep{}
	_ core.Step = ingest.StockStageSourcesStep{}
	_ core.Step = extract.StockExtractClipsStep{}
	_ core.Step = publish.StockPublishStep{}
	_ core.Step = finalize.StockFinalizeStep{}
)

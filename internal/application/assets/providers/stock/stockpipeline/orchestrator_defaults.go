// Package stockpipeline — orchestrator_defaults.go (Stock P1 split, July 2026).
//
// This file owns the canonical 6-step dispatch factory and compile-time
// assertions extracted from orchestrator_steps.go.
//
// godlike/06 SSOT: DefaultStockSteps() is the single canonical pipeline
// order for stock. Future steps MUST be appended (preserving pipeline
// semantics) — never inserted mid-slice.
package stockpipeline

// DefaultStockSteps returns the canonical 6-step slice the
// Orchestrator iterates in RunResilient. The slice order is the
// canonical pipeline order: plan → stage_sources → extract_clips
// → compose_chunks → publish → finalize.
//
// SSOT (godlike/06): this is the single canonical pipeline order
// for stock. Future steps MUST be appended (preserving pipeline
// semantics) — never inserted mid-slice (that would re-rank the
// lexically-sorted step_store.FirstNonCompleted result and break
// resume semantics per §12-3 doc-comment).
func DefaultStockSteps() []Step {
	return []Step{
		StockPlanStep{},
		StockStageSourcesStep{},
		StockExtractClipsStep{},
		StockComposeChunksStep{},
		StockPublishStep{},
		StockFinalizeStep{},
	}
}

// Compile-time assertions: every default Step struct satisfies Step.
var (
	_ Step = StockPlanStep{}
	_ Step = StockStageSourcesStep{}
	_ Step = StockExtractClipsStep{}
	_ Step = StockComposeChunksStep{}
	_ Step = StockPublishStep{}
	_ Step = StockFinalizeStep{}
)

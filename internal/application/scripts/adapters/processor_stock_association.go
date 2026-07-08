package adapters

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// StockAssociationProcessor searches stock footage for each scene
// via vector semantic search and populates scene.Bindings.Stock.
// Falls back to the existing Clip.DriveLink when no stock match
// is found.
//
// Enabled as "stock_association" in the plan's Postprocessors list.
// Best-effort policy: a missing or failing stock search does not
// block the pipeline.
//
// Phase 2 of postprocessor-unification (2026-07-08): the processor
// is a thin orchestrator that delegates to
// scene.SceneAssetBinder.BindStock. The per-iteration search loop,
// empty-text skip, and clip-fallback helper all moved to the scene
// package (godlike/06 SSOT one canonical owner per fact).
//
// The constructor signature is STABLE for godlike/07
// minimum-blast-radius — wire_script_postprocess.go does not need
// to change; the binder is constructed inline; the stock search
// port stays on the processor (passed per-call to BindStock — Q10
// verdict).
type StockAssociationProcessor struct {
	stockSearch ports.StockSearchPort
	binder      *scene.SceneAssetBinder
	log         *zap.Logger
}

func NewStockAssociationProcessor(stockSearch ports.StockSearchPort, log *zap.Logger) *StockAssociationProcessor {
	return &StockAssociationProcessor{
		stockSearch: stockSearch,
		log:         log,
		binder:      scene.NewSceneAssetBinder(log),
	}
}

func (p *StockAssociationProcessor) Name() ProcessorName { return ProcessorStockAssociation }

func (p *StockAssociationProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process delegates to scene.SceneAssetBinder.BindStock. The binder
// mutates input.SpecScene.Scenes in-place (per scene by index).
// Returns changed=true on every non-trivial path (Phase 1
// Changed: true invariant) so the registry's IsEmpty() short-circuit
// at postprocessor_document.go does not fire a false "returned empty
// output" warning.
func (p *StockAssociationProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	res := p.binder.BindStock(ctx, input.SpecScene.Scenes, p.stockSearch)
	if !res.Changed {
		return &PostProcessResult{}, nil
	}
	// godlike/07 NO-FAKE-AVAILABILITY: must surface post-loop work
	// even when no emitted fields landed in the result envelope —
	// every iter may have set scene.Bindings.Stock (real hit OR
	// fallbackToClip). Phase 1 closure shipped Changed:true for
	// this exact reason.
	return &PostProcessResult{Changed: true}, nil
}

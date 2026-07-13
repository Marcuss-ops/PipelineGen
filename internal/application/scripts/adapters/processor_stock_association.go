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
// scene.SceneAssetBinder.BindStock. The binder knows only scene_id,
// requirements, candidate assets, and binding policy; the processor
// owns the search and the mapping from SpecScene to the binder's
// request shape.
//
// The constructor signature is STABLE for godlike/07
// minimum-blast-radius — wire_script_postprocess.go does not need
// to change; the binder is constructed inline; the stock search
// port stays on the processor.
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

// Process delegates to scene.SceneAssetBinder.BindStock. The processor
// performs the Qdrant search per scene, builds stock candidate lists,
// and passes them to the binder. The binder returns a map of
// scene_id -> StockBinding that the processor applies back to the
// input scenes.
func (p *StockAssociationProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	_ = plan

	scenes := input.SpecScene.Scenes
	if len(scenes) == 0 || p.stockSearch == nil {
		return &PostProcessResult{}, nil
	}

	reqs := make([]scene.StockBindingRequest, 0, len(scenes))
	for _, s := range scenes {
		var candidates []scene.StockCandidate
		if p.stockSearch != nil && s.Text != "" {
			hits, err := p.stockSearch.SearchStock(ctx, s.Text, 1)
			if err != nil {
				if p.log != nil {
					p.log.Warn("stock_association: search failed",
						zap.String("scene_id", s.ID),
						zap.Error(err))
				}
			} else {
				for _, h := range hits {
					candidates = append(candidates, scene.StockCandidate{
						AssetID:   h.AssetID,
						Name:      h.Name,
						Source:    h.Source,
						DriveLink: h.DriveLink,
						Score:     h.Score,
					})
				}
			}
		}

		var clipDriveLink string
		if s.Bindings.Clip != nil {
			clipDriveLink = s.Bindings.Clip.DriveLink
		}

		reqs = append(reqs, scene.StockBindingRequest{
			SceneID:      s.ID,
			Requirements: scene.AssetRequirements{},
			Candidates:   candidates,
			Policy: scene.StockBindingPolicy{
				FallbackToClip:    true,
				FallbackDriveLink: clipDriveLink,
			},
		})
	}

	res := p.binder.BindStock(reqs)
	if !res.Changed {
		return &PostProcessResult{}, nil
	}

	// Apply bindings back to the original scenes.
	for i := range scenes {
		if binding, ok := res.Bindings[scenes[i].ID]; ok {
			scenes[i].Bindings.Stock = binding
		}
	}

	// godlike/07 NO-FAKE-AVAILABILITY: must surface post-loop work
	// even when no emitted fields landed in the result envelope —
	// every iter may have set scene.Bindings.Stock (real hit OR
	// fallbackToClip). Phase 1 closure shipped Changed:true for
	// this exact reason.
	return &PostProcessResult{Changed: true}, nil
}

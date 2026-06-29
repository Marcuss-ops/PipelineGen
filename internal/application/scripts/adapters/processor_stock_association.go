package adapters

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
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
type StockAssociationProcessor struct {
	stockSearch ports.StockSearchPort
	log         *zap.Logger
}

func NewStockAssociationProcessor(stockSearch ports.StockSearchPort, log *zap.Logger) *StockAssociationProcessor {
	return &StockAssociationProcessor{stockSearch: stockSearch, log: log}
}

func (p *StockAssociationProcessor) Name() string { return "stock_association" }

func (p *StockAssociationProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *StockAssociationProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PostProcessResult, error) {
	if p.stockSearch == nil {
		return &PostProcessResult{}, nil
	}

	scenes := input.SpecScene.Scenes
	if len(scenes) == 0 {
		return &PostProcessResult{}, nil
	}

	for i := range scenes {
		scene := &scenes[i]
		text := strings.TrimSpace(scene.Text)
		if text == "" {
			p.fallbackToClip(scene)
			continue
		}

		hits, err := p.stockSearch.SearchStock(ctx, text, 1)
		if err != nil {
			if p.log != nil {
				p.log.Warn("stock_association: search failed",
					zap.String("scene_id", scene.ID),
					zap.Error(err))
			}
			p.fallbackToClip(scene)
			continue
		}

		if len(hits) == 0 {
			p.fallbackToClip(scene)
			continue
		}

		hit := hits[0]
		scene.Bindings.Stock = &scriptpkg.StockBinding{
			AssetID:   hit.AssetID,
			Name:      hit.Name,
			Source:    hit.Source,
			DriveLink: hit.DriveLink,
			Score:     hit.Score,
			Fallback:  false,
		}

		if p.log != nil {
			p.log.Info("stock_association: bound stock to scene",
				zap.String("scene_id", scene.ID),
				zap.String("asset_id", hit.AssetID),
				zap.Float64("score", hit.Score))
		}
	}

	if p.log != nil {
		p.log.Info("stock_association: processed scenes",
			zap.Int("scenes", len(scenes)))
	}

	return &PostProcessResult{}, nil
}

// fallbackToClip sets StockBinding.DriveLink from the scene's
// existing Clip.DriveLink and marks it as a fallback.
func (p *StockAssociationProcessor) fallbackToClip(scene *scriptpkg.SpecScene) {
	if scene.Bindings.Clip != nil && scene.Bindings.Clip.DriveLink != "" {
		scene.Bindings.Stock = &scriptpkg.StockBinding{
			DriveLink: scene.Bindings.Clip.DriveLink,
			Fallback:  true,
		}
		return
	}
	// No clip binding either — leave Stock nil.
}

// Ensure StockSearchPort is importable (defensive import).
var _ = fmt.Sprintf

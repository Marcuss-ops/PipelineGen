package stockpipeline

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// buildRichStockAsset constructs a canonical asset.Asset from a ClipPlan
// and the cut results. Used by publishCuts for outbox writes.
func buildRichStockAsset(plan ClipPlan, sourceIdx, clipIdx int, outputPath, hash string) *asset.Asset {
	return &asset.Asset{
		ID:        plan.OutputLogicalID,
		Name:      plan.Title,
		Filename:  outputPath,
		Category:  plan.Category,
		SourceURL: plan.SourceID,
		Duration:  time.Duration(plan.EndSec-plan.StartSec) * time.Second,
		Tags:      append([]string(nil), plan.Tags...),
		SearchText: plan.Title + " " + plan.Description,
	}
}

// composeStockChunkSearchText builds searchable text for a stock clip chunk.
func composeStockChunkSearchText(plan ClipPlan) string {
	return plan.Title + " " + plan.Description + " " + plan.Category + " " + plan.SourceID
}

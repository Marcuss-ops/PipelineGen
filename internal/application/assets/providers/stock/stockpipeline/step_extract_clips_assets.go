package stockpipeline

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// buildRichStockAsset constructs a canonical asset.Asset from a ClipPlan
// and the cut results. Used by publishCuts for outbox writes.
func buildRichStockAsset(plan ClipPlan, sourceIdx, clipIdx int, outputPath, hash string) asset.Asset {
	return asset.Asset{
		ID:               plan.OutputLogicalID,
		SourceURL:        plan.SourceID,
		SourceProvider:   plan.SourceProvider,
		SourceVideoID:    plan.SourceVideoID,
		LocalPath:        outputPath,
		SHA256:           hash,
		Title:            plan.Title,
		Description:      plan.Description,
		Duration:         time.Duration(plan.EndSec-plan.StartSec) * time.Second,
		StartSec:         plan.StartSec,
		EndSec:           plan.EndSec,
		Category:         plan.Category,
		Tags:             append([]string(nil), plan.Tags...),
		Round:            plan.Round,
		Slug:             plan.Slug,
		LifecycleState:   asset.LifecycleExtracted,
		ArtifactID:       plan.OutputLogicalID,
	}
}

// composeStockChunkSearchText builds searchable text for a stock clip chunk.
func composeStockChunkSearchText(plan ClipPlan) string {
	return plan.Title + " " + plan.Description + " " + plan.Category + " " + plan.SourceID
}

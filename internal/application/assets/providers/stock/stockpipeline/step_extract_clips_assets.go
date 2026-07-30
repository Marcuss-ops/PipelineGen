package stockpipeline

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// buildRichStockAsset constructs a canonical asset.Asset from a ClipPlan
// and the cut results. Used by publishCuts for outbox writes.
//
// godlike/06 SSOT: Name is the slug derived from Title via perClipLeafName
// (or slugifyTitle fallback). Filename is only the basename of outputPath.
// Metadata carries title, description, round, start_sec, end_sec, slug,
// local_path, sha256, and file_hash so downstream consumers (Qdrant indexer,
// asset search, media_assets projection) see the full rich surface.
func buildRichStockAsset(plan ClipPlan, sourceIdx, clipIdx int, outputPath, hash string) *asset.Asset {
	_, _ = sourceIdx, clipIdx
	slug := perClipLeafName(plan)
	if slug == "" {
		slug = slugifyTitle(plan.Title)
	}

	name := slug
	filename := filepath.Base(outputPath)

	searchText := fmt.Sprintf("Stock video clip title: %s", plan.Title)
	if plan.Description != "" {
		searchText += fmt.Sprintf(" description: %s", plan.Description)
	}
	if plan.Round > 0 {
		searchText += fmt.Sprintf(" round %d", plan.Round)
	}
	if plan.Category != "" {
		searchText += fmt.Sprintf(" category: %s", plan.Category)
	}
	if len(plan.Tags) > 0 {
		searchText += fmt.Sprintf(" tags: %s", strings.Join(plan.Tags, ", "))
	}
	searchText += fmt.Sprintf(" start_sec: %.0f", plan.StartSec)
	searchText += fmt.Sprintf(" end_sec: %.0f", plan.EndSec)

	a := &asset.Asset{
		ID:         plan.OutputLogicalID,
		Source:     asset.Source("stock"),
		Name:       name,
		Filename:   filename,
		Category:   plan.Category,
		SourceURL:  plan.SourceID,
		Duration:   time.Duration(plan.EndSec-plan.StartSec) * time.Second,
		Tags:       append([]string(nil), plan.Tags...),
		SearchText: searchText,
		Metadata:   make(map[string]any),
	}

	// Populate rich Metadata.
	if plan.Title != "" {
		a.SetMetadataString("title", plan.Title)
	}
	if plan.Description != "" {
		a.SetMetadataString("description", plan.Description)
	}
	if plan.Round > 0 {
		a.SetMetadataInt("round", plan.Round)
	}
	a.Metadata["start_sec"] = float64(plan.StartSec)
	a.Metadata["end_sec"] = float64(plan.EndSec)
	if slug != "" {
		a.SetMetadataString("slug", slug)
	}
	a.SetLocalPath(outputPath)
	a.SetFileHash(hash)
	// godlike/06 SSOT: both sha256 and file_hash keys for
	// downstream consumers that probe either key.
	a.SetMetadataString("sha256", hash)

	return a
}

// composeStockChunkSearchText builds searchable text for a stock clip chunk.
func composeStockChunkSearchText(plan ClipPlan) string {
	return plan.Title + " " + plan.Description + " " + plan.Category + " " + plan.SourceID
}

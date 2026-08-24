package ingest

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

	provider := plan.SourceProvider
	if provider == "" {
		provider = "stock"
	}
	// Migration 189 canonical state constraints: media_assets writes MUST
	// carry a valid lifecycle_state (the SQLite trigger
	// trg_media_assets_state_valid_insert aborts on the zero value "").
	// The state mirrors the canonical finalizer convention
	// (asset_finalizer_committer.go::buildCommitRequest): youtube-sourced
	// clips start ACTIVE, everything else PUBLISHED. The Drive upload for
	// this clip happens in publishCuts AFTER this write; the state is the
	// optimistic canonical value that the finalizer/committer reconciles.
	// index_state is left to the column default (DISCOVERED).
	lifecycleState := asset.StatePublished
	if provider == SourceProviderYouTube {
		lifecycleState = asset.StateActive
	}
	a := &asset.Asset{
		ID: plan.OutputLogicalID,
		// Preserve the provider identity of the source URL. YouTube stock
		// acquisitions are consumed by the canonical YouTube asset resolver;
		// labelling them only as "stock" loses source_video_id and makes the
		// resulting row unusable for subject/role validation.
		Source:     asset.Source(provider),
		Name:       name,
		Filename:   filename,
		MediaType:  "video",
		Category:   plan.Category,
		SourceURL:  plan.SourceID,
		Duration:   time.Duration(plan.EndSec-plan.StartSec) * time.Second,
		Tags:       append([]string(nil), plan.Tags...),
		SearchText: searchText,
		Metadata:   make(map[string]any),
		// CreatedAt MUST be stamped by the producer: UpsertClipTx persists
		// clip.CreatedAt verbatim, and a zero time writes an empty
		// created_at column, which breaks time-bucketed queries (e.g. the
		// stock live battery's "created in the last 30 minutes" probe) and
		// ordering by recency. updated_at is re-stamped by UpsertClipTx.
		LifecycleState: lifecycleState,
		CreatedAt:      time.Now().UTC(),
	}
	// The YouTube-stock workflow is a stock usage intent even though the
	// physical provenance remains YouTube. Keep source_type=youtube while
	// making the searchable taxonomy distinguish stock acquisition from
	// ordinary YouTube discovery clips.
	if provider == SourceProviderYouTube {
		a.Metadata["asset_kind"] = "stock_video"
		a.Metadata["semantic_role"] = "stock"
	}

	// Populate rich Metadata.
	if plan.Title != "" {
		a.SetTitle(plan.Title)
	}
	if plan.Description != "" {
		a.SetDescription(plan.Description)
	}
	if plan.Round > 0 {
		a.SetRound(plan.Round)
	}
	a.SetStartSec(float64(plan.StartSec))
	a.SetEndSec(float64(plan.EndSec))
	if slug != "" {
		a.SetSlug(slug)
	}
	a.SetLocalPath(outputPath)
	a.SetLegacyFileMD5(hash)
	// Keep the canonical SHA-256 projection and the legacy compatibility
	// key in sync for downstream consumers that still read either name.
	a.SetSha256(hash)
	a.SetMetadataString("file_hash", hash)
	if plan.SourceProvider != "" {
		a.SetMetadataSourceProvider(plan.SourceProvider)
	}
	if plan.SourceVideoID != "" {
		a.SetMetadataSourceVideoID(plan.SourceVideoID)
	}
	if plan.SourceID != "" {
		// The typed SourceURL field is set in the struct literal above;
		// the metadata key is a provenance mirror for Qdrant search-text
		// and legacy consumers. Both are written so the two surfaces stay
		// in sync (godlike/06 source_url convergence).
		a.SetMetadataSourceURL(plan.SourceID)
	}

	return a
}

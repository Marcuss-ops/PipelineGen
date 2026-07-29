// Package stockpipeline — step_extract_clips_assets.go
// (PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// SOLE owner of the asset-write helpers used by StockExtractClipsStep
// (the canonical implementation of the stock.extract_clips step per
// godlike/06 SSOT). The helpers are pure functions with no
// cross-package state mutation; they live in this sister file so
// future per-field surface drift (additional fields on
// buildRichStockAsset's 10-field literal; new segments in
// composeStockChunkSearchText's Qdrant BM25 channel) touches ONLY
// this capability file. Lookup paths preserved — same package,
// so `buildRichStockAsset(plan, sourceIdx, clipIdx, localPath, hash)`
// and `composeStockChunkSearchText(plan)` resolve via package-scope
// visibility from the orchestrator in step_extract_clips.go.
package stockpipeline

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// buildRichStockAsset composes the canonical 10-field asset.Asset
// for a stock-pipeline cut clip. PR-STOCK-TIMESTAMP-CLIPS Front 4
// (July 2026) replaces the prior 4-field literal with this rich
// surface so downstream consumers (Qdrant indexer, asset search,
// media_assets projection) see Title/Description/Round/Tags/Category/
// LocalPath/SHA256/StartSec/EndSec/SearchText at the row level
// instead of just in metadata.json emitted later.
//
// Field routing (godlike/06 SSOT one-canonical-owner-per-fact):
//   - Direct fields (asset.Asset top-level): ID, Name, Filename,
//     Source, MediaType, Category, SourceURL, Duration, Tags,
//     SearchText, LifecycleState.
//   - Metadata map (open-ended, canonical for fields with no
//     direct column): Title, Description, Round, StartSec, EndSec,
//     LocalPath, SHA256 (mirrored as file_hash), Slug.
//
// godlike/06 typed-accessor contract: SetLocalPath + SetFileHash
// are the canonical typed setters in asset_accessors.go — they
// populate BOTH the Metadata map AND any future typed columns
// when they land. Calling them keeps the wire-shape and the
// accessor surface in lockstep.
//
// godlike/07 minimum-blast-radius: sourceIdx + clipIdx are
// retained as parameters for the legacy chunk_%d_%d fallback
// slug (used only when perClipLeafName returns empty); they
// don't appear in the canonical slug derivation otherwise.
//
// forward-pointer PR-STOCK-RICHASSET-GROUP: the Group field is
// left empty today (the planner's per-clip group is the YouTube
// channel name, not a stock-derived concept). A future PR can
// inject runner.RunInput() into buildRichStockAsset and derive
// Group from stockRootFolderName(in) for consistency with
// stock.publish's per-chunk group semantics.
func buildRichStockAsset(plan ClipPlan, sourceIdx, clipIdx int, localPath, sha256 string) *asset.Asset {
	slug := perClipLeafName(plan)
	if strings.TrimSpace(slug) == "" {
		slug = fmt.Sprintf("chunk_%d_%d", sourceIdx, clipIdx)
	}

	durationSec := plan.EndSec - plan.StartSec
	if durationSec < 0 {
		durationSec = 0
	}

	clip := &asset.Asset{
		ID:             plan.OutputLogicalID,
		Name:           slug,
		Filename:       slug + ".mp4",
		Source:         asset.Source("stock"),
		MediaType:      asset.MediaType("video"),
		Category:       plan.Category,
		SourceURL:      plan.SourceID,
		Duration:       time.Duration(durationSec * float64(time.Second)),
		Tags:           append([]string(nil), plan.Tags...),
		SearchText:     composeStockChunkSearchText(plan),
		LifecycleState: asset.StateActive,
		Metadata: asset.Metadata{
			"title":       plan.Title,
			"description": plan.Description,
			"round":       plan.Round,
			"start_sec":   plan.StartSec,
			"end_sec":     plan.EndSec,
			"local_path":  localPath,
			"sha256":      sha256,
			"file_hash":   sha256,
			"slug":        slug,
		},
	}

	clip.SetLocalPath(localPath)
	clip.SetFileHash(sha256)

	return clip
}

// composeStockChunkSearchText composes the canonical Qdrant BM25
// channel text for a stock-pipeline cut clip. Format:
//
//	"Stock video clip | title: <title> | description: <description>
//	 | round <N> | category: <cat> | tags: <t1, t2, ...>
//	 | start_sec: <s> | end_sec: <e>"
//
// godlike/07 minimum-blast-radius: each labelled segment is dropped
// silently when its inputs are empty so the output never contains
// dangling " | title: " or " | tags: " fragments. godlike/06 SSOT:
// this is the SOLE canonical owner of the stock-chunk text format
// used at the asset-row write seam; the strategy function in
// internal/infrastructure/indexing/searchtext/strategies.go is
// the canonical owner for the downstream Qdrant-payload compose
// seam. The two surfaces produce byte-equivalent SearchText for
// the same input (modulo the " | " separator vs " " separator
// chosen at each seam).
//
// forward-pointer PR-STOCK-SEARCHTEXT-PORT: extract this inline
// composition into a port + composition-root wiring so the
// asset-row write and the Qdrant-payload write share a single
// canonical source. Today the duplication is acceptable per
// godlike/07 minimum-blast-radius (test surface locks both
// compositions to the same field set).
func composeStockChunkSearchText(plan ClipPlan) string {
	parts := []string{"Stock video clip"}

	if title := strings.TrimSpace(plan.Title); title != "" {
		parts = append(parts, "title: "+title)
	}
	if desc := strings.TrimSpace(plan.Description); desc != "" {
		parts = append(parts, "description: "+desc)
	}
	if plan.Round > 0 {
		parts = append(parts, "round "+strconv.Itoa(plan.Round))
	}
	if cat := strings.TrimSpace(plan.Category); cat != "" {
		parts = append(parts, "category: "+cat)
	}
	if len(plan.Tags) > 0 {
		// Filter empty tags to avoid dangling "tags: , , " fragments.
		var kept []string
		for _, t := range plan.Tags {
			if t = strings.TrimSpace(t); t != "" {
				kept = append(kept, t)
			}
		}
		if len(kept) > 0 {
			parts = append(parts, "tags: "+strings.Join(kept, ", "))
		}
	}

	parts = append(parts,
		"start_sec: "+strconv.FormatFloat(plan.StartSec, 'f', -1, 64),
		"end_sec: "+strconv.FormatFloat(plan.EndSec, 'f', -1, 64),
	)

	return strings.Join(parts, " | ")
}

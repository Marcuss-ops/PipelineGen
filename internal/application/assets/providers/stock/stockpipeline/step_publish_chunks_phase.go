// Package stockpipeline — step_publish_chunks_phase.go
// (PR-SPLIT-STEP-PUBLISH, 2026-08-08).
//
// Phase 1 of stock.publish: the per-chunk ArtifactPreparation loop.
// Lives alongside the slim step_publish.go orchestrator; the
// run-level metadata.json (Phase 2) lives in
// step_publish_metadata_phase.go; the Drive-side naming helpers
// live in step_publish_naming.go.
//
// per-chunk ladder (§12-7 replaced the §12-5 Begin/Complete stub):
//
//  1. For each composed chunk: ComputeAndFillSHA256 → Build
//     VerifiedArtifact (ArtifactID = stock:<fp>:chunk:<i>,
//     Required:true) → ArtifactPreparation.Prepare → translate
//     PublishedArtifact → ChunkState (RemoteFileID =
//     Location.FileID per godlike/06 FileID=location NOT
//     identity).
//
//  2. Build metadata.json envelopes:
//     - explicit `clips[]` requests are gated out here
//     (per-timestamp metadata.json comes from Phase 2 / the
//     extract step, NOT per-chunk in this phase)
//     - legacy runs keep the single per-clip metadata.json
//     envelope upload in this loop (test-fixture compat)
//
// godlike/07 fail-closed contracts:
//   - Prepare returns error → abort with
//     ErrStockPublishArtifactFailed (wraps publisher fault;
//     preserves typed sentinel via %w + errors.Is).
//   - ComputeAndFillSHA256 returns error → abort (ChunkState
//     sentinel propagates verbatim — VerifyChunks surfaces
//     ErrStockChunkHashMissing / ErrStockChunkLocalMissing
//     consistently).
//
// godlike/06 SSOT: this file is the SOLE owner of Phase 1
// (per-chunk ArtifactPreparation ladder). Lookup path
// publishChunkPhase is package-private; no new exported symbol.
package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// publishChunkPhase runs the per-chunk ArtifactPreparation loop
// for stock.publish and returns the final chunks slice (seed
// preserved when publishedReady is true; otherwise the built
// per-chunk ChunkState values). The slim Run() orchestrator passes
// the returned slice to Phase 2 (metadata.json) and to the
// Downstream stock.finalize BuildFinalizationRequest gate.
//
// godlike/07 minimum-blast-radius: this helper is unexported
// (package-private); the canonical Run signature is unchanged
// (lookup path StockPublishStep.Run preserved byte-stable).
func publishChunkPhase(
	ctx context.Context,
	runner StepRunner,
	in *RunInput,
	fp string,
	explicitTimestamps bool,
	rootFolderName string,
	rootFolderOverride string,
	timestampGroupName string,
	plans []ClipPlan,
	composed []string,
	existingPublished []ChunkState,
) ([]ChunkState, error) {
	// PR-003 (July 2026): per-run total chunk count = the
	// post-plan length of plans. Repeated per-entry per user spec
	// (logically a run-level scalar — see ChunkState.TotalChunks
	// godoc + ChunkMetadataEntry field godoc for the duplication
	// rationale). Pre-compute once outside the loop per
	// godlike/07 minimum-blast-radius.
	totalChunks := len(plans)
	// PR-004 (July 2026): pre-compute policyVersion once per run
	// so the per-chunk assignment is byte-equivalent across the
	// whole run (locks traceability + avoids N-times strings.TrimSpace
	// in the hot loop). Fallback to StockTimestampPolicyVersionV1
	// when RunInput.PolicyVersion is empty/whitespace per user
	// spec. Stamped on every chunk via ChunkState.PolicyVersion.
	policyVersion := StockTimestampPolicyVersionV1
	if in != nil {
		policyVersion = nonEmptyTrim(in.PolicyVersion, policyVersion)
	}

	publishedReady := len(existingPublished) > 0 && (len(composed) == 0 || len(existingPublished) == len(composed))
	chunks := make([]ChunkState, 0, len(composed))
	if publishedReady {
		chunks = append(chunks, existingPublished...)
	}
	for i, compPath := range composed {
		if publishedReady {
			break
		}
		plan := ClipPlan{}
		hasPlan := i < len(plans)
		if hasPlan {
			plan = plans[i]
		}
		cs := ChunkState{
			Index:         i,
			ArtifactID:    ChunkArtifactID(fp, i),
			Filename:      ChunkArtifactFilename(fp, i),
			LocalPath:     compPath,
			TotalChunks:   totalChunks,
			PolicyVersion: policyVersion,
		}
		if hasPlan {
			cs.SourceURL = plan.SourceID
			cs.SourceProvider = plan.SourceProvider
			cs.SourceVideoID = plan.SourceVideoID
			cs.StartSec = plan.StartSec
			cs.EndSec = plan.EndSec
			cs.Description = plan.Description
			// PR-STOCK-TIMESTAMP-CLIPS Front 2 (July 2026): thread the
			// 4 new content fields from ClipPlan → ChunkState. Tags
			// gets a defensive copy so downstream mutation (rare but
			// possible for retry paths that reuse ChunkState) doesn't
			// leak into the plan.
			cs.Round = plan.Round
			cs.Tags = append([]string(nil), plan.Tags...)
			cs.Category = plan.Category
			cs.Slug = plan.Slug
		}
		if explicitTimestamps {
			cs.ArtifactID = TimestampArtifactID(fp, i, "video")
			if hasPlan && plans[i].Title != "" {
				cs.Title = plans[i].Title
			} else if in != nil && i < len(in.Clips) {
				cs.Title = in.Clips[i].Title
			}
			// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): sync
			// plan.Title to the resolved cs.Title so perClipLeafName
			// uses the SAME source-of-truth title the chunk is
			// indexed/displayed by. Without this, an empty Plan.Title
			// + populated in.Clips[i].Title would land the chunk in a
			// start-end-named subdir while the chunk title is "Round 7"
			// (godlike/07 NO-FAKE-AVAILABILITY: avoid the title/leaf
			// mismatch that confuses operators scanning Drive).
			if cs.Title != "" {
				plan.Title = cs.Title
			}
			// PR-PLAN-DESCRIPTION-SYNC (July 2026): same bug class as
			// the Title MUST-FIX above. If Plan.Description is empty
			// but in.Clips[i].Description is populated, populate
			// cs.Description from the canonical clip spec source.
			// Without this, an explicit-clips run that surfaces
			// through a planner that doesn't propagate Description
			// (e.g. a future implicit-planner path or a third-party
			// planner that skips the front-2 thread) would silently
			// lose the per-timestamp narration — metadata.json's
			// chunks[0].description would be absent even though
			// in.Clips[i].Description carried the canonical content
			// (godlike/07 NO-FAKE-AVAILABILITY: a silent description
			// drop in metadata.json hides Qdrant search-text input
			// from downstream consumers).
			if hasPlan && plans[i].Description != "" {
				cs.Description = plans[i].Description
			} else if in != nil && i < len(in.Clips) {
				cs.Description = in.Clips[i].Description
			}
			// Sync back plan.Description so perClipLeafName and any
			// other downstream consumer (e.g. Qdrant semantic-payload
			// enrichment) reads the SAME source-of-truth description
			// the chunk is indexed by — same godlike/06 SSOT
			// lockstep discipline as the Title sync-back above.
			if cs.Description != "" {
				plan.Description = cs.Description
			}
		}
		if compPath != "" {
			if err := cs.ComputeAndFillSHA256(runner.LocalFS()); err != nil {
				// P6 (July 2026): compose_chunks now produces real
				// files — ErrStockChunkLocalMissing is a hard failure.
				return nil, fmt.Errorf("orchestrator: stock.publish: chunk %d (artifact=%s): %w",
					i, cs.ArtifactID, err)
			}
		}
		idem, idemErr := asset.SHA256IdempotencyKey("stock", cs.SHA256)
		if idemErr != nil {
			return nil, fmt.Errorf("%w: chunk %d (artifact=%s) idem-key: %v",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, idemErr)
		}
		// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): explicit-clips
		// PathLeafName uses the shared explicit timestamp parent folder
		// (all 5-second children from the same parent timestamp land in
		// the same Drive folder). Legacy (no clips[]) also stays on
		// timestampGroupName, so the two modes now agree on the parent
		// leaf and differ only in how many files are placed under it.
		var leafName string
		if explicitTimestamps && plan.Round > 0 {
			// Round-indexed clips share exactly one Drive folder per
			// round. Previously this value was always timestampGroupName,
			// which merged every round into the same subdirectory.
			leafName = stockClipFolderName(in, plan, timestampGroupName)
		} else {
			leafName = timestampGroupName
		}
		va := finalization.VerifiedArtifact{
			ArtifactID:     cs.ArtifactID,
			Kind:           finalization.KindVideo,
			Filename:       cs.Filename,
			MIMEType:       "video/mp4",
			LocalPath:      cs.LocalPath,
			SizeBytes:      cs.SizeBytes,
			SHA256:         cs.SHA256,
			Requirement:    finalization.ArtifactRequirementRequired,
			IdempotencyKey: idem + ":c" + strconv.Itoa(i),
			Description:    cs.Description,
			// DRIVE-IS-DRIVE (July 2026): stock now passes the explicit
			// drive_folder_id as the Drive root override when provided.
			// FolderID remains the workflow identifier; the override is
			// the actual Drive root selector.
			// The artifact publisher adapter derives Group/Subject from
			// RootFolderName + PathLeafName via stockArtifactPathParts.
			// The DestinationRegistry + PathBuilder handle routing.
			RootFolderName:     rootFolderName,
			RootFolderOverride: rootFolderOverride,
			RootFolderResolved: in != nil && in.DriveFolderResolved,
			PathLeafName:       leafName,
		}
		published, prepErr := runner.ArtifactPreparation().Prepare(ctx, va)
		if prepErr != nil {
			return nil, fmt.Errorf("%w: chunk %d (artifact=%s): %v",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, prepErr)
		}
		cs.RemoteFileID = published.Location.FileID
		cs.RemoteWebViewLink = published.Location.WebViewLink
		// PR-004 (July 2026): capture the canonical Drive webview
		// link as DrivePath. The Qdrant semantic-payload enrichment
		// wave expects the wire-shape key drive_path on chunk rows;
		// the legacy metadata.json still uses drive_web_view_link
		// (preserved on RemoteWebViewLink above). Same source-of-truth
		// (PublishedArtifact.Location.WebViewLink) — the field-name
		// divergence is the canonical SSOT-vs-legacy-tradeoff the
		// user spec asks for (godlike/07 minimum-blast-radius: no
		// new surface contract, just a typed alias on the struct).
		cs.DrivePath = published.Location.WebViewLink
		cs.RemoteDownloadLink = published.Location.DownloadLink

		if !explicitTimestamps {
			// Legacy non-timestamp runs retain the per-chunk metadata
			// envelope behavior. Explicit timestamp runs now publish one
			// metadata.json per parent timestamp folder from the extract
			// step, so this branch is skipped for the 5-second child clips.
			clipMetaPath, clipMetaHash, clipMetaSize, clipMetaErr := writeAndHashPerClipMetadata(in, cs, fp, runner.LocalFS())
			if clipMetaErr != nil {
				return nil, fmt.Errorf("%w: per-clip metadata.json stage for chunk %d (artifact=%s): %w",
					ErrStockPublishArtifactFailed, i, cs.ArtifactID, clipMetaErr)
			}
			defer func() {
				if rmErr := runner.LocalFS().Remove(clipMetaPath); rmErr != nil && !os.IsNotExist(rmErr) {
					if runner.Log() != nil {
						runner.Log().Warn("orchestrator: stock.publish: failed to remove per-clip metadata temp file",
							zap.String("path", clipMetaPath), zap.Int("chunk_index", i), zap.Error(rmErr))
					}
				}
			}()

			clipMetaIdem, clipMetaIdemErr := asset.SHA256IdempotencyKey("stock:"+fp+":clip-metadata:"+strconv.Itoa(i), clipMetaHash)
			if clipMetaIdemErr != nil {
				return nil, fmt.Errorf("%w: per-clip metadata idem-key for chunk %d: %w",
					ErrStockPublishArtifactFailed, i, clipMetaIdemErr)
			}
			clipMetaArtifactID := ChunkArtifactID(fp, i) + ":metadata"
			clipMetaVA := finalization.VerifiedArtifact{
				ArtifactID:         clipMetaArtifactID,
				Kind:               finalization.KindMetadata,
				Filename:           "metadata.json",
				MIMEType:           "application/json",
				LocalPath:          clipMetaPath,
				SizeBytes:          clipMetaSize,
				SHA256:             clipMetaHash,
				Requirement:        finalization.ArtifactRequirementRequired,
				IdempotencyKey:     clipMetaIdem,
				RootFolderName:     rootFolderName,
				RootFolderOverride: rootFolderOverride,
				RootFolderResolved: in != nil && in.DriveFolderResolved,
				PathLeafName:       leafName,
			}
			if _, clipMetaPrepErr := runner.ArtifactPreparation().Prepare(ctx, clipMetaVA); clipMetaPrepErr != nil {
				return nil, fmt.Errorf("%w: per-clip metadata.json upload for chunk %d (artifact=%s): %w",
					ErrStockPublishArtifactFailed, i, clipMetaArtifactID, clipMetaPrepErr)
			}
		}

		chunks = append(chunks, cs)
	}
	return chunks, nil
}

// nonEmptyTrim returns s trimmed when non-empty, otherwise fallback.
// Kept package-private to this file so the policy-propagation logic
// of PR-004 (July 2026) doesn't escape to other phases.
//
// godlike/07 minimum-blast-radius: tiny helper introduced by
// PR-SPLIT-STEP-PUBLISH (2026-08-08) — pure code-motion equivalent
// to the prior inline `if trimmed := strings.TrimSpace(in.PolicyVersion);
// trimmed != "" { policyVersion = trimmed }` block (3 lines → 1).
func nonEmptyTrim(s string, fallback string) string {
	if trimmed := strings.TrimSpace(s); trimmed != "" {
		return trimmed
	}
	return fallback
}

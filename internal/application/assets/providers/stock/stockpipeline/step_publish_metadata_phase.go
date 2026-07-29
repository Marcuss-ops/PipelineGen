// Package stockpipeline — step_publish_metadata_phase.go
// (PR-SPLIT-STEP-PUBLISH, 2026-08-08).
//
// Phase 2 of stock.publish: the run-level metadata.json
// ArtifactPreparation + TimestampDriveFolderLink backfill on every
// chunk. Lives alongside the slim step_publish.go orchestrator;
// the per-chunk loop lives in step_publish_chunks_phase.go; the
// Drive-side naming helpers live in step_publish_naming.go.
//
// Per-run metadata.json ladder:
//  1. writeAndHashMetadata writes the per-run metadata.json to
//     temp, computes SHA256, returns (path, hash, size, err).
//  2. Build VerifiedArtifact (metadata ArtifactID =
//     MetadataArtifactID(fp), Required:true, PathLeafName =
//     "metadata" for explicit-clips mode OR timestampGroupName
//     for legacy mode).
//  3. ArtifactPreparation.Prepare → translate PublishedArtifact.
//  4. Backfill TimestampDriveFolderLink + TimestampFolderID on
//     every chunk from the metadata artifact's Location.FolderID
//     (PR-TIMESTAMP-FOLDER-LINK, July 2026).
//  5. Return MetadataState for the orchestrator's SUCCEEDED log
//     + downstream stock.finalize BuildFinalizationRequest gate.
//
// Inline drive.URL construction matches drive.FolderURLFromID
// ("https://drive.google.com/drive/folders/" + id). The stock
// pipeline cannot import infrastructure/drive directly (Pattern 0
// clean architecture); the constant is SSOT-locked here.
//
// godlike/06 SSOT: this file is the SOLE owner of Phase 2 (run-level
// metadata.json ArtifactPreparation + TimestampFolderLink
// backfill). Lookup path publishMetadataPhase is package-private;
// no new exported symbol.
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// driveFolderURLPrefix is the SSOT-locked Drive folder URL prefix
// used by publishMetadataPhase to build the canonical
// https://drive.google.com/drive/folders/<id> link for every chunk's
// TimestampDriveFolderLink backfill. Mirrors drive.FolderURLFromID
// exactly (the stock pipeline cannot import infrastructure/drive
// directly per Pattern 0 — the constant is replicated here as a
// canonical SSOT-locked sentinel).
const driveFolderURLPrefix = "https://drive.google.com/drive/folders/"

// publishMetadataPhase runs the per-run metadata.json
// ArtifactPreparation + TimestampDriveFolderLink backfill for
// stock.publish. Returns MetadataState for the orchestrator's
// SUCCEEDED log + downstream stock.finalize BuildFinalizationRequest
// gate.
//
// godlike/07 minimum-blast-radius: helper is unexported
// (package-private); the canonical Run signature is unchanged
// (lookup path StockPublishStep.Run preserved byte-stable).
//
// godlike/07 NO-FAKE-AVAILABILITY: PR-STOCK-RESUME-STATE-LOSS guard
// stays in the slim step_publish.go orchestrator (BEFORE this phase
// runs). This phase is only entered when len(chunks) > 0.
func publishMetadataPhase(
	ctx context.Context,
	runner StepRunner,
	in *RunInput,
	fp string,
	explicitTimestamps bool,
	rootFolderName string,
	rootFolderOverride string,
	timestampGroupName string,
	chunks []ChunkState,
) (MetadataState, error) {
	// PR-TIMESTAMP-FOLDER-LINK (July 2026): capture parent timestamp
	// folder metadata from the FIRST chunk's PublishedArtifact.Location
	// (all chunks share the same parent folder). Must happen BEFORE
	// writeAndHashMetadata so the metadata.json file on Drive also
	// contains the timestamp fields (not just SQLite metadata.json).
	// For explicit-clips: each chunk was published to its own per-clip
	// subfolder under the shared timestamp parent — the parent folder
	// is the grandparent of each per-clip folder. We capture the
	// metadataPublished.Location.FolderID below since that
	// artifact is always uploaded into the timestamp-parent context.
	//
	// The driveFolderURLPrefix constant above is the SSOT source.

	metaPath, metaHash, metaSize, metaErr := writeAndHashMetadata(in, chunks, fp, runner.LocalFS())
	if metaErr != nil {
		return MetadataState{}, fmt.Errorf("%w: metadata.json stage: %v",
			ErrStockPublishArtifactFailed, metaErr)
	}
	defer func() {
		if rmErr := runner.LocalFS().Remove(metaPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.publish: failed to remove metadata temp file",
					zap.String("path", metaPath), zap.Error(rmErr))
			}
		}
	}()

	metaIdem, metaIdemErr := asset.SHA256IdempotencyKey("stock:"+fp+":metadata", metaHash)
	if metaIdemErr != nil {
		return MetadataState{}, fmt.Errorf("%w: metadata idem-key: %v",
			ErrStockPublishArtifactFailed, metaIdemErr)
	}
	// PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026): metadata.json
	// sits inside the shared explicit timestamp parent folder (the
	// same leaf used by the child clips). Legacy stays on the shared
	// timestampGroupName leaf (preserves the legacy
	// TestStockPublishStep_LegacyMultipleChunks_SharedPathLeafName
	// invariant for the metadata artifact).
	var metaLeafName string
	if explicitTimestamps {
		metaLeafName = timestampGroupName
	} else {
		metaLeafName = timestampGroupName
	}
	metaVA := finalization.VerifiedArtifact{
		ArtifactID:     MetadataArtifactID(fp),
		Kind:           finalization.KindMetadata,
		Filename:       "metadata.json",
		MIMEType:       "application/json",
		LocalPath:      metaPath,
		SizeBytes:      metaSize,
		SHA256:         metaHash,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: metaIdem,
		// DRIVE-IS-DRIVE (July 2026): stock now passes the explicit
		// drive_folder_id as the Drive root override when provided.
		// FolderID remains the workflow identifier; the override is
		// the actual Drive root selector.
		RootFolderName:     rootFolderName,
		RootFolderOverride: rootFolderOverride,
		RootFolderResolved: in != nil && in.DriveFolderResolved,
		PathLeafName:       metaLeafName,
	}
	metaPublished, metaPrepErr := runner.ArtifactPreparation().Prepare(ctx, metaVA)
	if metaPrepErr != nil {
		return MetadataState{}, fmt.Errorf("%w: metadata.json upload: %v",
			ErrStockPublishArtifactFailed, metaPrepErr)
	}
	// PR-TIMESTAMP-FOLDER-LINK (July 2026): capture the parent
	// timestamp Drive folder metadata from the metadata artifact's
	// Location. For legacy runs: this is the timestamp parent
	// folder. For explicit-clips runs: this is the metadata/
	// subfolder (operators click breadcrumb to go up). Backfill
	// onto all chunks so buildStockRunMetadata propagates.
	metaFolderID := metaPublished.Location.FolderID
	if metaFolderID != "" {
		metaFolderLink := driveFolderURLPrefix + metaFolderID
		for i := range chunks {
			chunks[i].TimestampDriveFolderLink = metaFolderLink
			chunks[i].TimestampFolderID = metaFolderID
		}
	}

	return MetadataState{
		LocalPath:         metaVA.LocalPath,
		SHA256:            metaVA.SHA256,
		SizeBytes:         metaVA.SizeBytes,
		RemoteFileID:      metaPublished.Location.FileID,
		RemoteWebViewLink: metaPublished.Location.WebViewLink,
	}, nil
}

// Package stockpipeline — step_publish.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of StockPublishStep — the canonical implementation
// of the stock.publish step (Step 5 of the 6-step pipeline) per
// godlike/06 SSOT. §12-7 replaced the §12-5 Begin/Complete stub
// with the real AssetPreparation ladder:
//
//  1. For each composed chunk: ComputeAndFillSHA256 → Build
//     VerifiedArtifact (ArtifactID = stock:<fp>:chunk:<i>,
//     Required:true) → ArtifactPreparation.Prepare → translate
//     PublishedArtifact → ChunkState (RemoteFileID =
//     Location.FileID per godlike/06 FileID=location NOT
//     identity).
//
//  2. Build the per-run metadata.json (StockRunMetadata envelope
//     with the per-chunk entries baked in) → write to temp →
//     SHA256 → ArtifactPreparation.Prepare → translate →
//     MetadataState.
//
// godlike/07 fail-closed contracts:
//   - AssetPreparation nil → State.Published = nil, return nil
//     (test-fixture compat). Downstream stock.finalize's
//     BuildFinalizationRequest will raise
//     ErrStockNoChunksFinalized.
//   - Prepare returns error → abort with
//     ErrStockPublishArtifactFailed (wraps publisher fault;
//     preserves typed sentinel via %w+errors.Is).
//   - ComputeAndFillSHA256 returns error → abort (ChunkState
//     sentinel propagates verbatim — VerifyChunks surfaces
//     ErrStockChunkHashMissing / ErrStockChunkLocalMissing
//     consistently).
package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// StockPublishStep is the canonical implementation of
// stock.publish. §12-7 replaces the §12-5 Begin/Complete stub
// with the real AssetPreparation ladder.
type StockPublishStep struct{}

func (StockPublishStep) Name() string { return StepKeyStockPublish }

func (StockPublishStep) Run(ctx context.Context, runner StepRunner) error {
	if runner.ArtifactPreparation() == nil {
		// Test-fixture path: no AssetPreparation wired → no chunks
		// prepared. StockFinalizeStep's BuildFinalizationRequest gate
		// raises ErrStockNoChunksFinalized — that's the intended
		// fail-closed signal for unwired composition roots + tests.
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.publish: ArtifactPreparation nil — skipping upload (test-fixture path)")
		}
		runner.State().Published = nil
		return nil
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish: AssetPreparation wired — preparing chunks + metadata")
	}

	fp := runner.RunFingerprint()
	composed := runner.State().ComposedPaths
	chunks := make([]ChunkState, 0, len(composed))

	// ── Phase 1: per-chunk ArtifactPreparation ─────────────────────
	for i, compPath := range composed {
		cs := ChunkState{
			Index:      i,
			ArtifactID: ChunkArtifactID(fp, i),
			Filename:   ChunkArtifactFilename(fp, i),
			LocalPath:  compPath,
		}
		if compPath != "" {
			if err := cs.ComputeAndFillSHA256(); err != nil {
				// P6 (July 2026): compose_chunks now produces real
				// files — ErrStockChunkLocalMissing is a hard failure.
				return fmt.Errorf("orchestrator: stock.publish: chunk %d (artifact=%s): %w",
					i, cs.ArtifactID, err)
			}
		}
		idem, idemErr := asset.SHA256IdempotencyKey("stock", cs.SHA256)
		if idemErr != nil {
			return fmt.Errorf("%w: chunk %d (artifact=%s) idem-key: %v",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, idemErr)
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
		}
		published, prepErr := runner.ArtifactPreparation().Prepare(ctx, va)
		if prepErr != nil {
			return fmt.Errorf("%w: chunk %d (artifact=%s): %v",
				ErrStockPublishArtifactFailed, i, cs.ArtifactID, prepErr)
		}
		cs.RemoteFileID = published.Location.FileID
		cs.RemoteWebViewLink = published.Location.WebViewLink
		cs.RemoteDownloadLink = published.Location.DownloadLink
		chunks = append(chunks, cs)
	}
	runner.State().Published = chunks

	// ── Phase 2: per-run metadata.json ArtifactPreparation ────────
	// Always invoked AFTER chunks so the metadata's Chunks[] list
	// embeds the per-chunk ArtifactIDs + DriveFileIDs.
	//
	// STATO ATTUALE: compose_chunks produce file reali.
	// L'ErrStockChunkLocalMissing è RIMOSSO — chunk mancanti sono
	// hard failure.
	//
	// PROSSIMO STEP: rimuovere questo guard quando compose_chunks
	// è sempre wired in produzione. Oggi il renderer può essere nil
	// in test-fixture mode, quindi il guard è ancora necessario.
	if len(chunks) == 0 {
		// godlike/07 fail-closed (PR-STOCK-RESUME-STATE-LOSS, July 2026):
		// if AssetPreparation is wired (production mode) but ComposedPaths
		// was empty (zero chunks prepared), the runState was lost on resume
		// (or compose_chunks short-circuited). Returning nil here would be
		// a silent-success false-positive — the job would declare SUCCEEDED
		// without uploading anything. The leniency is preserved ONLY for
		// test-fixture mode (AssetPreparation nil) where empty chunks is
		// the expected outcome of a stub run.
		if runner.ArtifactPreparation() != nil {
			if runner.Log() != nil {
				runner.Log().Error("orchestrator: stock.publish: ArtifactPreparation wired but ComposedPaths empty — fail-closed on resume state-loss")
			}
			return ErrStockPublishStateLost
		}
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.publish: zero chunks prepared — skipping metadata publication (pre-Commit-7 stub)")
		}
		return nil
	}
	metaPath, metaHash, metaSize, metaErr := writeAndHashMetadata(
		runner.RunInput(), chunks, fp,
	)
	if metaErr != nil {
		return fmt.Errorf("%w: metadata.json stage: %v",
			ErrStockPublishArtifactFailed, metaErr)
	}
	defer func() {
		// Best-effort cleanup of the metadata temp file after
		// Prepare. The Publisher has already consumed the contents
		// and the metadata RemoteFileID lives on in GoMemory.
		if rmErr := os.Remove(metaPath); rmErr != nil && !os.IsNotExist(rmErr) {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.publish: failed to remove metadata temp file",
					zap.String("path", metaPath), zap.Error(rmErr))
			}
		}
	}()

	metaIdem, metaIdemErr := asset.SHA256IdempotencyKey("stock:"+fp+":metadata", metaHash)
	if metaIdemErr != nil {
		return fmt.Errorf("%w: metadata idem-key: %v",
			ErrStockPublishArtifactFailed, metaIdemErr)
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
	}
	metaPublished, metaPrepErr := runner.ArtifactPreparation().Prepare(ctx, metaVA)
	if metaPrepErr != nil {
		return fmt.Errorf("%w: metadata.json upload: %v",
			ErrStockPublishArtifactFailed, metaPrepErr)
	}
	runner.State().MetadataPublished = MetadataState{
		LocalPath:         metaVA.LocalPath,
		SHA256:            metaVA.SHA256,
		SizeBytes:         metaVA.SizeBytes,
		RemoteFileID:      metaPublished.Location.FileID,
		RemoteWebViewLink: metaPublished.Location.WebViewLink,
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish: AssetPreparation completed",
			zap.Int("chunk_count", len(chunks)),
			zap.String("metadata_artifact_id", MetadataArtifactID(fp)),
			zap.String("metadata_remote_file_id", metaPublished.Location.FileID))
	}
	return nil
}

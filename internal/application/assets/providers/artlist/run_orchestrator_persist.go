// Single canonical owner of artlist persistence methods persistRenditions/stagePersistResults/buildPublishedArtifact + fileSizeFromPath. Receiver *RunOrchestratorService remains in service.go (godlike/06 SSOT Commit C).
package artlist

import (
	"context"
	"fmt"
	"os"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"
)

// persistRenditions records each generated rendition as an
// asset_locations row + asset_renditions row. The location is marked
// as primary only for the mezzanine (the canonical edited master).
func (o *RunOrchestratorService) persistRenditions(ctx context.Context, assetID string, renditions []asset.RenditionOutput) error {
	if o.svc.locationRepo == nil || o.svc.renditionRepo == nil {
		return nil
	}
	for _, r := range renditions {
		if r.LocalPath == "" {
			continue
		}
		loc := &asset.Location{
			AssetID:       assetID,
			LocationKind:  asset.LocationKindLocal,
			URI:           r.LocalPath,
			MimeType:      r.MimeType,
			FileSizeBytes: r.SizeBytes,
			FileHash:      r.FileHash,
			IsPrimary:     r.Kind == asset.RenditionKindMezzanine,
		}
		if err := o.svc.locationRepo.Upsert(ctx, loc); err != nil {
			return fmt.Errorf("upsert location for %s/%s: %w", assetID, r.Kind, err)
		}

		rend := &asset.AssetRendition{
			AssetID:    assetID,
			LocationID: &loc.ID,
			Kind:       r.Kind,
			Container:  r.Container,
			Codec:      r.Codec,
			Width:      r.Width,
			Height:     r.Height,
			FPS:        r.FPS,
			Bitrate:    r.Bitrate, SHA256: r.FileHash,
			SizeBytes: r.SizeBytes,
		}
		if _, err := o.svc.renditionRepo.Create(ctx, rend); err != nil {
			return fmt.Errorf("create rendition for %s/%s: %w", assetID, r.Kind, err)
		}
	}
	return nil
}

// stagePersistResults persists each processed clip through the canonical
// AssetFinalizerTx. This replaces the legacy dispatchBridge path and
// writes media_assets, asset_versions, asset_locations, and
// asset_renditions inside a single SQLite transaction per clip.
//
// PR-ARTLIST-FINALIZER (July 2026): the legacy dispatchBridge +
// persistRenditions custom writer are retired. Artlist now uses the
// same AssetFinalizerTx as every other capability, ensuring the ledger
// tables are written by one canonical implementation.
func (o *RunOrchestratorService) stagePersistResults(ctx context.Context, resp *RunTagResponse) {
	if o.svc.assetFinalizer == nil || o.svc.mainDB == nil {
		o.svc.log.Warn("stagePersistResults: asset finalizer or main DB not wired (cannot persist)")
		return
	}

	for i := range resp.Items {
		item := &resp.Items[i]
		if item.Status == "media_process_failed" || item.Status == "dry_run" || item.Status == "skipped_existing" {
			continue
		}

		// PR-ARTLIST-DOD-GATE-02 (2026-07-07): Drive field gate.
		// Skip items whose processor returned Status="processed" but
		// left Drive fields empty — the processor's Drive upload step
		// failed silently.
		if item.DriveFileID == "" || item.DriveLink == "" {
			o.svc.log.Warn("stagePersistResults: skipping clip with missing Drive fields",
				zap.String("clip_id", item.ClipID),
				zap.String("drive_file_id", item.DriveFileID),
				zap.String("drive_link", item.DriveLink))
			item.Status = "drive_upload_failed"
			item.Error = "Drive upload failed: missing Drive fields after processing"
			resp.Failed++
			continue
		}

		// PR-ARTLIST-HASH-FIX (July 2026): reject assets without a real
		// SHA-256. The legacy fallback (clipID:source) is retired per
		// the hash-system refactor.
		if item.FileHash == "" {
			o.svc.log.Warn("stagePersistResults: skipping clip with missing SHA-256",
				zap.String("clip_id", item.ClipID))
			item.Status = "hash_missing"
			item.Error = "SHA-256 missing after processing"
			resp.Failed++
			continue
		}

		artifact := o.buildPublishedArtifact(item)

		tx, err := o.svc.mainDB.BeginTx(ctx, nil)
		if err != nil {
			o.svc.log.Warn("stagePersistResults: begin tx failed",
				zap.String("clip_id", item.ClipID), zap.Error(err))
			continue
		}

		_, _, err = o.svc.assetFinalizer.FinalizeAsset(ctx, assetfinalizer.WrapTx(tx), artifact)
		if err != nil {
			_ = tx.Rollback()
			o.svc.log.Warn("stagePersistResults: finalizer failed",
				zap.String("clip_id", item.ClipID), zap.Error(err))
			item.Status = "persist_failed"
			item.Error = err.Error()
			resp.Failed++
			continue
		}

		if err := tx.Commit(); err != nil {
			o.svc.log.Warn("stagePersistResults: commit failed",
				zap.String("clip_id", item.ClipID), zap.Error(err))
			item.Status = "persist_failed"
			item.Error = err.Error()
			resp.Failed++
			continue
		}

		resp.Processed++

		// Link materialized Artlist assets to the canonical Maya concept
		// graph in media memory. The linker is optional: when the
		// mediamemory repos are not wired, this is a no-op.
		o.linkMayaMediaMemory(ctx, item)
	}
}

// linkMayaMediaMemory creates the canonical Maya concept graph and binds
// it to the materialized asset. It is a best-effort post-persist step:
// failures are logged but do not fail the run.
func (o *RunOrchestratorService) linkMayaMediaMemory(ctx context.Context, item *RunTagItem) {
	linker := newMediaMemoryLinker(o.svc.conceptRepo, o.svc.bindingRepo, o.svc.normalizer, o.svc.log)
	if linker.disabled() {
		return
	}
	if item.ClipID == "" {
		return
	}

	slot := media.SlotPrimaryVideo
	// TODO: the language and topic are hard-coded for the legacy Maya
	// run. Drive both from the run context once topic-driven concept
	// discovery is implemented.
	if err := linker.linkMayaConcepts(ctx, item.ClipID, "it", slot); err != nil {
		o.svc.log.Warn("stagePersistResults: failed to link Maya media memory concepts",
			zap.String("clip_id", item.ClipID),
			zap.Error(err))
	}
}

// buildPublishedArtifact maps a processed RunTagItem into the canonical
// finalization.PublishedArtifact consumed by AssetFinalizerTx.
func (o *RunOrchestratorService) buildPublishedArtifact(item *RunTagItem) finalization.PublishedArtifact {
	metadata := cloneMetadata(item.Metadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["source"] = "artlist"
	metadata["status"] = "processed"
	metadata["filename"] = item.Filename
	// The Artlist processor renders the canonical output at the configured
	// video duration. Persist that interval explicitly so the finalizer can
	// populate the canonical start/end and duration columns instead of
	// leaving them at their zero-value defaults.
	if o.svc.cfg != nil && o.svc.cfg.Video.Duration > 0 {
		durationSec := float64(o.svc.cfg.Video.Duration)
		if _, ok := metadata["start_sec"]; !ok {
			metadata["start_sec"] = float64(0)
		}
		if _, ok := metadata["end_sec"]; !ok {
			metadata["end_sec"] = durationSec
		}
		if _, ok := metadata["duration_ms"]; !ok {
			metadata["duration_ms"] = int64(durationSec * 1000)
		}
	}
	artifact := finalization.PublishedArtifact{
		ArtifactID:  item.ClipID,
		Kind:        finalization.KindVideo,
		Filename:    item.Filename,
		MIMEType:    "video/mp4",
		SizeBytes:   fileSizeFromPath(item.LocalPath),
		SHA256:      item.FileHash,
		Source:      "artlist",
		Description: item.Name,
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       item.DriveFileID,
			WebViewLink:  item.DriveLink,
			DownloadLink: item.DownloadLink,
			FolderID:     o.svc.cfg.Drive.ArtlistFolder(),
			Action:       finalization.PublishCreated,
		},
		ArtifactMetadata: metadata,
	}

	for _, r := range item.Renditions {
		artifact.Renditions = append(artifact.Renditions, finalization.AssetRenditionLocation{
			Kind:      string(r.Kind),
			Provider:  "local",
			URI:       r.LocalPath,
			MimeType:  r.MimeType,
			SizeBytes: r.SizeBytes,
			FileHash:  r.FileHash,
			Width:     r.Width,
			Height:    r.Height,
			FPS:       r.FPS,
			Bitrate:   r.Bitrate,
			Container: r.Container,
			Codec:     r.Codec,
		})
	}

	return artifact
}

// fileSizeFromPath returns the file size in bytes, or 0 if the file cannot be stat'd.
func fileSizeFromPath(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

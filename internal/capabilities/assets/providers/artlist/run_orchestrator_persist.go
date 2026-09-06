// Single canonical owner of artlist persistence methods stagePersistResults/buildPublishedArtifact + fileSizeFromPath. Receiver *RunOrchestratorService remains in service.go (godlike/06 SSOT Commit C).
package artlist

import (
	"context"
	"fmt"
	"os"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

	"go.uber.org/zap"
)

// stagePersistResults persists each processed clip through the canonical
// AssetFinalizerTx. This replaces the legacy dispatchBridge path and
// writes media_assets, asset_versions, asset_locations, and
// asset_renditions inside a single SQLite transaction per clip.
//
// PR-ARTLIST-FINALIZER (July 2026): the legacy dispatchBridge +
// persistRenditions custom writer are retired. Artlist now uses the
// same AssetFinalizerTx as every other capability, ensuring the ledger
// tables are written by one canonical implementation.
//
// It returns an error only for systemic (non-wired) persistence
// failures. Per-item failures are recorded on the response
// (item.Error + resp.Failed) and never abort the remaining items.
func (o *RunOrchestratorService) stagePersistResults(ctx context.Context, resp *RunTagResponse) error {
	if o.svc.assetFinalizer == nil || o.svc.mainDB == nil {
		// Fail closed: returning success without persisting would surface
		// a run that claims completion for assets that were never
		// committed (godlike/07 no-fake-availability).
		return fmt.Errorf("stagePersistResults: asset finalizer or main DB not wired (cannot persist)")
	}

	for i := range resp.Items {
		item := &resp.Items[i]
		// OUTCOME-SINGLE-TALLY (September 2026): every item that an earlier
		// stage already adjudicated AND tallied (resp.Failed++ / resp.Skipped++)
		// must not be re-adjudicated here. Re-running the Drive-field gate on
		// an item that failed during processing (transcription_failed,
		// transcript_persist_failed, blocked_*) would re-stamp its Status to
		// drive_upload_failed and DOUBLE-COUNT it into resp.Failed, breaking
		// the Found == Processed + Skipped + Failed invariant that
		// EvaluateRunOutcome / EvaluateRunState own. Items that failed during
		// processing never entered the persist path; the Drive-field gate
		// below only applies to items the processor reported as processed.
		if itemStatusAlreadyTallied(item.Status) {
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
		if item.LegacyFileMD5 == "" {
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
			item.Status = "persist_failed"
			item.Error = fmt.Sprintf("persist failed: %v", err)
			resp.Failed++
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
	return nil
}

// itemStatusAlreadyTallied is the canonical single-owner mapping of the
// per-item Status values that an EARLIER stage already counted into
// resp.Failed / resp.Skipped (godlike/06 SSOT: one canonical owner of the
// tally-skip decision). Adding a new early-failure Status in
// stageProcessBatch (or the gate-block classifier) MUST add it here in the
// same commit so stagePersistResults never re-adjudicates it.
//
// Excluded statuses:
//   - media_process_failed  (stageProcessBatch: mediaProcessor.Process error)
//   - transcription_failed  (stageProcessBatch: transcriber.Transcribe error)
//   - transcript_persist_failed (stageProcessBatch: textTrackRepo.UpsertBatch error)
//   - blocked_mode / blocked_daily_limit / blocked_unauthorized /
//     blocked_session_expired (stageProcessBatch: gateBlockShortCircuit —
//     bumped via newGateBlockCounterFor(resp).bumpGateBlock)
//   - dry_run / skipped_existing (stageBuildProcessInputs: resp.Skipped++)
func itemStatusAlreadyTallied(status string) bool {
	switch status {
	case "media_process_failed",
		"transcription_failed",
		"transcript_persist_failed",
		"blocked_mode",
		"blocked_daily_limit",
		"blocked_unauthorized",
		"blocked_session_expired",
		"dry_run",
		"skipped_existing":
		return true
	default:
		return false
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
		SHA256:      item.LegacyFileMD5,
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
			Kind:          string(r.Kind),
			Provider:      "local",
			URI:           r.LocalPath,
			MimeType:      r.MimeType,
			SizeBytes:     r.SizeBytes,
			LegacyFileMD5: r.LegacyFileMD5,
			Width:         r.Width,
			Height:        r.Height,
			FPS:           r.FPS,
			Bitrate:       r.Bitrate,
			Container:     r.Container,
			Codec:         r.Codec,
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

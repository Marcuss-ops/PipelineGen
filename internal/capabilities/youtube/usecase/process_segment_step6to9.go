// Package usecase — process_segment_step6to9.go: canonical owner of
// Steps 6-9 (transcript acquisition + Drive upload + canonical
// localized-clip atomic super-tx).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - Step 6 transcript acquisition lives ONLY in
//     u.media.TextTrackResolver.AcquireSegmentText. NO handler may
//     re-implement priority 3-5 inline (PR-PY-CLIPS-CORRETTE-TRADOTTE
//     Fase 1.a, July 2026).
//   - Step 7 Whisper fallback lives ONLY inside AcquireSegmentText
//     (priority 5). This step does NOT call TranscribeAudio
//     directly — the central resolver owns that decision.
//   - Step 8 Drive upload lives ONLY in process_segment_step8_upload.go.
//   - Step 9 super-tx lives ONLY in process_segment_step9_text_tracks.go.
//
// GOP-06 (November 2026): this file is the orchestration only. The
// per-phase owners are process_segment_step6_text.go (acquisition + .txt
// sidecar), process_segment_step8_upload.go (concurrent Drive uploads) and
// process_segment_step9_text_tracks.go (metadata analysis + atomic commit).
// The invariants are unchanged: u.fail is called once on the caller
// goroutine after Wait, acquisition errors are non-fatal, sidecar write
// errors are fatal, and the commit is one atomic super-tx.
package usecase

import (
	"context"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// step6to9_SubtitlesDriveWriter is the canonical owner of Steps 6-9.
//
// Mutates `out` on success: Item.DriveFileID + Item.DriveLink +
// out.DriveFileID + out.DriveLink (Step 8); IndexedRequestID
// (Step 9). On any typed-error path, `u.fail(...)` has already
// populated `out.Item.Status="failed"` + `out.Item.Error` +
// `out.Error`. The `processed_but_index_blocked` status path
// (BLOCKER #4 closure) mutates the status without returning an
// error (the clip + tracks + cues ARE safe; only the index event
// was suppressed).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b (July 2026): the legacy two-step
// pattern (Step 9 commits clip + outbox, Step 9.5 commits text tracks
// separately) is REPLACED by ONE atomic super-tx that writes media_assets +
// asset_text_tracks + asset_text_track_segments + outbox_events in the SAME
// PostgreSQL transaction.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c (July 2026): the return signature
// surfaces the resolved ResolvedTextBundle to the orchestrator. Step 10's
// metadata enrichment consumes (transcript, languageCode, cues) from the
// bundle instead of re-invoking Whisper. The bundle can be nil when no track
// was acquired; Step 10's contract is fail-closed on nil bundle.
func (u *ProcessYouTubeSegmentUseCase) step6to9_SubtitlesDriveWriter(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	out *youtubetypes.ProcessSegmentResult,
	clipID string,
	startSec int,
	endSec int,
	localPath string,
	fileHash string,
	policyVer string,
) (*detail.ResolvedTextBundle, error) {
	// txtPath is the canonical transcript file path referenced by a
	// CROSS-PACKAGE consumer — clipindexer.lookupTranscriptPath reads
	// `local_path -> TrimSuffix(Ext) + ".txt"`. Keeping the .txt
	// side-channel alive until clipindexer migrates to a typed port
	// (godlike/06 forward-pointer: PR-CLIPINDEXER-TRANSCRIPT-TYPED-PORT).
	txtPath := sidecarPath(localPath)

	bundle, err := u.acquireSegmentText(ctx, cmd, clipID, startSec, endSec, localPath, txtPath)
	if err != nil {
		return nil, u.transcriptSidecarFailure(out, txtPath, err)
	}

	// A configured subtitle root is an explicit persistence contract.
	if bundle != nil && !bundle.IsEmpty() && cmd.SubtitleFolderID != "" && u.media.DriveFolderMgr == nil {
		return nil, u.subtitleDestinationMissing(out)
	}

	// Step 8 — subtitle sidecar + video uploaded concurrently. The helper
	// never mutates `out`; u.fail runs on this goroutine after Wait.
	subtitleUploadErr, videoUploadRes, videoUploadErr := u.uploadSegmentArtifacts(ctx, cmd, out, bundle, txtPath, localPath)
	if subtitleUploadErr != nil {
		return nil, u.uploadFailure(out, "upload subtitle sidecar", true, subtitleUploadErr)
	}
	if videoUploadErr != nil {
		return nil, u.videoUploadFailure(out, clipID, bundle, videoUploadErr)
	}
	u.applyVideoUploadResult(out, videoUploadRes)

	// Step 9 — metadata analysis + localized atomic super-tx.
	clipAsset := buildClipAsset(clipID, cmd, *out, fileHash, policyVer)
	clipAsset, metadataEnrichmentJSON, prepErr := u.prepareCommitMetadata(ctx, clipAsset, cmd, clipID, startSec, endSec, bundle)
	if prepErr != nil {
		return nil, u.fail(out, prepErr)
	}

	eventID, trackCount, handled, commitErr := u.commitLocalizedClip(ctx, cmd, out, clipID, clipAsset, bundle, metadataEnrichmentJSON)
	if commitErr != nil {
		return nil, u.fail(out, commitErr)
	}
	if handled {
		return bundle, nil
	}

	u.finalizeLocalizedClip(ctx, out, bundle, clipID, eventID, trackCount)
	return bundle, nil
}

// Package usecase — process_segment_step6to9.go: canonical owner of
// Steps 6-9 (subtitle slicing + Whisper fallback + Drive upload +
// canonical ClipAsset writer commit).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - Step 6 subtitle slicing (`Subtitles.SliceSubtitles`) lives ONLY here
//   - Step 7 Whisper fallback (`Transcriber.TranscribeAudio` +
//     `os.WriteFile` of the *.txt tempfile) lives ONLY here. NOTE:
//     the os.WriteFile(txtPath, ...) call is referenced by a
//     CROSS-PACKAGE consumer — clipindexer reads local_path → .txt
//     tempfile for Qdrant transcript embedding. Removing it silently
//     disables the Qdrant transcript indexing path until
//     clipindexer migrates to a typed port
//     (godlike/06 SSOT forward-pointer: PR-CLIPINDEXER-TRANSCRIPT-TYPED-PORT).
//   - Step 8 Drive upload (`DriveFolderMgr.UploadFileIfChanged`) lives ONLY here
//   - Step 9 canonical ClipAsset writer commit + ErrOutboxTerminalConflict
//     handling (BLOCKER #4 closure — sets `processed_but_index_blocked`
//     status when the outbox event is suppressed by a dead_letter row)
//     lives ONLY here
//
// godlike/07 no-fake-availability: every non-typed propagation here
// emits an explicit `u.deps.Log.{Warn,Error}` so operator dashboards
// never see a silent retryable/terminal classification.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// step6to9_SubtitlesDriveWriter is the canonical owner of Steps 6-9.
//
// Mutates `out` on success: Item.DriveFileID + Item.DriveLink +
// out.DriveFileID + out.DriveLink (Step 8); IndexedRequestID (Step 9).
// On any typed-error path, `u.fail(...)` has already populated
// `out.Item.Status="failed"` + `out.Item.Error` + `out.Error`. The
// `processed_but_index_blocked` status path (BLOCKER #4 closure)
// mutates the status without returning an error (the clip IS safe;
// the indexing event is the only thing not written).
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
) error {
	// Step 6 — SliceSubtitles (cache hit per Commit G).
	if u.deps.Subtitles != nil {
		if subErr := u.deps.Subtitles.SliceSubtitles(
			ctx, cmd.VideoID, startSec, endSec, localPath,
		); subErr != nil {
			u.deps.Log.Warn("subtitle slice failed, falling back to Whisper",
				zap.String("clip_id", clipID), zap.Error(subErr))
			// Step 7 — Whisper fallback.
			if u.deps.Transcriber != nil {
				transcript, wErr := u.deps.Transcriber.TranscribeAudio(ctx, localPath)
				if wErr == nil && transcript != "" {
					// NOTE: the os.WriteFile(txtPath, ...) call below is
					// referenced by a CROSS-PACKAGE consumer — DO NOT
					// drop per Step 7 refactor without migrating the
					// consumer to a typed port first.
					//
					// Canonical surface: clipindexer.lookupTranscriptPath
					// (internal/infrastructure/indexing/clipindexer/
					// indexing_api_persistence.go) reads
					// `local_path -> TrimSuffix(Ext) + ".txt"` and
					// os.Stat's it for the Qdrant transcript embedding
					// lookup (QDRANT transcript_embedding column +
					// persTranscriptEmbedding writer). Removing this
					// write silently disables the Qdrant transcript
					// indexing path until clipindexer migrates to a
					// typed port
					// (godlike/06 SSOT forward-pointer:
					// PR-CLIPINDEXER-TRANSCRIPT-TYPED-PORT).
					txtPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".txt"
					_ = os.WriteFile(txtPath, []byte(transcript), 0o644)
					u.deps.Log.Info("whisper fallback transcribed clip",
						zap.String("clip_id", clipID),
						zap.String("txt_path", txtPath))
				} else if wErr != nil {
					u.deps.Log.Warn("whisper fallback failed",
						zap.String("clip_id", clipID), zap.Error(wErr))
				}
			}
		}
	}

	// Step 8 — DriveUploadFileIfChanged.
	if u.deps.DriveFolderMgr != nil && cmd.DriveFolderID != "" && localPath != "" {
		if upRes, _, upErr := u.deps.DriveFolderMgr.UploadFileIfChanged(
			ctx, localPath, cmd.DriveFolderID, out.Item.Filename,
			deriveNormalizedGroup(cmd), cmd.VideoID,
		); upErr == nil && upRes != nil {
			out.Item.DriveFileID = upRes.FileID
			out.Item.DriveLink = upRes.WebViewLink
		} else if upErr != nil {
			retryable := IsTransientExtractionError(upErr)
			if retryable {
				u.deps.Log.Warn("drive upload transient failure (will be classified retryable by parent)",
					zap.String("clip_id", clipID), zap.Error(upErr))
			} else {
				u.deps.Log.Error("drive upload terminal failure (will be classified terminal by parent)",
					zap.String("clip_id", clipID), zap.Error(upErr))
			}
			typed := NewExtractionError(
				FailureCodeDriveUploadFailed,
				retryable,
				fmt.Sprintf("drive upload failed: %v", upErr),
				upErr,
			)
			return u.fail(out, typed)
		}
	}
	out.DriveFileID = out.Item.DriveFileID
	out.DriveLink = out.Item.DriveLink

	// Step 9 — ClipAtomicWriter. Build the canonical ClipAsset
	// and delegate to the writer, which owns the outbox envelope
	// exclusively. The caller MUST NOT supply a custom payload
	// (Blocco 1.1: the previous ad-hoc payload caused every
	// indexing event to land in dead_letter because the consumer
	// requires the canonical BuildReindexEnvelopeV1 shape).
	//
	// BLOCKER #4 closure (audit 2026-07-03): when the outbox event
	// is suppressed by an existing terminal row (dead_letter or
	// superseded), the writer returns ErrOutboxTerminalConflict.
	// We surface this as "processed_but_index_blocked" — the clip
	// was written to the DB but no new indexing event was created.
	if u.deps.Writer != nil {
		asset := buildClipAsset(clipID, cmd, *out, fileHash, policyVer)
		event := youtubeports.IndexEventPayload{
			Type:        "asset.index.requested",
			AggregateID: clipID,
			CreatedAt:   time.Now().UTC(),
		}
		if wErr := u.deps.Writer.CommitClipAndIndexEvent(
			ctx, clipID, asset, event,
		); wErr != nil {
			// BLOCKER #4: terminal outbox conflict → processed_but_index_blocked.
			// The clip row was committed; the index event was suppressed by
			// a dead_letter/superseded row. Not a failure — the clip is safe,
			// but the operator must resolve the terminal event before indexing.
			if errors.Is(wErr, youtubeports.ErrOutboxTerminalConflict) {
				out.Item.Status = "processed_but_index_blocked"
				out.Status = "processed_but_index_blocked"
				u.deps.Log.Warn("clip committed but index blocked by terminal outbox row (BLOCKER #4)",
					zap.String("clip_id", clipID),
					zap.Error(wErr))
				return nil
			}
			typed := NewExtractionError(FailureCodeWriterFailed, false,
				fmt.Sprintf("writer failed: %v", wErr), wErr)
			return u.fail(out, typed)
		}
		out.IndexedRequestID = event.AggregateID
	}

	return nil
}

// Package usecase — process_segment_step6to9.go: canonical owner of
// Steps 6-9 (transcript acquisition + Drive upload + canonical
// ClipAsset writer commit + post-commit text-track materialisation).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - Step 6 transcript acquisition lives ONLY in
//     u.deps.TextTrackResolver.AcquireSegmentText. NO handler may
//     re-implement priority 3-5 inline (PR-PY-CLIPS-CORRETTE-TRADOTTE
//     Fase 1.a, July 2026).
//   - Step 7 Whisper fallback lives ONLY inside AcquireSegmentText
//     (priority 5). This step does NOT call TranscribeAudio
//     directly — the central resolver owns that decision. The Step 10
//     removal happens in Fase 1.c; this step already delegates.
//   - Step 8 Drive upload lives ONLY here.
//   - Step 9 canonical ClipAsset writer commit + ErrOutboxTerminalConflict
//     handling lives ONLY here.
//   - Step 9.5 (Fase 1.a) materialises all text tracks AFTER the
//     clip row exists in media_assets (FK fix). Atomic same-tx write
//     is scope of Fase 2.b; Fase 1.a uses post-step-9 persistence
//     as a safe interim pattern.
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
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// step6to9_SubtitlesDriveWriter is the canonical owner of Steps 6-9.
//
// Mutates `out` on success: Item.DriveFileID + Item.DriveLink +
// out.DriveFileID + out.DriveLink (Step 8); IndexedRequestID
// (Step 9). On any typed-error path, `u.fail(...)` has already
// populated `out.Item.Status="failed"` + `out.Item.Error` +
// `out.Error`. The `processed_but_index_blocked` status path
// (BLOCKER #4 closure) mutates the status without returning an
// error (the clip IS safe; the indexing event is the only thing
// not written).
//
// Post-Step-9 partial-state path (Fase 1.a):
//
//	When the text-track materialisation fails AFTER the clip row
//	was safely committed, the status mutates to
//	"processed_but_text_pending" and the function returns nil
//	(mirrors BLOCKER #4). Operator dashboards see the partial-state
//	class WITHOUT weakening the clip-commit guarantee.
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
	// txtPath is the canonical transcript file path referenced by a
	// CROSS-PACKAGE consumer — clipindexer.lookupTranscriptPath reads
	// `local_path -> TrimSuffix(Ext) + ".txt"` for Qdrant transcript
	// embedding. Keeping the .txt side-channel alive until
	// clipindexer migrates to a typed port
	// (godlike/06 SSOT forward-pointer:
	// PR-CLIPINDEXER-TRANSCRIPT-TYPED-PORT).
	txtPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".txt"

	// Step 6 — TextTrackResolver.AcquireSegmentText (Fase 1.a).
	// The central resolver owns the 5-level chain; this method is
	// the SOLE canonical entry point that handlers call. Subtitle
	// fetcher errors are logged + swallowed so the chain falls
	// through to Whisper; Whisper errors are returned verbatim.
	var bundle *asset.ResolvedTextBundle
	if u.deps.TextTrackResolver != nil {
		var acqErr error
		bundle, acqErr = u.deps.TextTrackResolver.AcquireSegmentText(ctx, TextTrackAcquireRequest{
			ClipID:       clipID,
			VideoID:      cmd.VideoID,
			StartSec:     startSec,
			EndSec:       endSec,
			PayloadTexts: cmd.Segment.Texts,
			LocalPath:    localPath,
		})
		if acqErr != nil {
			// Non-fatal — a Whisper failure must NOT kill the pipeline.
			// Log + clear bundle; continue to Steps 8-9. The clip will
			// be persisted with no transcript (so future re-runs can
			// backfill via Fase 5 text-tracks admin command).
			u.deps.Log.Warn("text track acquisition failed (Whisper fallback returned an error); continuing with empty bundle — clip will be persisted without text tracks; backfill via Fase 5 admin command",
				zap.String("clip_id", clipID), zap.Error(acqErr))
			bundle = nil
		} else if bundle != nil && !bundle.IsEmpty() {
			// Backward-compat: keep the .txt side-channel alive for
			// clipindexer.lookupTranscriptPath (godlike/06 SSOT
			// forward-pointer: PR-CLIPINDEXER-TRANSCRIPT-TYPED-PORT).
			// Errors here are LOGGED, not swallowed, per Fase 1.a
			// (audit 2026-07-11 §2.a).
			if writeErr := os.WriteFile(txtPath, []byte(bundle.PlainText), 0o644); writeErr != nil {
				u.deps.Log.Warn("transcript .txt side-channel write failed (clipindexer.lookupTranscriptPath Qdrant transcript embedding may miss this clip)",
					zap.String("txt_path", txtPath),
					zap.Error(writeErr))
			}
		}
	}

	// Step 8 — DriveUploadFileIfChanged (unchanged, body verbatim
	// from pre-split).
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
		clipAsset := buildClipAsset(clipID, cmd, *out, fileHash, policyVer)
		event := youtubeports.IndexEventPayload{
			Type:        "asset.index.requested",
			AggregateID: clipID,
			CreatedAt:   time.Now().UTC(),
		}
		if wErr := u.deps.Writer.CommitClipAndIndexEvent(
			ctx, clipID, clipAsset, event,
		); wErr != nil {
			if errors.Is(wErr, youtubeports.ErrOutboxTerminalConflict) {
				out.Item.Status = "processed_but_index_blocked"
				out.Status = "processed_but_index_blocked"
				u.deps.Log.Warn("clip committed but index blocked by terminal outbox row (BLOCKER #4)",
					zap.String("clip_id", clipID),
					zap.Error(wErr))
				// Continue to Step 9.5 — text tracks should still be
				// materialised even if the index event was blocked.
			} else {
				typed := NewExtractionError(FailureCodeWriterFailed, false,
					fmt.Sprintf("writer failed: %v", wErr), wErr)
				return u.fail(out, typed)
			}
		} else {
			out.IndexedRequestID = event.AggregateID
		}
	}

	// Step 9.5 — Materialise all text tracks AFTER the clip row exists
	// (Fase 1.a FK fix). Mirrors the BLOCKER #4 partial-state pattern:
	// a save failure is logged loudly + status mutated, pipeline does
	// NOT fail. The clip + outbox event (when Step 9 succeeded) are
	// already persisted safely; missing text tracks are a backfill
	// concern (Fase 5 admin command).
	//
	// Append order is INTENTIONALLY PAYLOAD-FIRST, BUNDLE-LAST: SQLite's
	// ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE is
	// last-write-wins per-batch, so the chain's selected primary
	// bundle (priority 1-5 winner) overrides any payload row that
	// happens to share the same (lang, kind) key. Other-kind payload
	// rows (title/summary/description) never collide with the
	// bundle's transcript row (different kinds → independent keys).
	// godlike/06 SSOT: the chain's selection is the authoritative
	// source — flipping the order here would silently demote priority
	// 2-5 wins back to the payload (audit 2026-07-11 §2.b).
	if u.deps.TextTrackResolver != nil {
		var tracks []asset.TextTrack
		if len(cmd.Segment.Texts) > 0 {
			tracks = append(tracks,
				u.deps.TextTrackResolver.MaterializePayloadTexts(clipID, cmd.Segment.Texts)...)
		}
		if bundle != nil && !bundle.IsEmpty() {
			tracks = append(tracks, bundleToTextTracks(clipID, bundle, asset.TextTrackTranscript)...)
		}
		if len(tracks) > 0 {
			if saveErr := u.deps.TextTrackResolver.SaveMany(ctx, tracks); saveErr != nil {
				// godlike/07 honest lock: typed partial-state event,
				// not a hard fail. The clip is durable; backfill is a
				// separate admin step (Fase 5).
				u.deps.Log.Error("text track save failed AFTER clip write; clip is fully persisted; backfill via Fase 5 text-tracks admin command",
					zap.String("clip_id", clipID),
					zap.Int("rows_attempted", len(tracks)),
					zap.Error(saveErr))
				// Compose partial-state status. If Step 9 already set
				// processed_but_index_blocked, we surface text-pending
				// alongside (multi-axis partial-state).
				if out.Item.Status != "processed_but_index_blocked" {
					out.Item.Status = "processed_but_text_pending"
					out.Status = "processed_but_text_pending"
				}
				return nil
			}
			u.deps.Log.Info("text tracks materialised",
				zap.String("clip_id", clipID),
				zap.Int("rows_committed", len(tracks)))
		}
	}

	return nil
}

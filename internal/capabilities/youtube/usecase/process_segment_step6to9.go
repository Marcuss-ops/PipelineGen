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
//     directly — the central resolver owns that decision. The Step 10
//     removal happens in Fase 1.c; this step already delegates.
//   - Step 8 Drive upload lives ONLY here.
//   - Step 9 super-tx lives ONLY here. PR-PY-CLIPS-CORRETTE-TRADOTTE
//     Fase 2.b (July 2026) collapsed legacy Step 9 (clip only) +
//     Step 9.5 (text tracks separate write) into ONE atomic call
//     to LocalizedClipWriter.CommitClipTextAndIndexEvent. The
//     single super-tx writes media_assets + asset_text_tracks +
//     asset_text_track_segments + outbox_events in ONE
//     SQLite transaction; any failure rolls back EVERY surface,
//     eliminating the Fase 1.a "clip persisted but text-pending"
//     partial-state window.
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func isAsyncEnrichmentEnabled() bool {
	v := strings.TrimSpace(os.Getenv("VELOX_YOUTUBE_ASYNC_ENRICHMENT"))
	return strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "on") || strings.EqualFold(v, "yes")
}

// AsyncEnrichmentEnabled reports whether durable async metadata enrichment
// is enabled. It is exported so the composition root can fail closed when
// the gate is on but the PostgreSQL media outbox consumer cannot be wired
// (godlike/07: never boot with a silent enrichment drop).
func AsyncEnrichmentEnabled() bool { return isAsyncEnrichmentEnabled() }

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
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b (July 2026):
// the legacy two-step pattern (Step 9 commits clip + outbox,
// Step 9.5 commits text tracks separately) is REPLACED by ONE
// atomic super-tx that writes media_assets +
// asset_text_tracks + asset_text_track_segments + outbox_events
// in the SAME SQLite transaction. Failure paths collapse into a
// single typed error from the writer; the prior
// "clip persisted but text-pending" partial-state window is now
// ARCHITECTURALLY IMPOSSIBLE.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c (July 2026):
// the return signature is EXTENDED to surface the resolved
// ResolvedTextBundle to the orchestrator. Step 10's metadata
// enrichment now consumes (transcript, languageCode, cues)
// from the bundle instead of re-invoking Whisper. The bundle
// can be nil when no track was acquired (TextTrackResolver
// nil OR all 5 priorities failed); Step 10's contract is
// fail-closed on nil bundle (empty transcript, empty cues)
// so the metadata service's downstream behavior is unchanged
// (empty transcript → low quality score, no crash).
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
	var bundle *detail.ResolvedTextBundle
	if u.media.TextTrackResolver != nil {
		var acqErr error
		bundle, acqErr = u.media.TextTrackResolver.AcquireSegmentText(ctx, TextTrackAcquireRequest{
			ClipID:       clipID,
			VideoID:      cmd.VideoID,
			StartSec:     startSec,
			EndSec:       endSec,
			PayloadTexts: cmd.Segment.Texts,
			LocalPath:    localPath,
		})
		if acqErr != nil {
			// Non-fatal — a Whisper failure must NOT kill the pipeline.
			// Log + clear bundle; continue to Steps 8-9. The clip + an
			// empty text-track set will be persisted atomically
			// (the writer's policy validation is skipped when
			// RequireTranscriptReady=false, which is the default).
			u.core.Log.Warn("text track acquisition failed (Whisper fallback returned an error); continuing with empty bundle — clip will be persisted without text tracks; backfill via Fase 5 admin command",
				zap.String("clip_id", clipID), zap.Error(acqErr))
			bundle = nil
		} else if bundle != nil && !bundle.IsEmpty() {
			// Backward-compat: keep the .txt side-channel alive for
			// clipindexer.lookupTranscriptPath (godlike/06 SSOT
			// forward-pointer: PR-CLIPINDEXER-TRANSCRIPT-TYPED-PORT).
			// Errors here are LOGGED, not swallowed, per Fase 1.a
			// (audit 2026-07-11 §2.a).
			if writeErr := os.WriteFile(txtPath, []byte(bundle.PlainText), 0o644); writeErr != nil {
				return nil, u.fail(out, NewExtractionError(
					FailureCodeWriterFailed, true,
					fmt.Sprintf("write transcript sidecar %s: %v", txtPath, writeErr), writeErr))
			}
		}
	}

	// Step 8 + subtitle sidecar (Sept 2026): once txtPath/localPath and the
	// resolved folder IDs exist there is NO data dependency between the two
	// uploads, so they run CONCURRENTLY instead of subtitle-then-video. Drive
	// uploads stay bounded by the fan-out concurrency (one slot per segment),
	// so this doubles per-segment overlap without unbounded Drive load.
	//
	// u.fail mutates `out`, so it is called exactly ONCE on the caller
	// goroutine after Wait — never from a worker goroutine.
	var (
		subtitleUploadErr error
		videoUploadRes    *youtubeports.UploadResultDTO
		videoUploadErr    error
	)
	uploadGroup, uploadCtx := errgroup.WithContext(ctx)

	// Subtitle sidecar destination. A configured subtitle root is an explicit
	// persistence contract: never let a READY Whisper bundle disappear merely
	// because the per-clip-folder flag was omitted or was not propagated by an
	// older request adapter.
	if bundle != nil && !bundle.IsEmpty() && cmd.SubtitleFolderID != "" {
		if u.media.DriveFolderMgr == nil {
			return nil, u.fail(out, NewExtractionError(
				FailureCodeDriveUploadFailed, false,
				"subtitle destination requested but Drive folder manager is not wired", nil))
		}
		// Subtitle folder is ALREADY the final upload target: the
		// extraction orchestrator resolved the per-video subfolder ONCE
		// before fan-out (resolveSubtitleDestination), so no per-segment
		// GetOrCreateFolder happens here (Sept 2026 N→1 contract).
		subtitleFolderID := cmd.SubtitleFolderID
		subtitleName := filepath.Base(txtPath)
		uploadGroup.Go(func() error {
			_, _, err := u.media.DriveFolderMgr.UploadFileIfChanged(
				uploadCtx, txtPath, subtitleFolderID, subtitleName,
				deriveNormalizedGroup(cmd), cmd.VideoID,
			)
			subtitleUploadErr = err
			return nil
		})
	}

	// Step 8 — DriveUploadFileIfChanged.
	if u.media.DriveFolderMgr != nil && cmd.DriveFolderID != "" && localPath != "" {
		uploadGroup.Go(func() error {
			res, _, err := u.media.DriveFolderMgr.UploadFileIfChanged(
				uploadCtx, localPath, cmd.DriveFolderID, out.Item.Filename,
				deriveNormalizedGroup(cmd), cmd.VideoID,
			)
			videoUploadRes, videoUploadErr = res, err
			return nil
		})
	}
	_ = uploadGroup.Wait()

	if subtitleUploadErr != nil {
		return nil, u.fail(out, NewExtractionError(
			FailureCodeDriveUploadFailed, true,
			fmt.Sprintf("upload subtitle sidecar: %v", subtitleUploadErr), subtitleUploadErr))
	}
	if videoUploadErr != nil {
		// godlike/07 observability: if a transcript bundle was
		// acquired BEFORE the Drive upload failed, surface that
		// the bundle was discarded (re-acquired on next retry).
		// Without this log line, operators cannot distinguish
		// "transcript was never acquired" from "transcript was
		// acquired and dropped due to upstream failure".
		if bundle != nil && !bundle.IsEmpty() {
			u.core.Log.Warn("text bundle dropped due to upstream Drive upload failure; transcript will be re-acquired on the next retry — no atomic super-tx executed",
				zap.String("clip_id", clipID),
				zap.String("bundle_language", bundle.LanguageCode),
				zap.Int("bundle_cues", len(bundle.Cues)),
				zap.Error(videoUploadErr))
		}
		retryable := IsTransientExtractionError(videoUploadErr)
		if retryable {
			u.core.Log.Warn("drive upload transient failure (will be classified retryable by parent)",
				zap.String("clip_id", clipID), zap.Error(videoUploadErr))
		} else {
			u.core.Log.Error("drive upload terminal failure (will be classified terminal by parent)",
				zap.String("clip_id", clipID), zap.Error(videoUploadErr))
		}
		typed := NewExtractionError(
			FailureCodeDriveUploadFailed,
			retryable,
			fmt.Sprintf("drive upload failed: %v", videoUploadErr),
			videoUploadErr,
		)
		return nil, u.fail(out, typed)
	}
	if videoUploadRes != nil {
		out.Item.DriveFileID = videoUploadRes.FileID
		out.Item.DriveLink = videoUploadRes.WebViewLink
	}
	out.DriveFileID = out.Item.DriveFileID
	out.DriveLink = out.Item.DriveLink

	// Step 9 — localized atomic super-tx
	// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.b, July 2026).
	//
	// Replaces the Fase 1.a two-step pattern (CommitClipAndIndexEvent
	// THEN TextTrackResolver.SaveMany separately) with ONE cold
	// call:
	//
	//   LocalizedClipWriter.CommitClipTextAndIndexEvent:
	//     BEGIN
	//     UPSERT media_assets
	//     UPSERT asset_text_tracks (RETURNING id for FK)
	//     BATCH INSERT asset_text_track_segments (in seq_no order)
	//     INSERT outbox_events
	//     COMMIT
	//
	// Any failure rolls back ALL FOUR surfaces — the previous
	// "clip persisted but text-pending" partial-state window is
	// architecturally impossible.
	//
	// Append order is INTENTIONALLY PAYLOAD-FIRST, BUNDLE-LAST
	// (preserved from Fase 1.a): SQLite's ON
	// CONFLICT(asset_id, language_code, text_kind) DO UPDATE is
	// last-write-wins per-batch, so the chain's selected primary
	// bundle (priority 1-5 winner) overrides any payload row that
	// happens to share the same (lang, kind) key. Other-kind
	// payload rows (title/summary/description) never collide with
	// the bundle's transcript row (different kinds → independent
	// keys).
	//
	// godlike/06 SSOT: the chain's selection is the authoritative
	// source — flipping the order here would silently demote
	// priority 2-5 wins back to the payload (audit 2026-07-11 §2.b).
	clipAsset := buildClipAsset(clipID, cmd, *out, fileHash, policyVer)

	// ── Metadata analysis BEFORE the canonical commit ───────────────
	// (PR-ASSET-COMMITTER-ENRICHMENT, August 2026): the metadata
	// analyzer runs BEFORE the atomic super-tx so the SINGLE commit
	// carries the complete semantic snapshot (summary / topics /
	// speakers / mentioned people / hook / quality score / tags) plus
	// the canonical taxonomy. The post-commit metadata-only write
	// (Step 10) is retired — there is no second media_assets write and
	// no second asset.index.requested event. Analysis failure is
	// fail-closed: a semantically-poor clip is never committed.
	//
	// Async metadata enrichment (Sept 2026): when VELOX_YOUTUBE_ASYNC_ENRICHMENT
	// is on, the LLM analysis is deferred to the media outbox worker instead
	// of running inline. The analysis input (transcript included) is
	// serialized into the commit so the metadata.enrich.requested event the
	// writer emits carries everything the worker needs — no second read of
	// partial state. The clip commits with the raw segment metadata and the
	// worker writes the semantic snapshot + asset.index.requested atomically.
	// Disabled by default (fail-closed synchronous enrichment).
	var metadataEnrichmentJSON string
	if u.metadata.MetadataService != nil {
		if isAsyncEnrichmentEnabled() {
			payload, mErr := json.Marshal(buildClipMetadataInput(cmd, clipID, startSec, endSec, bundle))
			if mErr != nil {
				return nil, u.fail(out, NewExtractionError(FailureCodeMetadataFailed, false,
					fmt.Sprintf("marshal async metadata enrichment payload: %v", mErr), mErr))
			}
			metadataEnrichmentJSON = string(payload)
			u.core.Log.Debug("async enrichment gate active: committing clip without Ollama analysis",
				zap.String("clip_id", clipID))
		} else {
			enrichStart := time.Now()
			enrichment, analyzeErr := u.analyzeClipForCommit(ctx, cmd, clipID, startSec, endSec, bundle)
			// Sept 2026 enrichment SRE surface: the synchronous run is
			// recorded on the canonical enrichment metrics (total /
			// duration / failures) even when it fails — the failure is
			// counted BEFORE the fail-closed clip abort below so the
			// dashboard reflects the analyzer's real error rate, not
			// just the clips that survived it.
			u.recordEnrichmentOutcome(analyzeErr, enrichStart)
			if analyzeErr != nil {
				typed := NewExtractionError(FailureCodeMetadataFailed, false,
					fmt.Sprintf("metadata analysis failed before commit: %v", analyzeErr), analyzeErr)
				u.core.Log.Warn("metadata analysis failed BEFORE clip write — clip not committed",
					zap.String("clip_id", clipID),
					zap.String("failure_code", string(FailureCodeMetadataFailed)),
					zap.Error(analyzeErr))
				return nil, u.fail(out, typed)
			}
			clipAsset = foldEnrichmentIntoClipAsset(clipAsset, enrichment)
		}
	}

	if u.metadata.LocalizedWriter != nil {
		event := youtubeports.IndexEventPayload{
			AggregateID: clipID,
			CreatedAt:   time.Now().UTC(),
		}

		var tracks []detail.TextTrack
		if len(cmd.Segment.Texts) > 0 && u.media.TextTrackResolver != nil {
			tracks = append(tracks,
				u.media.TextTrackResolver.MaterializePayloadTexts(clipID, cmd.Segment.Texts)...)
		}
		if bundle != nil && !bundle.IsEmpty() {
			tracks = append(tracks,
				bundleToTextTracks(clipID, bundle, detail.TextTrackTranscript)...)
		}

		var timedTracks []localized.TimedTextTrack
		if u.media.TextTrackResolver != nil {
			if tt := u.media.TextTrackResolver.bundleToTimedTrack(clipID, bundle); tt != nil {
				timedTracks = append(timedTracks, *tt)
			}
		}

		requireAllLanguages := u.observability.RequireAllLanguagesBeforeVideo
		if cmd.RequireAllLanguagesBeforeVideo != nil {
			requireAllLanguages = *cmd.RequireAllLanguagesBeforeVideo
		}

		requireTranscript := u.observability.RequireTranscriptReady
		if cmd.RequireTranscriptReady != nil {
			requireTranscript = *cmd.RequireTranscriptReady
		}

		superCmd := localized.CommitLocalizedClipCommand{
			Clip:                           clipAsset,
			TextTracks:                     tracks,
			TimedTracks:                    timedTracks,
			IndexEvent:                     event,
			RequireTranscriptReady:         requireTranscript,
			RequireAllLanguagesBeforeVideo: requireAllLanguages,
			PreferredLanguages:             u.observability.PreferredLanguages,
			MetadataEnrichmentJSON:         metadataEnrichmentJSON,
		}

		if wErr := u.metadata.LocalizedWriter.CommitClipTextAndIndexEvent(ctx, superCmd); wErr != nil {
			// BLOCKER #4 closure (audit 2026-07-03): when the outbox
			// row insert is suppressed by an existing terminal row
			// (dead_letter or superseded), the writer returns
			// ErrOutboxTerminalConflict. The clip + tracks + cues
			// ARE safe (committed atomically per Fase 2.b); only
			// the index event was blocked. We surface
			// "processed_but_index_blocked" — mirrors the BLOCKER #4
			// schema without breaking the new atomically-durable text
			// tracks guarantee.
			if errors.Is(wErr, youtubeports.ErrOutboxTerminalConflict) {
				out.Item.Status = "processed_but_index_blocked"
				out.Status = "processed_but_index_blocked"
				u.core.Log.Warn("clip + tracks + cues committed but index blocked by terminal outbox row (BLOCKER #4)",
					zap.String("clip_id", clipID),
					zap.Error(wErr))
				return bundle, nil
			}
			// godlike/07 typed-error contract: a policy violation
			// (ErrClipLocaleNotReady) gets the same fail-closed
			// treatment as a generic writer failure. The terminal
			// state is reached without partial row visibility
			// (the writer rolled back the tx before the error
			// surfaced).
			if localized.IsClipLocaleNotReady(wErr) {
				typed := NewExtractionError(
					FailureCodeWriterFailed,
					false,
					fmt.Sprintf("writer rejected locale-not-ready: %v", wErr),
					wErr,
				)
				u.core.Log.Error("writer rejected locale-not-ready policy (Fase 2.b atomic tx rolled back; no rows visible)",
					zap.String("clip_id", clipID), zap.Error(wErr))
				return nil, u.fail(out, typed)
			}
			typed := NewExtractionError(FailureCodeWriterFailed, false,
				fmt.Sprintf("localized writer failed: %v", wErr), wErr)
			u.core.Log.Error("localized writer terminal failure (Fase 2.b atomic tx rolled back; no rows visible)",
				zap.String("clip_id", clipID), zap.Error(wErr))
			return nil, u.fail(out, typed)
		}
		out.IndexedRequestID = event.AggregateID
		u.core.Log.Info("localized clip super-tx committed",
			zap.String("clip_id", clipID),
			zap.Int("text_tracks", len(tracks)),
			zap.Int("timed_tracks", len(timedTracks)))
		// NOTE (Sept 2026): there is NO legacy CommitClipAndIndexEvent
		// downgrade branch anymore — LocalizedWriter is required at
		// composition time (ValidateProcessSegmentSubBundles), so the
		// per-segment pipeline has exactly ONE commit contract.
	}

	return bundle, nil
}

// buildClipMetadataInput is the SINGLE canonical constructor of the
// metadata-analyzer input for one segment. Both the synchronous path
// (analyzeClipForCommit) and the async path (the serialized
// metadata.enrich.requested payload) read the SAME input shape, so the two
// enrichment modes cannot drift (godlike/06 SSOT).
func buildClipMetadataInput(
	cmd youtubetypes.ProcessSegmentCommand,
	clipID string,
	startSec int,
	endSec int,
	bundle *detail.ResolvedTextBundle,
) youtubetypes.ClipMetadataInput {
	transcript := ""
	if bundle != nil && !bundle.IsEmpty() {
		transcript = bundle.PlainText
	}
	return youtubetypes.ClipMetadataInput{
		ClipID:           clipID,
		Title:            cmd.Segment.Name,
		Description:      segmentDescription(cmd.Segment.Texts),
		Summary:          cmd.Segment.Summary,
		Tags:             append([]string(nil), cmd.Segment.Tags...),
		Transcript:       transcript,
		ClipDuration:     endSec - startSec,
		SourceURL:        cmd.VideoURL,
		SourceTitle:      cmd.Segment.SourceTitle,
		SourceChannel:    cmd.Segment.SourceChannel,
		SourceProvider:   "youtube",
		VideoID:          cmd.VideoID,
		ClipStartSec:     startSec,
		ClipEndSec:       endSec,
		PolicyVersion:    cmd.PolicyVersion,
		Group:            deriveNormalizedGroup(cmd),
		Hook:             cmd.Segment.Hook,
		SearchVisibility: cmd.Segment.SearchVisibility,
		Topics:           append([]string(nil), cmd.Segment.Topics...),
		Speakers:         append([]string(nil), cmd.Segment.Speakers...),
		MentionedPeople:  append([]string(nil), cmd.Segment.MentionedPeople...),
	}
}

// recordEnrichmentOutcome reports one synchronous metadata-enrichment
// run to the canonical enrichment metrics (Sept 2026). Nil-safe: tests
// and minimal compositions without a recorder skip silently. total is
// counted for every run (success AND failure); failures are counted on
// the dedicated failure counter so the SRE surface never has to derive
// an error rate from a delta.
func (u *ProcessYouTubeSegmentUseCase) recordEnrichmentOutcome(analyzeErr error, start time.Time) {
	if u == nil || u.observability.EnrichmentMetrics == nil {
		return
	}
	m := u.observability.EnrichmentMetrics
	m.IncEnrichmentTotal()
	m.ObserveEnrichmentDuration(time.Since(start).Seconds())
	if analyzeErr != nil {
		m.IncEnrichmentFailures()
	}
}

// analyzeClipForCommit runs the PURE metadata analyzer (MetadataAnalyzer.
// AnalyzeClip) against the resolved transcript bundle. It returns the
// CanonicalClipEnrichment that the caller folds into the ClipAsset BEFORE
// the canonical atomic commit — the analyzer never writes media_assets.
//
// godlike/07 fail-closed: a nil MetadataService yields a zero enrichment
// (no analysis) and the caller commits the caller-supplied segment metadata
// verbatim. An analyzer error is returned to the caller, which MUST fail
// the run BEFORE the commit (no semantically-poor clip is persisted).
func (u *ProcessYouTubeSegmentUseCase) analyzeClipForCommit(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	clipID string,
	startSec int,
	endSec int,
	bundle *detail.ResolvedTextBundle,
) (ytmetadata.CanonicalClipEnrichment, error) {
	if u.metadata.MetadataService == nil {
		return ytmetadata.CanonicalClipEnrichment{}, nil
	}
	return u.metadata.MetadataService.AnalyzeClip(ctx, buildClipMetadataInput(cmd, clipID, startSec, endSec, bundle))
}

// foldEnrichmentIntoClipAsset merges the analyzer's CanonicalClipEnrichment
// into the canonical ClipAsset. Analyzer-produced values win over the raw
// segment metadata (they are the authoritative semantic snapshot); empty
// enrichment fields leave the existing value untouched. The canonical search
// text is recomputed from the FOLDED metadata so the search surface stays
// consistent with the committed summary/topics/speakers/mentioned people/hook.
func foldEnrichmentIntoClipAsset(asset youtubetypes.ClipAsset, en ytmetadata.CanonicalClipEnrichment) youtubetypes.ClipAsset {
	if en.Summary != "" {
		asset.Metadata.Summary = en.Summary
	}
	if len(en.Topics) > 0 {
		asset.Metadata.Topics = en.Topics
	}
	if len(en.Speakers) > 0 {
		asset.Metadata.Speakers = en.Speakers
	}
	if len(en.MentionedPeople) > 0 {
		asset.Metadata.MentionedPeople = en.MentionedPeople
	}
	if en.Hook != "" {
		asset.Metadata.Hook = en.Hook
	}
	if en.QualityScore != 0 {
		asset.Metadata.QualityScore = en.QualityScore
	}
	if en.Description != "" {
		asset.Metadata.Description = en.Description
	}
	if len(en.Tags) > 0 {
		asset.Metadata.Tags = en.Tags
	}
	// Recompute from the folded snapshot so search_text reflects the
	// committed semantic fields (godlike/06 SSOT: one composer).
	asset.SearchText = composeYouTubeClipSearchText(asset.Metadata, asset.Metadata.Hook)
	return asset
}

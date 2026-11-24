// Package usecase — process_segment_step9_text_tracks.go (GOP-06, November 2026).
//
// Step 9 owner: metadata analysis BEFORE the canonical commit, the single
// atomic super-tx that writes media_assets + asset_text_tracks +
// asset_text_track_segments + outbox_events, and the outcome handling for
// that commit.
//
// Append order is INTENTIONALLY PAYLOAD-FIRST, BUNDLE-LAST: the
// ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE is
// last-write-wins per-batch, so the chain's selected primary bundle
// (priority 1-5 winner) overrides any payload row that happens to share the
// same (lang, kind) key. godlike/06 SSOT: the chain's selection is the
// authoritative source — flipping the order silently demotes priority 2-5 wins
// back to the payload (audit 2026-07-11 §2.b).
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
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

// prepareCommitMetadata runs the analyzer BEFORE the atomic super-tx so the
// SINGLE commit carries the complete semantic snapshot (summary / topics /
// speakers / mentioned people / hook / quality score / tags) plus the
// canonical taxonomy. Analysis failure is fail-closed.
//
// When VELOX_YOUTUBE_ASYNC_ENRICHMENT is on, the LLM analysis is deferred to
// the media outbox worker: the analysis input (transcript included) is
// serialized so the metadata.enrich.requested event carries everything the
// worker needs. Disabled by default.
func (u *ProcessYouTubeSegmentUseCase) prepareCommitMetadata(
	ctx context.Context,
	clipAsset youtubetypes.ClipAsset,
	cmd youtubetypes.ProcessSegmentCommand,
	clipID string,
	startSec, endSec int,
	bundle *detail.ResolvedTextBundle,
) (youtubetypes.ClipAsset, string, *ExtractionError) {
	if u.metadata.MetadataService == nil {
		return clipAsset, "", nil
	}

	if isAsyncEnrichmentEnabled() {
		payload, mErr := json.Marshal(buildClipMetadataInput(cmd, clipID, startSec, endSec, bundle))
		if mErr != nil {
			return clipAsset, "", NewExtractionError(FailureCodeMetadataFailed, false,
				fmt.Sprintf("marshal async metadata enrichment payload: %v", mErr), mErr)
		}
		u.core.Log.Debug("async enrichment gate active: committing clip without Ollama analysis",
			zap.String("clip_id", clipID))
		return clipAsset, string(payload), nil
	}

	enrichStart := time.Now()
	enrichment, analyzeErr := u.analyzeClipForCommit(ctx, cmd, clipID, startSec, endSec, bundle)
	// Sept 2026 enrichment SRE surface: the synchronous run is recorded on
	// the canonical enrichment metrics (total / duration / failures) even
	// when it fails — the failure is counted BEFORE the fail-closed abort.
	u.recordEnrichmentOutcome(analyzeErr, enrichStart)
	if analyzeErr != nil {
		u.core.Log.Warn("metadata analysis failed BEFORE clip write — clip not committed",
			zap.String("clip_id", clipID),
			zap.String("failure_code", string(FailureCodeMetadataFailed)),
			zap.Error(analyzeErr))
		return clipAsset, "", NewExtractionError(FailureCodeMetadataFailed, false,
			fmt.Sprintf("metadata analysis failed before commit: %v", analyzeErr), analyzeErr)
	}
	return foldEnrichmentIntoClipAsset(clipAsset, enrichment), "", nil
}

// commitLocalizedClip builds the text-track set and executes the single
// atomic super-tx.
//
// Returns (indexEventID, textTrackCount, handled, err):
//   - handled=true means the caller must return (bundle, nil) immediately:
//     either LocalizedWriter is not wired, or the commit succeeded but the
//     index event was suppressed by a terminal outbox row
//     (processed_but_index_blocked, BLOCKER #4 closure).
//   - err != nil is a typed ExtractionError the caller must pass to u.fail.
func (u *ProcessYouTubeSegmentUseCase) commitLocalizedClip(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	out *youtubetypes.ProcessSegmentResult,
	clipID string,
	clipAsset youtubetypes.ClipAsset,
	bundle *detail.ResolvedTextBundle,
	metadataEnrichmentJSON string,
) (string, int, bool, *ExtractionError) {
	if u.metadata.LocalizedWriter == nil {
		return "", 0, true, nil
	}

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

	commitStarted := time.Now()
	wErr := u.metadata.LocalizedWriter.CommitClipTextAndIndexEvent(ctx, superCmd)
	kernobs.RecordStage(ctx, kernobs.StageInfo{
		Stage:          kernobs.StageYouTubeCommit,
		ItemsInput:     1,
		ItemsCompleted: completedCount(wErr == nil),
	}, commitStarted, time.Now(), wErr)
	if wErr != nil {
		// BLOCKER #4 closure (audit 2026-07-03): when the outbox row insert
		// is suppressed by an existing terminal row (dead_letter or
		// superseded), the writer returns ErrOutboxTerminalConflict. The clip
		// + tracks + cues ARE safe; only the index event was blocked.
		if errors.Is(wErr, youtubeports.ErrOutboxTerminalConflict) {
			out.Item.Status = "processed_but_index_blocked"
			out.Status = "processed_but_index_blocked"
			u.core.Log.Warn("clip + tracks + cues committed but index blocked by terminal outbox row (BLOCKER #4)",
				zap.String("clip_id", clipID),
				zap.Error(wErr))
			// The clip + tracks ARE durable, so the multilingual fan-out is
			// still owed (see materialize_fanout.go).
			u.enqueueMaterializeFanOut(ctx, clipID, bundle)
			return "", len(tracks), true, nil
		}
		// godlike/07 typed-error contract: a policy violation
		// (ErrClipLocaleNotReady) gets the same fail-closed treatment as a
		// generic writer failure. The writer rolled back the tx before the
		// error surfaced.
		if localized.IsClipLocaleNotReady(wErr) {
			u.core.Log.Error("writer rejected locale-not-ready policy (Fase 2.b atomic tx rolled back; no rows visible)",
				zap.String("clip_id", clipID), zap.Error(wErr))
			return "", len(tracks), false, NewExtractionError(
				FailureCodeWriterFailed,
				false,
				fmt.Sprintf("writer rejected locale-not-ready: %v", wErr),
				wErr,
			)
		}
		u.core.Log.Error("localized writer terminal failure (Fase 2.b atomic tx rolled back; no rows visible)",
			zap.String("clip_id", clipID), zap.Error(wErr))
		return "", len(tracks), false, NewExtractionError(FailureCodeWriterFailed, false,
			fmt.Sprintf("localized writer failed: %v", wErr), wErr)
	}

	u.core.Log.Info("localized clip super-tx committed",
		zap.String("clip_id", clipID),
		zap.Int("text_tracks", len(tracks)),
		zap.Int("timed_tracks", len(timedTracks)))
	return event.AggregateID, len(tracks), false, nil
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

// analyzeClipForCommit runs the PURE metadata analyzer against the resolved
// transcript bundle. It returns the CanonicalClipEnrichment that the caller
// folds into the ClipAsset BEFORE the canonical atomic commit — the analyzer
// never writes media_assets.
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
// segment metadata; empty enrichment fields leave the existing value
// untouched. The canonical search text is recomputed from the FOLDED metadata
// so the search surface stays consistent with the committed semantic fields.
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

// transcriptSidecarFailure wraps a fatal sidecar write error.
func (u *ProcessYouTubeSegmentUseCase) transcriptSidecarFailure(out *youtubetypes.ProcessSegmentResult, txtPath string, err error) *ExtractionError {
	return u.fail(out, NewExtractionError(
		FailureCodeWriterFailed, true,
		fmt.Sprintf("write transcript sidecar %s: %v", txtPath, err), err))
}

// subtitleDestinationMissing fails closed when a subtitle destination was
// requested but the Drive folder manager is not wired.
func (u *ProcessYouTubeSegmentUseCase) subtitleDestinationMissing(out *youtubetypes.ProcessSegmentResult) *ExtractionError {
	return u.fail(out, NewExtractionError(
		FailureCodeDriveUploadFailed, false,
		"subtitle destination requested but Drive folder manager is not wired", nil))
}

// uploadFailure wraps a subtitle-sidecar upload error.
func (u *ProcessYouTubeSegmentUseCase) uploadFailure(out *youtubetypes.ProcessSegmentResult, label string, retryable bool, err error) *ExtractionError {
	return u.fail(out, NewExtractionError(
		FailureCodeDriveUploadFailed, retryable,
		fmt.Sprintf("%s: %v", label, err), err))
}

// videoUploadFailure logs the dropped bundle + retry classification and wraps
// the video upload error.
func (u *ProcessYouTubeSegmentUseCase) videoUploadFailure(out *youtubetypes.ProcessSegmentResult, clipID string, bundle *detail.ResolvedTextBundle, err error) *ExtractionError {
	u.logDroppedBundle(clipID, bundle, err)
	u.logVideoUploadFailure(clipID, err)
	return u.fail(out, NewExtractionError(
		FailureCodeDriveUploadFailed,
		IsTransientExtractionError(err),
		fmt.Sprintf("drive upload failed: %v", err),
		err,
	))
}

// applyVideoUploadResult publishes the uploaded video identity on the result.
func (u *ProcessYouTubeSegmentUseCase) applyVideoUploadResult(out *youtubetypes.ProcessSegmentResult, res *youtubeports.UploadResultDTO) {
	if res != nil {
		out.Item.DriveFileID = res.FileID
		out.Item.DriveLink = res.WebViewLink
	}
	out.DriveFileID = out.Item.DriveFileID
	out.DriveLink = out.Item.DriveLink
}

// finalizeLocalizedClip records the commit outcome, flags a committed clip
// with no text track, and schedules the multilingual materialization.
func (u *ProcessYouTubeSegmentUseCase) finalizeLocalizedClip(ctx context.Context, out *youtubetypes.ProcessSegmentResult, bundle *detail.ResolvedTextBundle, clipID, eventID string, trackCount int) {
	out.IndexedRequestID = eventID
	// STATE SEMANTICS (Sept 2026): a clip committed with ZERO text tracks
	// is NOT a completed subtitle pipeline. The clip itself is durable, so
	// this is a distinct, explicit outcome rather than a failure.
	if trackCount == 0 {
		out.Item.Status = "processed_but_text_missing"
		out.Status = "processed_but_text_missing"
		u.core.Log.Warn("clip committed with NO text track: transcript acquisition exhausted; translations and subtitle artifacts are NOT produced",
			zap.String("clip_id", clipID))
	}

	// POSTGRES-MEDIA-CUTOVER follow-up (September 2026): schedule the
	// canonical multilingual materialization for the clip we just
	// committed.
	u.enqueueMaterializeFanOut(ctx, clipID, bundle)
}

// Package usecase — process_segment_step10.go: canonical owner of
// Step 10 (metadata enrichment).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c (July 2026): the typed
// Transcriber port was RETIRED. The transcript is now acquired
// EXCLUSIVELY by TextTrackResolver (Step 6-9) and threaded into
// Step 10 as a parameter. Step 10 produces only metadata
// enrichments (title, summary, quality score) using the already-
// resolved transcript — no Whisper invocation, no filesystem
// reads, no port coupling.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.c (July 2026): audit +
// hardening of the Fase 1.c structural removal. The
// hermetic test coverage lives in
// process_segment_fase2c_test.go: three probes pinning
// (a) the data-flow thread `bundle.PlainText -> step10Transcript
// -> EnrichClip.ClipMetadataInput.Transcript` byte-for-byte,
// (b) the empty-bundle fail-closed path (Step 6-9 returned
// nil -> Step 10 threads "" verbatim, NEVER a Whisper
// fallback), (c) 0 Transcriber calls on direct Step 10
// even with TextTrackResolver + MetadataService wired.
// Together with Fase 1.c counter assertions
// (process_segment_fase1c_test.go): hermetic coverage at the
// static (no Transcriber field on ProcessSegmentDeps),
// counter, and data-flow layers.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - MetadataService.EnrichClip lives ONLY here
//   - Step10Metrics.IncStep10FailAfterClip counter (Prometheus
//     `transcript_metadata_step10_fail_after_clip_total{failure_code}`)
//     lives ONLY here
//
// godlike/07 no-fake-availability: nil MetadataService resolves
// to a no-op (EnrichClip not called). The canonical job-outcome
// path is the typed *ExtractionError on EnrichClip failure; the
// Warn log + counter increment are PURELY observability (the
// typed error is still returned to the orchestrator).
//
// godlike/07 fail-closed on empty transcript: the contract is
// consistent with the pre-Fase-1.c behavior (Transcriber nil/err
// also produced empty transcript). The metadata service
// downstream behavior is unchanged (empty transcript → low
// quality score, no crash).
package usecase

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// step10_MetadataEnrich is the canonical owner of Step 10. The
// transcript is sourced from the orchestrator (which received it
// from step6to9 via the ResolvedTextBundle). Step 10 only
// performs metadata enrichment — no transcript acquisition.
//
// Parameters:
//   - transcript:  the PlainText of the resolved bundle (may be
//     "" if all 5 chain priorities failed; the metadata service
//     handles empty transcripts gracefully).
//   - languageCode: the BCP-47 of the resolved bundle (may be
//     "" if the bundle is nil).
//   - cues:        the per-segment timed cues (may be nil if
//     the source was payload-text or Whisper rather than VTT).
//
// Returns nil when MetadataService is nil (no-op) or when
// EnrichClip succeeds. Returns the typed *ExtractionError
// (FailureCodeMetadataFailed) on EnrichClip failure. On failure,
// the Warn log + counter increment are emitted FIRST so
// observability catches the partial-state class (Step 9 already
// wrote media_assets + outbox event before we reach here; the
// clip IS persisted; only the metadata enrichment needs manual
// re-extract).
func (u *ProcessYouTubeSegmentUseCase) step10_MetadataEnrich(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	clipID string,
	duration int,
	transcript string,
	languageCode string,
	cues []detail.TimedCue,
) error {
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.c: the legacy Whisper
	// invocation at this seam is RETIRED. The transcript is
	// already resolved by step6to9 (TextTrackResolver priority
	// chain 1-5) and threaded in via the parameter list. When
	// the chain failed to acquire any text, the orchestrator
	// passes transcript=""; the metadata service's downstream
	// behavior is unchanged (low quality score, no crash).
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 2.c (audit): the
	// transcript parameter is the SOLE source of ClipMetadataInput
	// fields below — the use case never reaches back into the
	// resolver nor the Transcriber port. Tests in
	// process_segment_fase2c_test.go prove this both at the
	// counter layer (0 Whisper invocations on direct Step 10)
	// and at the data-flow layer (builder.last.Transcript
	// must equal the parameter byte-for-byte).
	_ = cues // cues are exposed for future per-segment
	//          metadata enrichment hooks (e.g. quality-score
	//          variance across cues). For Fase 1.c they are
	//          accepted but unused.

	if u.metadata.MetadataService != nil {
		startSec, _ := textutil.ParseTimestamp(cmd.Segment.Start)
		endSec, _ := textutil.ParseTimestamp(cmd.Segment.End)
		// PR-ASSET-COMMITTER-ENRICHMENT (August 2026): this method now
		// performs the PURE analysis (AnalyzeClip) instead of the legacy
		// write (EnrichClip). The enrichment is folded into the canonical
		// commit by step6to9 BEFORE the atomic super-tx; this method is
		// retained only as the data-flow regression seam (transcript →
		// ClipMetadataInput.Transcript, no Whisper re-invocation). It no
		// longer persists media_assets nor emits a second index event.
		_, metaErr := u.metadata.MetadataService.AnalyzeClip(ctx, youtubetypes.ClipMetadataInput{
			ClipID:           clipID,
			Title:            cmd.Segment.Name,
			Description:      segmentDescription(cmd.Segment.Texts),
			Summary:          cmd.Segment.Summary,
			Tags:             append([]string(nil), cmd.Segment.Tags...),
			Transcript:       transcript,
			ClipDuration:     duration,
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
		})
		if metaErr != nil {
			// PR-PY-STEP10-FAIL-LOG (code-reviewer S1, July 2026):
			// Step 9 already wrote media_assets + emitted the
			// asset.index.requested outbox event before we reach
			// here, so the clip IS persisted. Only the metadata
			// enrichment needs manual re-extract. Emit a Warn log
			// BEFORE u.fail so operator dashboards see the
			// partial-state class WITHOUT weakening the typed-error
			// contract (godlike/07 NO-FAKE-AVAILABILITY: the
			// canonical job outcome is the typed error; the Warn
			// log is purely observability).
			u.core.Log.Warn("Step 10 failed AFTER clip write \u2013 manual re-extract needed",
				zap.String("clip_id", clipID),
				zap.String("language_code", languageCode),
				zap.String("failure_code", string(FailureCodeMetadataFailed)),
				zap.Error(metaErr))
			// PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY (July 2026):
			// Increment the transcript_metadata_step10_fail_after_clip_total
			// counter so dashboards can aggregate partial-state
			// events across a batch extraction by failure_code.
			if u.observability.Step10Metrics != nil {
				u.observability.Step10Metrics.IncStep10FailAfterClip(string(FailureCodeMetadataFailed))
			}
			typed := NewExtractionError(FailureCodeMetadataFailed, false,
				fmt.Sprintf("metadata enrichment failed: %v", metaErr), metaErr)
			return typed
		}
	}
	return nil
}

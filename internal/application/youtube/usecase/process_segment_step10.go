// Package usecase — process_segment_step10.go: canonical owner of
// Step 10 (metadata enrichment + typed Transcriber port + Metrics port).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - Typed Transcriber port invocation (`Transcriber.TranscribeAudio`)
//     lives ONLY here. Registered unconditionally when wired (NOT
//     gated on MetadataService) so the contract is testable in
//     isolation AND symmetric with Step 7's Whisper fallback.
//   - MetadataService.EnrichClip lives ONLY here
//   - Step10Metrics.IncStep10FailAfterClip counter (Prometheus
//     `transcript_metadata_step10_fail_after_clip_total{failure_code}`)
//     lives ONLY here
//
// godlike/07 no-fake-availability: nil Transcriber OR nil MetadataService
// each resolve to a no-op (transcript="" / EnrichClip not called). The
// canonical job-outcome path is the typed *ExtractionError on
// EnrichClip failure; the Warn log + counter increment are PURELY
// observability (the typed error is still returned to the orchestrator).
package usecase

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
)

// step10_MetadataEnrich is the canonical owner of Step 10. Returns nil
// when MetadataService is nil (no-op) or when EnrichClip succeeds.
// Returns the typed *ExtractionError (FailureCodeMetadataFailed) on
// EnrichClip failure. On failure, the Warn log + counter increment
// are emitted FIRST so observability catches the partial-state class
// (Step 9 already wrote media_assets + outbox event before we reach
// here; the clip IS persisted; only the metadata enrichment needs
// manual re-extract).
//
// godlike/07 no-fake-availability: the Transcriber.TranscribeAudio call
// is gated only on `localPath != "" && u.deps.Transcriber != nil` —
// nil OR port-error both resolve to transcript="" which the metadata
// service already handles (empty transcript → low quality score, no
// crash). The Warn log surfaces the Whisper-failure case for operator
// forensics.
//
// godlike/06 SSOT: transcript="" when Transcriber nil/errored is
// CONSISTENT with Step 7's failure path; the metadata service's
// downstream behavior is unchanged.
func (u *ProcessYouTubeSegmentUseCase) step10_MetadataEnrich(
	ctx context.Context,
	cmd youtubetypes.ProcessSegmentCommand,
	clipID string,
	localPath string,
	duration int,
) error {
	// STEP-10-TYPED-PORT-FIX (PR-PY-STEP10-PORT-M3, July 2026):
	// the legacy os.ReadFile(localPath+".txt") filesystem-coupled
	// read is REPLACED with u.deps.Transcriber.TranscribeAudio(port) —
	// the canonical WhisperTranscriberPort already wired to the use
	// case (Step 7's Whisper fallback uses the same port). result:
	//
	//   - step-7 race retired (the .txt tempfile was written only when
	//     SliceSubtitles failed AND Whisper succeeded; under the typical
	//     SliceSubtitles-success path no .txt was ever written so the
	//     transcript was silently empty).
	//   - transcript content reaches metadata enrichment on the happy
	//     path (SliceSubtitles-success + no Whisper=None + non-empty
	//     TranscribeAudio result).
	//   - nil port → fail-open: empty transcript with no panic.
	//   - port error → graceful swallow (Warn-level log + empty
	//     transcript + Execute continues). The Execute still surfaces
	//     to the pipeline as `processed` so the media_assets row +
	//     outbox event from Step 9 land ungated.
	transcript := ""
	if localPath != "" && u.deps.Transcriber != nil {
		if text, tErr := u.deps.Transcriber.TranscribeAudio(ctx, localPath); tErr == nil {
			transcript = strings.TrimSpace(text)
		} else {
			u.deps.Log.Warn("step 10 transcriber port failed; transcript will be empty",
				zap.String("clip_id", clipID),
				zap.String("local_path", localPath),
				zap.Error(tErr))
		}
	}

	// STEP-10-METADATA-ENRICHMENT (canonical; unchanged shape): the
	// metadata service persists enriched clip metadata into SQLite +
	// emits its own re-index outbox event. Receives `transcript` from
	// the typed port above (or "" if Transcriber is nil/errored).
	if u.deps.MetadataService != nil {
		_, metaErr := u.deps.MetadataService.EnrichClip(ctx, youtubetypes.ClipMetadataInput{
			ClipID:           clipID,
			Title:            cmd.Segment.Name,
			Transcript:       transcript,
			ClipDuration:     duration,
			SourceURL:        cmd.VideoURL,
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
			u.deps.Log.Warn("Step 10 failed AFTER clip write – manual re-extract needed",
				zap.String("clip_id", clipID),
				zap.String("local_path", localPath),
				zap.String("failure_code", string(FailureCodeMetadataFailed)),
				zap.Error(metaErr))
			// PR-PY-STEP10-FAIL-LOG-OBSEVE-PARITY (July 2026):
			// Increment the transcript_metadata_step10_fail_after_clip_total
			// counter so dashboards can aggregate partial-state
			// events across a batch extraction by failure_code.
			// The counter is the canonical dashboard-aggregate
			// surface; the Warn log above is preserved for
			// granular forensics. Nil-tolerance: the call is
			// silently skipped when the port is not wired
			// (composition root may omit it).
			if u.deps.Step10Metrics != nil {
				u.deps.Step10Metrics.IncStep10FailAfterClip(string(FailureCodeMetadataFailed))
			}
			typed := NewExtractionError(FailureCodeMetadataFailed, false,
				fmt.Sprintf("metadata enrichment failed: %v", metaErr), metaErr)
			return typed
		}
	}
	return nil
}

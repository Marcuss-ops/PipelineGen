// Package texttracks — fanout_commit.go: the canonical post-COMMIT mapping
// between a clip that was just durably committed and the multilingual
// materialization job it must schedule.
//
// WHY THIS FILE EXISTS (September 2026, register-path gap).
//
// The mapping used to live in the ONLY producer that had it
// (youtube/usecase/materialize_fanout.go), so the post-commit fan-out was
// reachable from exactly one commit route: the YouTube per-segment
// extraction pipeline. The Register route
// (assets/sourcing/youtube → commitClipAtomically) commits through the SAME
// PostgreSQL media committer but never scheduled the job, so every clip
// registered through POST /api/media/register-batch landed with exactly ONE
// text track (whatever Whisper produced): no translations, no `.ass`
// artifacts, no multilingual search — a silently under-materialized asset
// with no error anywhere.
//
// godlike/06 SSOT: this file is the SOLE owner of the
// "committed clip → materialize vs. acquire" decision. Producers call
// EnqueueCommittedClip and MUST NOT re-implement the branch, the `und`
// language fallback or the source-text hash computation — the same rules
// must hold for every commit route, present and future.
package texttracks

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// CommittedClipEnqueuer is the narrow post-commit seam every commit route
// consumes. The production concrete is *MaterializeFanOut (satisfied
// structurally); consumers declare their own single-method port so no
// capability has to import this package's types just to hold the seam.
//
// It is intentionally void-returning: the clip is already durably committed,
// so a broker hiccup must never surface as a failed registration — every
// failure is logged and recoverable via the operator backfill
// (`admin text-tracks-backfill --all --apply`).
type CommittedClipEnqueuer interface {
	// EnqueueCommittedClip schedules the canonical multilingual
	// materialization for clipID using the transcript that was just
	// committed (sourceLanguage + plainText). An empty plainText means the
	// clip was committed WITHOUT a transcript: the canonical acquisition
	// chain (payload → DB → YouTube manual → YouTube auto → Whisper) is
	// scheduled instead, before translation.
	EnqueueCommittedClip(ctx context.Context, clipID, sourceLanguage, plainText string)
}

// EnqueueCommittedClip schedules multilingual materialization for a clip
// whose atomic commit just succeeded.
//
// Contract:
//   - committed transcript → EnqueueMaterializeOne with the SAME hash the
//     persistence layer wrote onto the READY track
//     (detail.TextHash(plainText, lang, kind)); the materializer re-reads
//     that row and fails closed on any mismatch, so recomputing the hash
//     here with the same canonical function is the contract, not a
//     duplication.
//   - unknown language → "und", mirroring bundleToTextTracks/track
//     persistence, or the hash would not match the persisted row.
//   - clip committed WITHOUT a transcript → EnqueueAcquireOne, so the
//     canonical acquisition chain still runs before translation.
//
// Failures are logged, never propagated: the clip is already durable and
// turning a broker hiccup into an extraction/registration failure would
// report a successful commit as failed. The fan-out is recoverable via the
// backfill CLI.
func (f *MaterializeFanOut) EnqueueCommittedClip(ctx context.Context, clipID, sourceLanguage, plainText string) {
	if f == nil || clipID == "" {
		return
	}
	kinds := []detail.TextTrackKind{detail.TextTrackTranscript}

	if plainText == "" {
		sourceLanguage = f.DefaultSourceLanguage()
		if sourceLanguage == "" {
			f.log.Warn("texttracks.materialize fan-out skipped: no source language resolvable and no default configured",
				zap.String("clip_id", clipID))
			return
		}
		if err := f.EnqueueAcquireOne(ctx, clipID, sourceLanguage, kinds); err != nil {
			f.log.Warn("texttracks.materialize acquire fan-out failed (clip is committed; recover via backfill)",
				zap.String("clip_id", clipID),
				zap.String("source_language", sourceLanguage),
				zap.Error(err))
		}
		return
	}

	if sourceLanguage == "" {
		// Mirrors the track persistence: an unknown language is persisted as
		// "und", so the hash must be computed on the same value or the
		// materializer's source-track hash check would reject the job.
		sourceLanguage = "und"
	}
	sourceTextHash := string(detail.TextHash(plainText, sourceLanguage, detail.TextTrackTranscript))

	if err := f.EnqueueMaterializeOne(ctx, clipID, sourceLanguage, sourceTextHash, kinds); err != nil {
		f.log.Warn("texttracks.materialize fan-out failed (clip is committed; recover via backfill)",
			zap.String("clip_id", clipID),
			zap.String("source_language", sourceLanguage),
			zap.Error(err))
		return
	}
	f.log.Info("texttracks.materialize scheduled after YouTube clip commit",
		zap.String("clip_id", clipID),
		zap.String("source_language", sourceLanguage))
}

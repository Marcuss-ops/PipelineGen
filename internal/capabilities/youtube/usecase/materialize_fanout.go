// Package usecase — materialize_fanout.go: the post-commit multilingual
// fan-out seam for the direct YouTube extraction path.
//
// POSTGRES-MEDIA-CUTOVER follow-up (September 2026).
//
// The canonical pipeline today is:
//
//	YouTube URL → cut → transcript (5-priority chain) → Drive →
//	CommitClipTextAndIndexEvent (clip + tracks + cues + PG outbox)
//	  → asset.index.requested → PostgresIndexWorker → media_embeddings
//
// Everything above is correct, but it stops there: the clip ends up with
// exactly the languages the acquisition chain happened to produce (usually
// just the original), while the Artlist / Stock / generic paths get their
// other languages from the post-publish fan-out their finalizers call.
// The YouTube per-segment pipeline commits through
// localized.LocalizedClipWriter directly and never reached
// MaterializeFanOut, so "download a new YouTube clip" did NOT automatically
// produce the configured translation set.
//
// This file is that missing edge. It runs strictly AFTER the atomic commit
// succeeds, so it can never schedule translation work for a clip that was
// not persisted.
//
// godlike/06 SSOT: the enqueue decision (materialize vs. acquire) and the
// job payload construction stay in texttracks.MaterializeFanOut; this file
// only maps the committed bundle onto that helper's contract.
package usecase

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// enqueueMaterializeFanOut schedules multilingual materialization for a clip
// whose atomic commit just succeeded.
//
// Contract:
//   - nil fan-out port (test fixtures, minimal compositions, media-disabled
//     deployments) → silent no-op, exactly the pre-change behaviour.
//   - a committed source transcript → EnqueueMaterializeOne with the SAME
//     hash the persistence layer wrote onto the READY track
//     (detail.TextHash(plainText, lang, kind)); the materializer re-reads
//     that row and fails closed on any mismatch, so recomputing the hash
//     here with the same canonical function is the contract, not a
//     duplication.
//   - a clip committed WITHOUT a transcript → EnqueueAcquireOne, so the
//     canonical acquisition chain (payload → DB → YouTube manual → YouTube
//     auto → Whisper) still runs before translation.
//
// Scheduling failures are logged, never propagated: the clip is already
// durably committed, and turning a broker hiccup into an extraction failure
// would report a successful commit as failed. The fan-out is recoverable via
// the backfill CLI.
func (u *ProcessYouTubeSegmentUseCase) enqueueMaterializeFanOut(
	ctx context.Context,
	clipID string,
	bundle *detail.ResolvedTextBundle,
) {
	if u == nil || u.media.MaterializeFanOut == nil {
		return
	}
	if clipID == "" {
		return
	}
	fanout := u.media.MaterializeFanOut

	kinds := []detail.TextTrackKind{detail.TextTrackTranscript}

	if bundle == nil || bundle.IsEmpty() || bundle.PlainText == "" {
		sourceLanguage := fanout.DefaultSourceLanguage()
		if sourceLanguage == "" {
			u.core.Log.Warn("texttracks.materialize fan-out skipped: no source language resolvable and no default configured",
				zap.String("clip_id", clipID))
			return
		}
		if err := fanout.EnqueueAcquireOne(ctx, clipID, sourceLanguage, kinds); err != nil {
			u.core.Log.Warn("texttracks.materialize acquire fan-out failed (clip is committed; recover via backfill)",
				zap.String("clip_id", clipID),
				zap.String("source_language", sourceLanguage),
				zap.Error(err))
		}
		return
	}

	sourceLanguage := bundle.LanguageCode
	if sourceLanguage == "" {
		// Mirrors bundleToTextTracks: an unknown language is persisted as
		// "und", so the hash must be computed on the same value or the
		// materializer's source-track hash check would reject the job.
		sourceLanguage = "und"
	}
	sourceTextHash := string(detail.TextHash(bundle.PlainText, sourceLanguage, detail.TextTrackTranscript))

	if err := fanout.EnqueueMaterializeOne(ctx, clipID, sourceLanguage, sourceTextHash, kinds); err != nil {
		u.core.Log.Warn("texttracks.materialize fan-out failed (clip is committed; recover via backfill)",
			zap.String("clip_id", clipID),
			zap.String("source_language", sourceLanguage),
			zap.Error(err))
		return
	}
	u.core.Log.Info("texttracks.materialize scheduled after YouTube clip commit",
		zap.String("clip_id", clipID),
		zap.String("source_language", sourceLanguage))
}

// WithMaterializeFanOut late-binds the canonical post-commit fan-out onto
// the per-segment pipeline.
//
// Late binding is required because the fan-out helper needs the jobs broker,
// which the composition root assembles AFTER the domain bundle (and therefore
// after this use case) exists. wireLateBindings calls this once at boot, before
// any request is served — the same late-binding pattern the Artlist/Stock
// finalizers already use (AssetTxFinalizer.WithFanOut).
//
// nil-safe: a nil port, a nil service, or a service built without the
// per-segment pipeline is an observable no-op rather than a panic.
func (s *Service) WithMaterializeFanOut(port MaterializeFanOutPort) {
	if s == nil {
		return
	}
	if s.processSeg != nil {
		s.processSeg.media.MaterializeFanOut = port
	}
	// Defensive: the extraction orchestrator may hold its own reference in a
	// non-production composition. In production both point at the SAME use
	// case, so this is a no-op there.
	if s.extraction != nil && s.extraction.processSeg != nil && s.extraction.processSeg != s.processSeg {
		s.extraction.processSeg.media.MaterializeFanOut = port
	}
}

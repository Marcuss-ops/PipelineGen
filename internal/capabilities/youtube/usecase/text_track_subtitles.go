// Package usecase — text_track_subtitles.go is the subtitle-side
// leaf of the text-track 6-file split. It owns ONLY the canonical
// converter between the orchestrator's ResolvedTextBundle result
// (priority 3+4 winner from AcquireSegmentText) and the writer's
// per-cue timed shape (localized.TimedTextTrack).
//
// AGENTS.md / godlike/06 SSOT split (July 2026): the orchestrator
// (text_track_resolver.go) is the SOLE canonical site for:
//   - asset.Normalize() calls (BCP-47 normalisation)
//   - ResolvedTextBundle provenance assembly (SourceType, IsOriginal,
//     Provider, ModelName, ModelVersion, Confidence)
//   - the priority-3 + priority-4 vs Whisper-fall-through decision
//
// The subtitle fetcher port (SubtitleFetcherPort.FetchSegmentSubtitles)
// itself returns an already-assembled *asset.ResolvedTextBundle so the
// orchestrator can accept it directly — this leaf is reserved for the
// downstream conversion into the writer's per-cue shape, not the port
// call itself.
//
// Method receiver: this leaf is a (*TextTrackResolver) method
// (NOT a free function) so the resolver's *zap.Logger is plumbed
// end-to-end and the dropped-cue diagnostic emits via r.Log.Warn.
// Free-function callers would lose the diagnostic silently
// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5.c logger plumb).
//
// Pre-conditions (audit 2026-07-11 §2.c):
//   - Each Cue has StartMs >= 0, EndMs >= StartMs, Text != "".
//     Malformed upstream cues are SILENTLY DROPPED here (NOT a hard
//     fail); the leaf emits a Warn with the dropped count so
//     operators can trace which clips are losing cues.
//   - The bundle's LanguageCode is already BCP-47 normalised ("und"
//     when empty — collapses to "und" mirror of bundleToTextTracks).
package usecase

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// bundleToTimedTrack converts a non-empty bundle's Cues into a single
// localized.TimedTextTrack row (text_kind=transcript). Returns nil
// for nil receiver, nil bundle, or empty Cues. SequenceNo assignment
// is the writer's responsibility (the writer sorts ascending by
// StartMs before assigning sequence_no based on the array index).
//
// godlike/06 SSOT: this is the canonical converter between the
// resolver's plain-text bundle and the writer's per-cue timed
// shape. The LocalizationWriter consumes it directly — handlers MUST
// NOT re-shape the data inline.
//
// Scope (Fase 2.b):
//   - Today, ONLY the resolved BUNDLE (priority 1-5 winner from
//     AcquireSegmentText) carries Cues — payload-provided
//     LocalizedClipText rows in Segment.Texts yield TextTrack
//     rows only (no cues). The cue-row surface is bundle-only.
//   - Fase 5 (payload-level cues): if Segment.Texts grows a
//     Cues field, this helper grows a parallel MaterializePayload
//     TimedTracks API. Until then the publisher contract is
//     bundle-only.
func (r *TextTrackResolver) bundleToTimedTrack(clipID string, bundle *asset.ResolvedTextBundle) *localized.TimedTextTrack {
	if r == nil {
		return nil
	}
	if bundle == nil {
		return nil
	}
	if len(bundle.Cues) == 0 {
		return nil
	}
	lang := bundle.LanguageCode
	if lang == "" {
		lang = "und"
	}
	cues := make([]asset.TimedCue, 0, len(bundle.Cues))
	dropped := 0
	for _, c := range bundle.Cues {
		if c.StartMs < 0 || c.EndMs < c.StartMs || c.Text == "" {
			// godlike/07 honest lock: a malformed upstream cue
			// MUST NOT poison the whole super-tx; the leaf drops
			// it loudly and surfaces the count via r.Log.Warn.
			dropped++
			continue
		}
		cues = append(cues, c)
	}
	if dropped > 0 && r.Log != nil {
		r.Log.Warn("bundleToTimedTrack: dropped malformed cues",
			zap.String("clip_id", clipID),
			zap.Int("dropped", dropped))
	}
	if len(cues) == 0 {
		return nil
	}
	return &localized.TimedTextTrack{
		LanguageCode: lang,
		TextKind:     asset.TextTrackTranscript,
		SourceType:   bundle.SourceType,
		Cues:         cues,
	}
}

// Package usecase — text_track_resolver_persist.go is the SLIM PERSISTENCE
// HALF of the text_track_resolver split. It owns the 3 methods that WRITE
// ResolvedTextBundle / TextTrack rows to asset_text_tracks:
//  1. Save             — single transcript-row writer (text_kind=transcript)
//  2. SaveMany         — pre-built batch writer
//  3. MaterializePayloadTexts — payload->rows adapter (transcript+title+
//     summary+description per input, max 4 rows per input)
//
// Split rationale (Commit D, July 2026): the 6-file orchestrator
// text_track_resolver.go composes the 5-level acquisition chain
// AND the BCP-47 normalization SSOT; this file isolates the
// pre-Step-9 persistence concern so the orchestrator stays a pure
// resolver (Composable, no Repo coupling on the read path).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The TextHash + SourceVersion SHA-256 formula lives in
//     internal/kernel/asset/text_track_hashes.go. This file CALLS
//     detail.TextHash / detail.SourceVersion and NEVER re-implements
//     the formula inline.
//   - The canonical BCP-47 normalization rules live in
//     internal/kernel/asset/bcp47.go. This file CALLS asset.Normalize
//     and NEVER re-derives the rules inline.
//   - The ResolvedTextBundle -> TextTrack row factory lives in
//     text_track_persistence.go (bundleToTextTracks helper). This
//     file CALLS the canonical factory and NEVER builds TextTrack
//     structs inline (the pre-Save inline construction was removed).
//
// Pre-Step-9 persistence contract:
//   - Save/SaveMany MUST be invoked AFTER Step 9 completes
//     (ClipAtomicWriter.CommitClipAndIndexEvent). Saving a track
//     whose clip row doesn't yet exist surfaces as a typed SQLite
//     FK violation on asset_text_tracks.asset_id and silently fails
//     the batch.
//   - godlike/07 no-fake-availability: persistence errors propagate
//     VERBATIM (no `_ = ...`, no swallowed errors). Callers in
//     process_segment_step6to9.go mirror the BLOCKER #4
//     partial-state pattern (log + status mutation, do NOT fail the
//     pipeline) so the clip is always safely persisted.
//
// godlike/07 honest locks in this file:
//   - Save: empty source is a TYPED error (no silent classify-as-provided).
//   - Save: empty languageCode collapses to "und" via asset.Normalize
//     (NEVER "en").
//   - MaterializePayloadTexts: malformed language code SKIPS the
//     entry (log+continue, never poison the batch).
package usecase

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"fmt"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// Save persists a single transcript row (text_kind=transcript) to
// asset_text_tracks. To materialise all payload-provided texts
// (transcript + title + summary + description per language) use
// MaterializePayloadTexts + SaveMany.
//
// godlike/07 honest lock:
//   - empty languageCode defaults to BCP-47 "und" (NEVER "en");
//   - empty Source is a TYPED error (no silent classify-as-provided).
//
// Empty transcript or nil Repo -> no-op (idempotent).
func (r *TextTrackResolver) Save(
	ctx context.Context,
	clipID string,
	transcript string,
	source detail.TextTrackSource,
	languageCode string,
) error {
	if r == nil || r.Repo == nil || transcript == "" {
		return nil
	}
	if source == "" {
		return fmt.Errorf("text_track_resolver.Save: source is required (got empty); refusing to write provenance-less asset_text_tracks row")
	}
	lang, err := asset.Normalize(languageCode)
	if err != nil {
		return fmt.Errorf("text_track_resolver.Save: invalid language %q: %w", languageCode, err)
	}
	bundle := &detail.ResolvedTextBundle{
		LanguageCode:       lang,
		SourceLanguageCode: lang,
		PlainText:          transcript,
		SourceType:         source,
		IsOriginal:         source == detail.TextSourceYouTubeSubtitle || source == detail.TextSourceProvided || source == detail.TextSourceWhisper,
		Provider:           "",
	}
	tracks := bundleToTextTracks(clipID, bundle, detail.TextTrackTranscript)
	if len(tracks) == 0 {
		return nil
	}
	if r.Log != nil {
		r.Log.Info("text track saved",
			zap.String("clip_id", clipID),
			zap.String("source", string(source)),
			zap.String("language", lang))
	}
	return r.Repo.UpsertBatch(ctx, tracks)
}

// SaveMany persists a pre-built batch of TextTrack rows. Idempotent
// and nil-safe: nil Receiver or empty slice is a no-op. Errors are
// propagated verbatim (caller in step6to9 mirrors the BLOCKER #4
// partial-state pattern on failure).
func (r *TextTrackResolver) SaveMany(ctx context.Context, tracks []detail.TextTrack) error {
	if r == nil || r.Repo == nil || len(tracks) == 0 {
		return nil
	}
	if r.Log != nil {
		r.Log.Info("text track batch saved",
			zap.Int("count", len(tracks)))
	}
	return r.Repo.UpsertBatch(ctx, tracks)
}

// MaterializePayloadTexts converts the payload's []LocalizedClipText
// into TextTrack rows for every non-empty field per input
// (transcript/title/summary/description, max 4 rows per input).
// Each row's TextHash + SourceVersion are computed via the canonical
// factory.
//
// Pre-step-9 pre-condition: this method does NOT verify the clip row
// exists. Callers MUST have committed the clip row
// (ClipAtomicWriter.CommitClipAndIndexEvent) before invoking SaveMany
// with the rows returned here — otherwise the FK constraint on
// asset_text_tracks.asset_id will reject the upsert and the SQLite
// transaction rolls back. The atomic same-tx write is scope of
// Fase 2.b; Fase 1.a uses post-step-9 persistence as an interim
// safe pattern (no FK violation; clip + outbox are precommitted).
//
// Fase 1.b: language codes are normalized via asset.Normalize here
// (the orchestrator) before the row is built — godlike/07 honest
// lock: empty input collapses to "und" without error.
func (r *TextTrackResolver) MaterializePayloadTexts(clipID string, texts []youtubetypes.LocalizedClipText) []detail.TextTrack {
	var out []detail.TextTrack
	for _, t := range texts {
		rawLang := t.LanguageCode
		lang, err := asset.Normalize(rawLang)
		if err != nil {
			// Skip the entire entry on a malformed language
			// code (godlike/07 honest lock: a typo in the
			// payload MUST NOT poison the batch).
			if r.Log != nil {
				r.Log.Warn("MaterializePayloadTexts: skipping entry with invalid language",
					zap.String("clip_id", clipID),
					zap.String("raw", rawLang),
					zap.Error(err))
			}
			continue
		}
		srcType := sourceOrProvided(t.SourceType)
		isOriginal := t.IsOriginal ||
			srcType == detail.TextSourceProvided ||
			srcType == detail.TextSourceYouTubeSubtitle ||
			srcType == detail.TextSourceWhisper

		// godlike/06 SSOT: the SourceLanguageCode derivation lives
		// ONLY in sourceLangOf (text_track_persistence.go). The
		// orchestrator does NOT re-derive the rule inline (this
		// method previously had a parallel-inline version of the
		// same rule; the inline version was removed in the 6-file
		// split per the user-spec discipline).
		srcLang := sourceLangOf(t, lang, isOriginal)

		push := func(kind detail.TextTrackKind, text string) {
			if text == "" {
				return
			}
			hash := detail.TextHash(text, lang, kind)
			out = append(out, detail.TextTrack{
				AssetID:            clipID,
				LanguageCode:       lang,
				TextKind:           kind,
				TextContent:        text,
				SourceType:         srcType,
				SourceLanguageCode: srcLang,
				IsOriginal:         isOriginal,
				Provider:           "",
				ModelName:          t.ModelName,
				ModelVersion:       t.ModelVersion,
				TextHash:           hash,
				SourceVersion:      detail.SourceVersion(hash, srcLang, lang, "", t.ModelName, t.ModelVersion, ""),
				Confidence:         confidencePtr(t.Confidence),
				Status:             detail.TextTrackReady,
			})
		}
		push(detail.TextTrackTranscript, t.Transcript)
		push(detail.TextTrackTitle, t.Title)
		push(detail.TextTrackSummary, t.Summary)
		push(detail.TextTrackDescription, t.Description)
	}
	return out
}

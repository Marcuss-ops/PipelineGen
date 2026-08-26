package detail

// text_track_repository.go defines the canonical port for persisting
// and querying TextTrack records. The concrete SQLite implementation
// lives in internal/platform/sqlite/assets/.
//
// This port is consumed by:
//   - The YouTube writer path (atomic save of media_assets + text tracks + outbox)
//   - The TextTrackResolver (lookup-before-Whisper fast path)
//   - The SearchTextBuilder (fetch all tracks for configured index_languages)
//   - The source_version computation (text_hash inclusion)
//   - The Materializer (lookup-before-translate gate + audit-friendly
//     translation insert path) — PR-CATALOG-MULTILINGUA step 4

import "context"

// TextTrackRepository is the canonical port for persisting and querying
// localized text tracks per media asset.
type TextTrackRepository interface {
	// UpsertBatch atomically inserts or updates a batch of text tracks.
	// Each track is upserted on the UNIQUE(asset_id, language_code, text_kind)
	// constraint. Existing rows have their text_content, text_hash,
	// source_type, status, and updated_at refreshed.
	UpsertBatch(ctx context.Context, tracks []TextTrack) error

	// Find returns a single text track for the given (asset, language, kind)
	// triple. Returns (nil, nil) when no row exists (not-found is not an error).
	Find(ctx context.Context, assetID string, languageCode string, kind TextTrackKind) (*TextTrack, error)

	// ListByAsset returns all text tracks for the given asset, ordered by
	// language_code, text_kind. Returns an empty slice (not nil) when no
	// tracks exist.
	ListByAsset(ctx context.Context, assetID string) ([]TextTrack, error)

	// FindReady is the canonical typed lookup the resolver uses for
	// ResolveLanguage + ResolveBestAvailable (PR-PY-CLIPS-CORRETTE-TRADOTTE
	// Fase 1.b, July 2026) AND for the ClipSourceBuilder video-pipeline
	// cutover (Fase 4, July 2026). It returns a single text track
	// for the given (asset, language, kind) triple, filtered to
	// status=READY, PLUS the timed cues if the source carried
	// per-segment timing (VTT-derived rows).
	//
	// Return contract (Fase 4):
	//   (track, cues, nil) — track found and READY; cues is nil
	//                        when the source is payload-text,
	//                        DB-stored full-text, or Whisper.
	//   (nil, nil, nil)    — no row OR row in non-READY status
	//                        (PENDING / FAILED). The READY-only
	//                        filter is the canonical contract: a
	//                        non-READY row is not authoritative,
	//                        and the pipeline surfaces
	//                        ErrTextTrackNotReady rather than
	//                        using a stale row.
	//   (nil, nil, err)    — repository-level error.
	FindReady(ctx context.Context, assetID string, languageCode string, kind TextTrackKind) (*TextTrack, []TimedCue, error)

	// ListReadyLanguages enumerates the sorted set of language
	// codes for which a READY text track exists for the given
	// (asset, kind). Populates the `AvailableLanguages` field of
	// `*ErrTextTrackNotReady` so operator dashboards surface
	// "what's actually READY" without requiring a second
	// round-trip. Returns an empty slice (not nil) when no
	// READY tracks exist.
	//
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): this method
	// was promoted to the canonical port surface so the video
	// pipeline (ClipSourceBuilder) and the backfill CLI (Fase 5)
	// can share one canonical sub-surface (TextTrackReader). The
	// concrete SQLite implementation lives in
	// `internal/platform/sqlite/assets/`.
	ListReadyLanguages(ctx context.Context, assetID string, kind TextTrackKind) ([]string, error)

	// FindCurrentForTranslation is the dedicated READY+is_current=1
	// lookup the Materializer uses as the
	// lookup-before-translate gate (PR-CATALOG-MULTILINGUA step 4).
	//
	// Inputs (godlike/07 contract — empty fields are a caller bug,
	// not a fallback directive; the port computes the
	// translation_key internally via asset.TranslationKey):
	//
	//	assetID, kind, targetLanguageCode, sourceTextHash,
	//	translationModel, modelVersion, promptVersion
	//
	// Behaviour: internally calls
	// `translationKey := asset.TranslationKey(sourceTextHash,
	//   targetLanguageCode, translationModel, modelVersion,
	//   promptVersion)` and then runs
	// `SELECT WHERE asset_id=? AND language_code=? AND text_kind=?
	//   AND translation_key=? AND is_current=1 AND status='READY'`.
	//
	// Returns (track, nil) on hit + (nil, nil) on miss. The lookup
	// is index-only (no row scan) thanks to the partial UNIQUE
	// INDEX idx_asset_text_tracks_current WHERE is_current=1 +
	// the translation_key idx_asset_text_tracks_hash reuse
	// (additive per migration 155).
	//
	// godlike/06 SSOT: this is the SOLE canonical "is there already
	// a covered translation under this 6-tuple (5-tuple
	// translation_key inputs + asset_id/kind)?" query. Callers
	// MUST NOT compose the predicate inline — the canonical owner
	// of the lookup formula is this port method, not the
	// application layer. The application layer passes the natural
	// inputs; the repo computes the translation_key via
	// asset.TranslationKey.
	//
	// Stale is_current=0 rows (audit predecessors) are NOT visible
	// through this method — they stay for forensic dumps via
	// ListByAsset. The partial UNIQUE INDEX guarantees fewer-cost
	// lookup + split-brain protection.
	FindCurrentForTranslation(
		ctx context.Context,
		assetID string,
		kind TextTrackKind,
		targetLanguageCode string,
		sourceTextHash string,
		translationModel string,
		modelVersion string,
		promptVersion string,
	) (*TextTrack, error)

	// InsertTranslationWithAuditPredecessor inserts a new TextTrack
	// row marking it is_current=1, atomically flipping any prior
	// is_current=1 row for the same (asset, language, kind)
	// context to is_current=0 — preserving the audit-trail
	// invariant "previous tracks stay, never silently overwritten"
	// (PR-CATALOG-MULTILINGUA step 4).
	//
	// Inputs (godlike/07 contract — caller-provided):
	//   - track.PromptVersion + track.TranslationKey + track.IsCurrent=true
	//
	// Behaviour:
	//
	//	BEGIN IMMEDIATE TRANSACTION;
	//	  UPDATE asset_text_tracks SET is_current = 0, updated_at = ...
	//	  WHERE asset_id=? AND language_code=? AND text_kind=?
	//	    AND is_current = 1 AND translation_key != ?;
	//	  INSERT INTO asset_text_tracks (..., is_current=1, ...) VALUES (...);
	//	COMMIT;
	//
	// The UPDATE is a NO-OP when no prior row exists (or the prior
	// row already carries the same translation_key as the new
	// row — idempotency case). The partial UNIQUE INDEX
	// idx_asset_text_tracks_current guarantees that the INSERT
	// cannot split-brain against an existing is_current=1 row.
	//
	// godlike/07 honest lock: the insert ALWAYS flips the prior
	// is_current=1 row. There is no silent UPSERT semantic here;
	// audit-trail is non-negotiable. Callers wanting UPSERT-on-
	// identify semantics (Whisper acquire, initial embed) MUST
	// use UpsertBatch instead. This method is BACKWARD-INCOMPATIBLE
	// with the migration 137 UNIQUE(asset_id, language_code,
	// text_kind) constraint — migration 155 replaces the
	// constraint with the partial UNIQUE INDEX WHERE is_current=1.
	//
	// godlike/06 SSOT: this is the SOLE canonical
	// "flip-and-insert translation row" path. Callers MUST NOT
	// inline UPDATE+INSERT pairs (split-brain risk) or substitute
	// the legacy UpsertBatch (silent-overwrite risk).
	InsertTranslationWithAuditPredecessor(ctx context.Context, track TextTrack) error
}

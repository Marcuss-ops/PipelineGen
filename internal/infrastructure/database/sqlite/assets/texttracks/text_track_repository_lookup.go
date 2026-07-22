// Package assets — text_track_repository_lookup.go
//
// TextTrackRepositorySQLite read paths (finders). All read-only.
//   - Find                       — non-status-filtered single-track lookup.
//   - FindReady                  — READY-only single-track lookup (resolver + Fase 4
//     ClipSourceBuilder video-pipeline cutover).
//     Returns (track, cues, err) — Fase 4 contract.
//   - FindCurrentForTranslation  — translation-fingerprint lookup-before-translate gate
//     (PR-CATALOG-MULTILINGUA step 4).
//   - ListByAsset                — all tracks for one asset (admin/debug).
//   - ListReadyLanguages         — languages that have a READY track for an asset.
//   - findCuesForTrackID         — per-segment timed-cue fetch for FindReady.
package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Find returns a single text track for the given (asset, language, kind)
// triple. Returns (nil, nil) when no row exists.
func (r *TextTrackRepositorySQLite) Find(ctx context.Context, assetID string, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	if assetID == "" {
		return nil, fmt.Errorf("text_track_repository.Find: AssetID is required")
	}

	row := r.db.QueryRowContext(ctx,
		`SELECT id, asset_id, language_code, text_kind,
		        text_content,
		        source_type, source_language_code, is_original,
		        provider, model_name, model_version, prompt_version,
		        text_hash, source_version, translation_key, is_current,
		        source_track_id, source_text_hash,
		        confidence, status,
		        created_at, updated_at
		 FROM asset_text_tracks
		 WHERE asset_id = ? AND language_code = ? AND text_kind = ?`,
		assetID, languageCode, string(kind),
	)

	t, err := scanTextTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("text_track_repository.Find: %w", err)
	}
	return t, nil
}

// FindReady is the READY-only typed lookup the resolver uses for
// ResolveLanguage + ResolveBestAvailable (PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 1.b, July 2026) AND the Fase 4 ClipSourceBuilder video-pipeline
// cutover. It returns a single text track PLUS its timed cues (if the
// source carried per-segment timing) for the given (asset, language,
// kind) triple, filtered to status=READY.
//
// Return contract (Fase 4, matches the domain port):
//
//	(track, cues, nil)  — track found and READY; cues is nil when
//	                      the source is payload-text, full-text, or
//	                      Whisper (no per-segment timing persisted).
//	(nil, nil, nil)     — no row OR row in non-READY status
//	                      (PENDING/FAILED). The READY-only filter
//	                      is the canonical contract: a non-READY
//	                      row is not authoritative.
//	(nil, nil, err)     — repository-level error.
//
// godlike/06 SSOT: the underlying SQL is identical to Find
// (same column shape) plus a `status = 'ready'` predicate. The
// domain-level "filter to READY" decision is owned by this method
// so callers (resolver) MUST NOT re-implement a status-check
// inline.
func (r *TextTrackRepositorySQLite) FindReady(ctx context.Context, assetID string, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	if assetID == "" {
		return nil, nil, fmt.Errorf("text_track_repository.FindReady: AssetID is required")
	}
	if languageCode == "" {
		return nil, nil, fmt.Errorf("text_track_repository.FindReady: LanguageCode is required")
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, asset_id, language_code, text_kind,
		        text_content,
		        source_type, source_language_code, is_original,
		        provider, model_name, model_version, prompt_version,
		        text_hash, source_version, translation_key, is_current,
		        source_track_id, source_text_hash,
		        confidence, status,
		        created_at, updated_at
		 FROM asset_text_tracks
		 WHERE asset_id = ? AND language_code = ? AND text_kind = ?
		   AND status = ?`,
		assetID, languageCode, string(kind), string(asset.TextTrackReady),
	)

	t, err := scanTextTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("text_track_repository.FindReady: %w", err)
	}

	// Fase 4: fetch the cues for this track. Returns nil (not
	// an error) when the track has no per-segment timing rows
	// (the consumer can distinguish "no row" from "row with no
	// cues" via the parent *TextTrack nil-check).
	cues, cueErr := r.findCuesForTrackID(ctx, t.ID)
	if cueErr != nil {
		return nil, nil, fmt.Errorf("text_track_repository.FindReady: cues: %w", cueErr)
	}
	return t, cues, nil
}

// findCuesForTrackID returns the timed cues for a given track_id,
// sorted ascending by sequence_no (1-based; sequence_no is assigned
// at persist time by the writer, not at read time). Returns nil
// (not an empty slice — the domain port contract requires nil for
// "no cues", not a zero-length slice) when the track has no
// per-segment rows.
//
// PR-CATALOG-MULTILINGUA step 2 (July 2026): the SELECT projection
// includes the new text_hash column added by migration 156. The
// current TimedCue struct (asset.TimedCue = {StartMs, EndMs, Text})
// does NOT carry TextHash on the wire yet — the column is read so
// it lands in the row memory and is discarded. A future step adds
// a TimedCue.TextHash field + a Method that surfaces the segment
// hashes to callers (e.g. "skip-WAV if identical segment hash
// already exists in the track" fast path).
//
// Caller MUST pass a valid track_id (the FK ON DELETE CASCADE
// ensures orphan rows are impossible).
func (r *TextTrackRepositorySQLite) findCuesForTrackID(ctx context.Context, trackID int64) ([]asset.TimedCue, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT start_ms, end_ms, text, text_hash
		 FROM asset_text_track_segments
		 WHERE track_id = ?
		 ORDER BY sequence_no ASC`,
		trackID,
	)
	if err != nil {
		return nil, fmt.Errorf("findCuesForTrackID: query: %w", err)
	}
	defer rows.Close()

	var cues []asset.TimedCue // nil when no rows (matches domain port contract)
	for rows.Next() {
		var c asset.TimedCue
		var segHash string
		if scanErr := rows.Scan(&c.StartMs, &c.EndMs, &c.Text, &segHash); scanErr != nil {
			return nil, fmt.Errorf("findCuesForTrackID: scan: %w", scanErr)
		}
		// Note: segHash is currently discarded — TimedCue
		// has no TextHash field yet. The column is read so
		// the scanner's column count matches the SELECT
		// projection; future steps expose the hash through
		// TimedCue (PR-CATALOG-MULTILINGUA step 8+).
		_ = segHash
		cues = append(cues, c)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("findCuesForTrackID: rows: %w", err)
	}
	return cues, nil
}

// ListReadyLanguages enumerates the sorted set of language codes
// for which a READY text track exists for the given (asset, kind).
// Returns an empty slice (not nil) when no READY tracks exist.
//
// godlike/06 SSOT: this is the SOLE canonical "what READY languages
// does this clip have?" query. The require_all_before_video policy
// gate and the video pipeline's backfill CLI consume it.
func (r *TextTrackRepositorySQLite) ListReadyLanguages(ctx context.Context, assetID string, kind asset.TextTrackKind) ([]string, error) {
	if assetID == "" {
		return nil, fmt.Errorf("text_track_repository.ListReadyLanguages: AssetID is required")
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT language_code
		 FROM asset_text_tracks
		 WHERE asset_id = ? AND text_kind = ? AND status = ?
		 ORDER BY language_code ASC`,
		assetID, string(kind), string(asset.TextTrackReady),
	)
	if err != nil {
		return nil, fmt.Errorf("text_track_repository.ListReadyLanguages: query: %w", err)
	}
	defer rows.Close()

	languages := make([]string, 0)
	for rows.Next() {
		var lang string
		if scanErr := rows.Scan(&lang); scanErr != nil {
			return nil, fmt.Errorf("text_track_repository.ListReadyLanguages: scan: %w", scanErr)
		}
		languages = append(languages, lang)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("text_track_repository.ListReadyLanguages: rows: %w", err)
	}
	return languages, nil
}

// ListByAsset returns all text tracks for the given asset, ordered by
// language_code, text_kind. Returns an empty slice when no tracks exist.
func (r *TextTrackRepositorySQLite) ListByAsset(ctx context.Context, assetID string) ([]asset.TextTrack, error) {
	if assetID == "" {
		return nil, fmt.Errorf("text_track_repository.ListByAsset: AssetID is required")
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, asset_id, language_code, text_kind,
		        text_content,
		        source_type, source_language_code, is_original,
		        provider, model_name, model_version, prompt_version,
		        text_hash, source_version, translation_key, is_current,
		        source_track_id, source_text_hash,
		        confidence, status,
		        created_at, updated_at
		 FROM asset_text_tracks
		 WHERE asset_id = ?
		 ORDER BY language_code, text_kind`,
		assetID,
	)
	if err != nil {
		return nil, fmt.Errorf("text_track_repository.ListByAsset: query: %w", err)
	}
	defer rows.Close()

	tracks := make([]asset.TextTrack, 0)
	for rows.Next() {
		t, scanErr := scanTextTrackRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("text_track_repository.ListByAsset: scan: %w", scanErr)
		}
		tracks = append(tracks, *t)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("text_track_repository.ListByAsset: rows: %w", err)
	}
	return tracks, nil
}

// FindCurrentForTranslation is the canonical lookup-before-translate
// gate (PR-CATALOG-MULTILINGUA step 4, July 2026). Returns the
// is_current=1 + status=READY row whose translation_key fingerprint
// matches the input 5-tuple (asset_id, kind, target_language,
// source_text_hash, model_version, prompt_version), or (nil, nil)
// when no row exists.
//
// godlike/06 SSOT — the lookup predicate is owned here:
//   - WHERE on (asset_id, language_code, text_kind) — the lookup key.
//   - AND translation_key = ? — the request fingerprint (5-tuple
//     SHA-256 computed INTERNALLY via asset.TranslationKey — the
//     caller passes the natural 5-tuple inputs, NOT a precomputed
//     hash, so the canonical formula has exactly one owner).
//   - AND is_current = 1 — split-brain guard via the partial UNIQUE
//     INDEX idx_asset_text_tracks_current (migration 155).
//   - AND status = 'READY' — non-READY rows are not authoritative
//     (matches FindReady semantics for symmetry).
//
// Caller passes the natural 5-tuple inputs (no precomputed
// translation_key). The repo computes the key via
// asset.TranslationKey; off-port callers that want to reuse the
// precomputed key directly should compose via the SQL projection
// instead of inlining the predicate (godlike/06).
func (r *TextTrackRepositorySQLite) FindCurrentForTranslation(
	ctx context.Context,
	assetID string,
	kind asset.TextTrackKind,
	targetLanguageCode string,
	sourceTextHash string,
	translationModel string,
	modelVersion string,
	promptVersion string,
) (*asset.TextTrack, error) {
	if assetID == "" {
		return nil, fmt.Errorf("text_track_repository.FindCurrentForTranslation: AssetID is required")
	}
	if targetLanguageCode == "" {
		return nil, fmt.Errorf("text_track_repository.FindCurrentForTranslation: targetLanguageCode is required")
	}
	if sourceTextHash == "" {
		return nil, fmt.Errorf("text_track_repository.FindCurrentForTranslation: sourceTextHash is required (caller bug: did not pass the source-text fingerprint)")
	}

	// Compute the 5-tuple translation_key fingerprint via the
	// canonical SSOT formula (matches the inputs consumed by
	// InsertTranslationWithAuditPredecessor → no fingerprint drift
	// between the lookup and the persistence path).
	translationKey := asset.TranslationKey(
		sourceTextHash,
		targetLanguageCode,
		translationModel,
		modelVersion,
		promptVersion,
	)

	row := r.db.QueryRowContext(ctx,
		`SELECT id, asset_id, language_code, text_kind,
		        text_content,
		        source_type, source_language_code, is_original,
		        provider, model_name, model_version, prompt_version,
		        text_hash, source_version, translation_key, is_current,
		        source_track_id, source_text_hash,
		        confidence, status,
		        created_at, updated_at
		 FROM asset_text_tracks
		 WHERE asset_id = ? AND language_code = ? AND text_kind = ?
		   AND translation_key = ? AND is_current = 1
		   AND status = ?`,
		assetID, targetLanguageCode, string(kind), translationKey, string(asset.TextTrackReady),
	)

	t, err := scanTextTrack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("text_track_repository.FindCurrentForTranslation: %w", err)
	}
	return t, nil
}

// Package assets — text_track_repository.go
//
// SQLite concrete for the TextTrackRepository port. Persists and queries
// localized text tracks (transcript, description, summary, title,
// keywords) per media asset. Used by the YouTube writer path, the
// TextTrackResolver lookup-before-Whisper fast path, and the
// SearchTextBuilder for multilingual embedding construction.
package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// TextTrackRepositorySQLite is the SQLite-backed implementation of
// asset.TextTrackRepository.
type TextTrackRepositorySQLite struct {
	db  *sql.DB
	log *zap.Logger
}

// NewTextTrackRepository builds a SQLite-backed text track repository.
func NewTextTrackRepository(db *sql.DB, log *zap.Logger) (*TextTrackRepositorySQLite, error) {
	if db == nil {
		return nil, errors.New("text_track_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &TextTrackRepositorySQLite{db: db, log: log}, nil
}

var _ asset.TextTrackRepository = (*TextTrackRepositorySQLite)(nil)

const upsertTextTrackSQL = `
INSERT INTO asset_text_tracks (
    asset_id, language_code, text_kind,
    text_content,
    source_type, source_language_code, is_original,
    provider, model_name, model_version, prompt_version,
    text_hash, source_version, translation_key, is_current,
    confidence, status,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, datetime('now'), datetime('now'))
ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE SET
    text_content         = excluded.text_content,
    source_type          = excluded.source_type,
    source_language_code = excluded.source_language_code,
    is_original          = excluded.is_original,
    provider             = excluded.provider,
    model_name           = excluded.model_name,
    model_version        = excluded.model_version,
    prompt_version       = excluded.prompt_version,
    text_hash            = excluded.text_hash,
    source_version       = excluded.source_version,
    translation_key      = excluded.translation_key,
    is_current           = excluded.is_current,
    confidence           = excluded.confidence,
    status               = excluded.status,
    updated_at           = datetime('now')
`

// UpsertBatch atomically inserts or updates a batch of text tracks.
func (r *TextTrackRepositorySQLite) UpsertBatch(ctx context.Context, tracks []asset.TextTrack) error {
	if len(tracks) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("text_track_repository.UpsertBatch: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, upsertTextTrackSQL)
	if err != nil {
		return fmt.Errorf("text_track_repository.UpsertBatch: prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range tracks {
		if t.AssetID == "" {
			return fmt.Errorf("text_track_repository.UpsertBatch: AssetID is required")
		}
		if t.LanguageCode == "" {
			return fmt.Errorf("text_track_repository.UpsertBatch: LanguageCode is required")
		}
		if t.TextKind == "" {
			return fmt.Errorf("text_track_repository.UpsertBatch: TextKind is required")
		}

		var confidence sql.NullFloat64
		if t.Confidence != nil {
			confidence = sql.NullFloat64{Float64: *t.Confidence, Valid: true}
		}

		sourceVersion := t.SourceVersion

		isOriginal := 0
		if t.IsOriginal {
			isOriginal = 1
		}

		status := string(t.Status)
		if status == "" {
			status = string(asset.TextTrackReady)
		}

		if _, err = stmt.ExecContext(ctx,
			t.AssetID,
			t.LanguageCode,
			string(t.TextKind),
			t.TextContent,
			string(t.SourceType),
			t.SourceLanguageCode,
			isOriginal,
			t.Provider,
			t.ModelName,
			t.ModelVersion,
			t.PromptVersion,
			t.TextHash,
			sourceVersion,
			t.TranslationKey,
			confidence,
			status,
		); err != nil {
			return fmt.Errorf("text_track_repository.UpsertBatch: exec (asset=%s lang=%s kind=%s): %w",
				t.AssetID, t.LanguageCode, t.TextKind, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("text_track_repository.UpsertBatch: commit: %w", err)
	}
	return nil
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
// Caller MUST pass a valid track_id (the FK ON DELETE CASCADE
// ensures orphan rows are impossible).
func (r *TextTrackRepositorySQLite) findCuesForTrackID(ctx context.Context, trackID int64) ([]asset.TimedCue, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT start_ms, end_ms, text
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
		if scanErr := rows.Scan(&c.StartMs, &c.EndMs, &c.Text); scanErr != nil {
			return nil, fmt.Errorf("findCuesForTrackID: scan: %w", scanErr)
		}
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

// textTrackScanner abstracts sql.Row vs sql.Rows for scan.
type textTrackScanner interface {
	Scan(dest ...any) error
}

func scanTextTrack(s textTrackScanner) (*asset.TextTrack, error) {
	var t asset.TextTrack
	var (
		id             int64
		assetID        string
		languageCode   string
		textKind       string
		textContent    string
		sourceType     string
		sourceLangCode string
		isOriginal     int
		provider       string
		modelName      string
		modelVersion   string
		promptVersion  string
		textHash       string
		sourceVersion  string
		translationKey string
		isCurrent      int
		confidence     sql.NullFloat64
		status         string
		createdAtStr   string
		updatedAtStr   string
	)

	err := s.Scan(
		&id, &assetID, &languageCode, &textKind,
		&textContent,
		&sourceType, &sourceLangCode, &isOriginal,
		&provider, &modelName, &modelVersion, &promptVersion,
		&textHash, &sourceVersion, &translationKey, &isCurrent,
		&confidence, &status,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		return nil, err
	}

	t.ID = id
	t.AssetID = assetID
	t.LanguageCode = languageCode
	t.TextKind = asset.TextTrackKind(textKind)
	t.TextContent = textContent
	t.SourceType = asset.TextTrackSource(sourceType)
	t.SourceLanguageCode = sourceLangCode
	t.IsOriginal = isOriginal == 1
	t.Provider = provider
	t.ModelName = modelName
	t.ModelVersion = modelVersion
	t.PromptVersion = promptVersion
	t.TextHash = textHash
	t.SourceVersion = sourceVersion
	t.TranslationKey = translationKey
	t.IsCurrent = isCurrent == 1
	if confidence.Valid {
		v := confidence.Float64
		t.Confidence = &v
	}
	t.Status = asset.TextTrackStatus(status)

	if createdAtStr != "" {
		if parsed := timeutil.ParseRFC3339(createdAtStr); !parsed.IsZero() {
			t.CreatedAt = parsed
		}
	}
	if updatedAtStr != "" {
		if parsed := timeutil.ParseRFC3339(updatedAtStr); !parsed.IsZero() {
			t.UpdatedAt = parsed
		}
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}

	return &t, nil
}

func scanTextTrackRows(rows *sql.Rows) (*asset.TextTrack, error) {
	return scanTextTrack(rows)
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

// InsertTranslationWithAuditPredecessor atomically inserts a new
// is_current=1 row and flips any prior is_current=1 row for the same
// (asset, language, kind) context to is_current=0 — preserving the
// audit-trail invariant (PR-CATALOG-MULTILINGUA step 4, July 2026).
//
// godlike/06 SSOT — transaction shape:
//   - BEGIN IMMEDIATE TRANSACTION (WRITE-locked at the SQLite
//     boundary so concurrent Materialize() invocations serialize
//     on the same (asset, language, kind) context).
//   - SELECT ... WHERE (asset, lang, kind, translation_key, is_current=1)
//     LIMIT 1. If a row IS found, COMMIT as a no-op (idempotency
//     short-circuit). godlike/07 honest lock — never silently drop
//     a duplicate translation request.
//   - Else: UPDATE ALL prior is_current=1 rows for (asset, lang, kind)
//     to is_current=0 (regardless of their translation_key; the
//     partial UNIQUE INDEX will be cleared).
//   - INSERT new row with is_current=1 (hard-coded; ignore caller
//     value to keep the audit invariant absolute).
//   - COMMIT.
//
// Race semantics: under WAL + 5s busy_timeout, two parallel calls
// for the same context serialize on the BEGIN IMMEDIATE lock.
//   - If both calls compute the same translation_key: first call
//     wins the INSERT (the second short-circuits via the
//     idempotency SELECT). This is the canonical
//     "reused-translation" path.
//   - If the calls compute different translation_keys (operator
//     bumped model_version or prompt_version between calls):
//     first call flips prior + INSERTs new. Second call's
//     idempotency SELECT returns nil, second call proceeds with
//     its own flip + INSERT (chain of audit predecessors).
//   - This is the audit-trail-maximising race semantics, NOT a
//     silent-overwrite trade-off.
func (r *TextTrackRepositorySQLite) InsertTranslationWithAuditPredecessor(ctx context.Context, track asset.TextTrack) error {
	if track.AssetID == "" {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: AssetID is required")
	}
	if track.LanguageCode == "" {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: LanguageCode is required")
	}
	if track.TextKind == "" {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: TextKind is required")
	}
	if track.TranslationKey == "" {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: TranslationKey is required (caller bug)")
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Step 1 (idempotency short-circuit): SELECT the canonical
	// is_current=1 row for this exact 5-tuple. If present, the
	// caller has already produced this exact translation under
	// these exact conditions — COMMIT as a no-op and return.
	var existingID int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM asset_text_tracks
		 WHERE asset_id = ? AND language_code = ? AND text_kind = ?
		   AND translation_key = ? AND is_current = 1
		 LIMIT 1`,
		track.AssetID, track.LanguageCode, string(track.TextKind), track.TranslationKey,
	).Scan(&existingID)
	switch {
	case err == nil:
		// Idempotency hit — return without modifying anything.
		if commitErr := tx.Commit(); commitErr != nil {
			return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: idempotency-commit: %w", commitErr)
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		// No idempotent match — fall through to flip + INSERT.
		err = nil
	default:
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: idempotency-check: %w", err)
	}

	// Step 2: flip ALL prior is_current=1 rows for (asset, lang, kind)
	// to is_current=0. Removes the partial-UNIQUE-INDEX entry so
	// the next INSERT can land with is_current=1 without collision.
	//
	// Safety: this UPDATE is unconditional (no translation_key
	// filter) because Step 1's SELECT already guarantees that no
	// is_current=1 row with the SAME translation_key can reach
	// here. The flip is therefore flipping AUDIT PREDECESSORS
	// (rows with a different translation_key) and never the
	// caller's own row. A future maintainer who bypasses Step 1
	// MUST re-add the `AND translation_key != ?` filter to
	// preserve idempotency on the same-key case.
	if _, err = tx.ExecContext(ctx,
		`UPDATE asset_text_tracks
		 SET is_current = 0,
		     updated_at = datetime('now')
		 WHERE asset_id = ? AND language_code = ? AND text_kind = ?
		   AND is_current = 1`,
		track.AssetID, track.LanguageCode, string(track.TextKind),
	); err != nil {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: flip prior row: %w", err)
	}

	// Step 3: insert the new row with is_current=1 (hard-coded).
	var confidence sql.NullFloat64
	if track.Confidence != nil {
		confidence = sql.NullFloat64{Float64: *track.Confidence, Valid: true}
	}

	sourceVersion := track.SourceVersion
	isOriginal := 0
	if track.IsOriginal {
		isOriginal = 1
	}

	status := string(track.Status)
	if status == "" {
		status = string(asset.TextTrackReady)
	}

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO asset_text_tracks (
			asset_id, language_code, text_kind,
			text_content,
			source_type, source_language_code, is_original,
			provider, model_name, model_version, prompt_version,
			text_hash, source_version, translation_key, is_current,
			confidence, status,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, datetime('now'), datetime('now'))`,
		track.AssetID,
		track.LanguageCode,
		string(track.TextKind),
		track.TextContent,
		string(track.SourceType),
		track.SourceLanguageCode,
		isOriginal,
		track.Provider,
		track.ModelName,
		track.ModelVersion,
		track.PromptVersion,
		track.TextHash,
		sourceVersion,
		track.TranslationKey,
		confidence,
		status,
	); err != nil {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: insert new row: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("text_track_repository.InsertTranslationWithAuditPredecessor: commit: %w", err)
	}
	return nil
}

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
    provider, model_name, model_version,
    text_hash, source_version,
    confidence, status,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
ON CONFLICT(asset_id, language_code, text_kind) DO UPDATE SET
    text_content         = excluded.text_content,
    source_type          = excluded.source_type,
    source_language_code = excluded.source_language_code,
    is_original          = excluded.is_original,
    provider             = excluded.provider,
    model_name           = excluded.model_name,
    model_version        = excluded.model_version,
    text_hash            = excluded.text_hash,
    source_version       = excluded.source_version,
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
			t.TextHash,
			sourceVersion,
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
//   (track, cues, nil)  — track found and READY; cues is nil when
//                         the source is payload-text, full-text, or
//                         Whisper (no per-segment timing persisted).
//   (nil, nil, nil)     — no row OR row in non-READY status
//                         (PENDING/FAILED). The READY-only filter
//                         is the canonical contract: a non-READY
//                         row is not authoritative.
//   (nil, nil, err)     — repository-level error.
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
		        provider, model_name, model_version,
		        text_hash, source_version,
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
		        provider, model_name, model_version,
		        text_hash, source_version,
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
		        provider, model_name, model_version,
		        text_hash, source_version,
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
		textHash       string
		sourceVersion  string
		confidence     sql.NullFloat64
		status         string
		createdAtStr   string
		updatedAtStr   string
	)

	err := s.Scan(
		&id, &assetID, &languageCode, &textKind,
		&textContent,
		&sourceType, &sourceLangCode, &isOriginal,
		&provider, &modelName, &modelVersion,
		&textHash, &sourceVersion,
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
	t.TextHash = textHash
	t.SourceVersion = sourceVersion
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

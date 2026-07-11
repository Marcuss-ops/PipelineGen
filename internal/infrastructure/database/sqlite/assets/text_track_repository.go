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
		id              int64
		assetID         string
		languageCode    string
		textKind        string
		textContent     string
		sourceType      string
		sourceLangCode  string
		isOriginal      int
		provider        string
		modelName       string
		modelVersion    string
		textHash        string
		sourceVersion   string
		confidence      sql.NullFloat64
		status          string
		createdAtStr    string
		updatedAtStr    string
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

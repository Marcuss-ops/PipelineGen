package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SoundEffectCatalog is the shared PostgreSQL reader behind the operator
// sound-effect tools (classify / trim / download / organize).
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): these tools previously ran
// four separate SQLite reads of `media_assets` directly in `cmd/admin`. The
// media SSOT is PostgreSQL, so each read could only ever answer from a database
// that holds no committed media rows. Concentrating the projection here gives
// the four call sites ONE engine decision point instead of four copies of the
// same `source='sound_effect'` predicate (godlike/06 one-owner-per-fact).
//
// A nil handle returns nil from the constructor so callers fail closed rather
// than silently degrading onto the operational SQLite store.
type SoundEffectCatalog struct {
	db *sql.DB
}

// SoundEffectRow is the operator-tool projection of a sound-effect asset. It is
// deliberately a superset of the four former per-command column lists.
type SoundEffectRow struct {
	ID           string
	Name         string
	DriveFileID  string
	FolderPath   string
	LocalPath    string
	MetadataJSON string
}

// NewSoundEffectCatalog binds the reader to the media SSOT handle.
func NewSoundEffectCatalog(db *sql.DB) *SoundEffectCatalog {
	if db == nil {
		return nil
	}
	return &SoundEffectCatalog{db: db}
}

// soundEffectWhere is the canonical sound-effect predicate shared by every
// method below so the four tools cannot drift in eligibility.
const soundEffectWhere = `source = 'sound_effect' AND media_type = 'sound_effect' AND category = 'file'`

// ListIDs returns the sound-effect asset IDs ordered by name then id, matching
// the retired SQLite listing used by `classify-sound-effects` and
// `trim-sound-effects`.
func (c *SoundEffectCatalog) ListIDs(ctx context.Context) ([]string, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: media reader unavailable")
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE `+soundEffectWhere+`
		ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: list ids: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, 64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres sound-effect catalog: scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: iterate ids: %w", err)
	}
	return ids, nil
}

// ListRows returns the full operator projection ordered by id, matching the
// retired SQLite listing used by `download-sound-effects` and the Drive
// classification load used by `organize-sound-effects-drive`.
//
// The caller receives metadata_json as TEXT (not jsonb): the two consumers
// parse it in Go and must keep reporting a typed error on malformed metadata
// rather than failing the whole query.
func (c *SoundEffectCatalog) ListRows(ctx context.Context) ([]SoundEffectRow, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: media reader unavailable")
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, COALESCE(name, ''), COALESCE(drive_file_id, ''),
		       COALESCE(folder_path, ''), COALESCE(local_path, ''),
		       COALESCE(NULLIF(metadata_json, ''), '{}')
		FROM media_assets
		WHERE `+soundEffectWhere+`
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: list rows: %w", err)
	}
	defer rows.Close()

	out := make([]SoundEffectRow, 0, 64)
	for rows.Next() {
		var row SoundEffectRow
		if err := rows.Scan(&row.ID, &row.Name, &row.DriveFileID, &row.FolderPath, &row.LocalPath, &row.MetadataJSON); err != nil {
			return nil, fmt.Errorf("postgres sound-effect catalog: scan row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: iterate rows: %w", err)
	}
	return out, nil
}

// FindByName returns the first sound-effect row whose name matches any of the
// supplied names (the retired `name IN (?, ?) LIMIT 1` probe used by
// `apply-additional-sound-effects`, where the asset may already carry either the
// old or the new name). An absent asset is (nil, nil) so the caller keeps its
// "already superseded or absent" classification.
func (c *SoundEffectCatalog) FindByName(ctx context.Context, names ...string) (*SoundEffectRow, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: media reader unavailable")
	}
	cleaned := make([]string, 0, len(names))
	for _, name := range names {
		if n := strings.TrimSpace(name); n != "" {
			cleaned = append(cleaned, n)
		}
	}
	if len(cleaned) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(cleaned))
	args := make([]any, len(cleaned))
	for i, name := range cleaned {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = name
	}
	query := `
		SELECT id, COALESCE(name, ''), COALESCE(drive_file_id, ''),
		       COALESCE(folder_path, ''), COALESCE(local_path, ''),
		       COALESCE(NULLIF(metadata_json, ''), '{}')
		FROM media_assets
		WHERE ` + soundEffectWhere + `
		  AND name IN (` + strings.Join(placeholders, ", ") + `)
		LIMIT 1`

	var row SoundEffectRow
	err := c.db.QueryRowContext(ctx, query, args...).Scan(
		&row.ID, &row.Name, &row.DriveFileID, &row.FolderPath, &row.LocalPath, &row.MetadataJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: find by name: %w", err)
	}
	return &row, nil
}

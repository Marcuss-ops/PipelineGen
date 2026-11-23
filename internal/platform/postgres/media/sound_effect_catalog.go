package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminmedia"
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

// SoundEffectProjectionRow is the identity + editorial-taxonomy projection of
// one indexed sound effect: the shape the script-generation selector and the
// JSON index export both need. It lives here, next to the predicate, so no
// caller re-opens `media_assets` with its own copy of
// `source='sound_effect' AND category='file'` (godlike/06 one owner per fact —
// and the MEDIA-SSOT read gate requires every media read to live in this
// package).
//
// DriveFileID and DriveLink are producer-location fields carried only so the
// export can print a link: they are runtime values, never part of a
// serialization contract, hence the explicit `json:"-"` markers (the media
// identity gate allows a location field beside a content address exactly when
// it is declared non-serializable).
type SoundEffectProjectionRow struct {
	AssetID     string
	Name        string
	Filename    string
	DriveFileID string `json:"-"`
	DriveLink   string `json:"-"`
	DurationMS  int64
	Family      string
	Subtype     string
	Mood        string
	Energy      string
	// BestFor and Tags are the raw comma-joined metadata values; the caller
	// splits them because each projection presents them differently.
	BestFor    string
	Tags       string
	FolderPath string
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
// The canonical taxonomy gate admits only audio/image/video/text/document as
// media_type, so an indexed effect is stored as `media_type = 'audio'` with
// `source = 'sound_effect'` and `asset_kind = 'sfx'`. Filtering on
// media_type = 'sound_effect' made every operator tool blind to the rows the
// canonical committer actually accepts; the source + category pair is the
// durable predicate.
const soundEffectWhere = `source = 'sound_effect' AND category = 'file'`

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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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

// ListProjectionRows returns the identity + taxonomy projection of every
// indexed effect, ordered by id. The ordering is stable and engine-owned; a
// caller that presents in another order (the JSON export groups by family,
// then name) sorts the returned slice itself.
func (c *SoundEffectCatalog) ListProjectionRows(ctx context.Context) ([]SoundEffectProjectionRow, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: media reader unavailable")
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, COALESCE(name, ''), COALESCE(filename, ''), COALESCE(drive_file_id, ''),
		       COALESCE(drive_link, ''), COALESCE(duration_ms, 0),
		       COALESCE(metadata_json::jsonb->>'sfx_family', ''),
		       COALESCE(metadata_json::jsonb->>'sfx_subtype', ''),
		       COALESCE(metadata_json::jsonb->>'sfx_mood', ''),
		       COALESCE(metadata_json::jsonb->>'sfx_energy', ''),
		       COALESCE(metadata_json::jsonb->>'sfx_best_for', ''),
		       COALESCE(metadata_json::jsonb->>'sfx_tags', ''),
		       COALESCE(folder_path, '')
		FROM media_assets
		WHERE `+soundEffectWhere+`
		  AND COALESCE(lifecycle_state, '') <> 'DELETED'
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: list projection rows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]SoundEffectProjectionRow, 0, 256)
	for rows.Next() {
		var row SoundEffectProjectionRow
		if err := rows.Scan(&row.AssetID, &row.Name, &row.Filename, &row.DriveFileID,
			&row.DriveLink, &row.DurationMS, &row.Family, &row.Subtype, &row.Mood,
			&row.Energy, &row.BestFor, &row.Tags, &row.FolderPath); err != nil {
			return nil, fmt.Errorf("postgres sound-effect catalog: scan projection row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres sound-effect catalog: iterate projection rows: %w", err)
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

// ── absorbed from sound_effect_source.go ──

// SoundEffectSource reads the canonical sound-effect projection from the
// PostgreSQL media SSOT.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): this replaces the retired
// read and media_assets is PostgreSQL-owned, so the previous SQLite read could
// only ever answer from a database that holds no committed media rows — the
// export saw an empty catalog while PostgreSQL held the assets. The
// `adminmedia.MetadataSource` port boundary already existed, so only the
// implementation moved.
//
// Note on the SQL translation: the SQLite query fell back to a `group_name`
// column that does not exist on the PostgreSQL media_assets projection, so the
// family is resolved from the canonical `metadata_json.sfx_family` only. The
// invalid-metadata contract is preserved (fail closed) — the SQLite version
// already returned an error on malformed metadata_json.
type SoundEffectSource struct {
	db *sql.DB
}

var _ adminmedia.MetadataSource = (*SoundEffectSource)(nil)

// NewSoundEffectSource binds the reader to the media SSOT handle. A nil handle
// returns nil so composition can fail closed rather than silently reading a
// second engine (godlike/07 no-fake-availability).
func NewSoundEffectSource(db *sql.DB) adminmedia.MetadataSource {
	if db == nil {
		return nil
	}
	return &SoundEffectSource{db: db}
}

const soundEffectSourceQuery = `
	SELECT id, COALESCE(name, ''), COALESCE(filename, ''),
	       COALESCE(drive_file_id, ''), COALESCE(drive_link, ''), COALESCE(download_link, ''),
	       COALESCE(local_path, ''), COALESCE(duration_ms, 0),
	       COALESCE(metadata_json::jsonb->>'sfx_family', ''),
	       COALESCE(metadata_json::jsonb->>'sfx_subtype', ''),
	       COALESCE(tags, ''), COALESCE(folder_id, ''), COALESCE(parent_folder_id, ''),
	       COALESCE(folder_path, ''), COALESCE(NULLIF(metadata_json, ''), '{}')
	FROM media_assets
	WHERE source = 'sound_effect' AND COALESCE(lifecycle_state, '') <> 'DELETED'
	ORDER BY COALESCE(metadata_json::jsonb->>'sfx_family', ''), name
`

// ListSoundEffects returns the canonical sound-effect projection ordered by
// family then name, matching the retired SQLite reader byte-for-byte in shape.
func (s *SoundEffectSource) ListSoundEffects(ctx context.Context) ([]adminmedia.SoundEffectMetadata, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres sound-effect source: media reader unavailable")
	}
	rows, err := s.db.QueryContext(ctx, soundEffectSourceQuery)
	if err != nil {
		return nil, fmt.Errorf("postgres sound-effect source: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make([]adminmedia.SoundEffectMetadata, 0, 64)
	for rows.Next() {
		var item adminmedia.SoundEffectMetadata
		var durationMS int64
		var metadata string
		if err := rows.Scan(&item.ID, &item.Name, &item.Filename, &item.DriveFileID, &item.DriveLink,
			&item.DownloadLink, &item.LocalPath, &durationMS, &item.Family, &item.Subtype, &item.Tags,
			&item.FolderID, &item.ParentFolderID, &item.FolderPath, &metadata); err != nil {
			return nil, fmt.Errorf("postgres sound-effect source: scan: %w", err)
		}
		item.DurationSeconds = float64(durationMS) / 1000
		if metadata == "" {
			metadata = "{}"
		}
		if !json.Valid([]byte(metadata)) {
			return nil, fmt.Errorf("invalid metadata_json for sound effect %s", item.ID)
		}
		item.Metadata = json.RawMessage(metadata)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres sound-effect source: iterate: %w", err)
	}
	return result, nil
}

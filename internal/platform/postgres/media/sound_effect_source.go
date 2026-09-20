package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminmedia"
)

// SoundEffectSource reads the canonical sound-effect projection from the
// PostgreSQL media SSOT.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): this replaces the retired
// SQLite `artlist.AdminMediaMetadataSource`. The projection is a media_assets
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
	defer rows.Close()

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

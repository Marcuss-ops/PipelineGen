package artlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
)

// AdminMediaMetadataSource exposes the canonical sound-effect projection to
// the operator-media application use case.
type AdminMediaMetadataSource struct {
	DB *sql.DB
}

func (s AdminMediaMetadataSource) ListSoundEffects(ctx context.Context) ([]adminmedia.SoundEffectMetadata, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, filename, drive_file_id, drive_link, download_link,
		       local_path, COALESCE(duration_ms, 0),
		       COALESCE(json_extract(metadata_json, '$.sfx_family'), group_name, ''),
		       COALESCE(json_extract(metadata_json, '$.sfx_subtype'), ''),
		       tags, folder_id, parent_folder_id, folder_path, COALESCE(metadata_json, '{}')
		FROM media_assets
		WHERE source = 'sound_effect' AND lifecycle_state <> 'DELETED'
		ORDER BY COALESCE(json_extract(metadata_json, '$.sfx_family'), group_name, ''), name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []adminmedia.SoundEffectMetadata
	for rows.Next() {
		var item adminmedia.SoundEffectMetadata
		var durationMS int64
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Filename, &item.DriveFileID, &item.DriveLink, &item.DownloadLink,
			&item.LocalPath, &durationMS, &item.Family, &item.Subtype, &item.Tags, &item.FolderID,
			&item.ParentFolderID, &item.FolderPath, &metadata); err != nil {
			return nil, err
		}
		item.DurationSeconds = float64(durationMS) / 1000
		if len(metadata) == 0 {
			metadata = []byte("{}")
		}
		if !json.Valid(metadata) {
			return nil, fmt.Errorf("invalid metadata_json for sound effect %s", item.ID)
		}
		item.Metadata = json.RawMessage(metadata)
		result = append(result, item)
	}
	return result, rows.Err()
}

var _ adminmedia.MetadataSource = AdminMediaMetadataSource{}

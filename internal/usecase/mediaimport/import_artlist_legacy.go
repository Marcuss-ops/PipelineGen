package mediaimport

import (
	"context"
	"database/sql"
	"time"

	"velox/go-master/internal/core/media"
)

type LegacyArtlistImporter struct {
	oldDB      *sql.DB
	mediaRepo  media.Repository
}

func NewLegacyArtlistImporter(oldDB *sql.DB, mediaRepo media.Repository) *LegacyArtlistImporter {
	return &LegacyArtlistImporter{
		oldDB:     oldDB,
		mediaRepo: mediaRepo,
	}
}

func (i *LegacyArtlistImporter) Import(ctx context.Context, workspaceID, projectID string) error {
	rows, err := i.oldDB.QueryContext(ctx, `
		SELECT clip_id, filename, title, duration, folder_path, tags
		FROM artlist_clips
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var clipID, filename, title string
		var duration int
		var folderPath, tags string

		if err := rows.Scan(&clipID, &filename, &title, &duration, &folderPath, &tags); err != nil {
			return err
		}

		if title == "" {
			title = filename
		}

		now := time.Now()
		item := &media.Item{
			ID:           clipID,
			WorkspaceID:  workspaceID,
			ProjectID:    projectID,
			SourceKind:   media.SourceKindArtlist,
			MediaType:    media.MediaTypeArtlistClip,
			Status:       media.MediaStatusReady,
			Title:        title,
			Description:  "",
			DurationSecs: duration,
			MetadataJSON: `{"folder_path":"` + folderPath + `","legacy":true}`,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := i.mediaRepo.CreateItem(ctx, item); err != nil {
			return err
		}

		if tags != "" {
			tagList := parseTags(tags)
			if len(tagList) > 0 {
				_ = i.mediaRepo.AddTags(ctx, item.ID, tagList)
			}
		}
	}

	return rows.Err()
}

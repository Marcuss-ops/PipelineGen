package mediaimport

import (
	"context"
	"database/sql"
	"time"

	"velox/go-master/internal/core/media"
)

type LegacyClipsImporter struct {
	oldDB      *sql.DB
	mediaRepo  media.Repository
}

func NewLegacyClipsImporter(oldDB *sql.DB, mediaRepo media.Repository) *LegacyClipsImporter {
	return &LegacyClipsImporter{
		oldDB:     oldDB,
		mediaRepo: mediaRepo,
	}
}

func (i *LegacyClipsImporter) Import(ctx context.Context, workspaceID, projectID string) error {
	rows, err := i.oldDB.QueryContext(ctx, `
		SELECT clip_id, filename, media_type, source, duration, local_path, drive_link, tags
		FROM clips
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var clipID, filename, mediaType, source, localPath, driveLink, tags string
		var duration int

		if err := rows.Scan(&clipID, &filename, &mediaType, &source, &duration, &localPath, &driveLink, &tags); err != nil {
			return err
		}

		sourceKind := media.SourceKindManual
		switch source {
		case "stock":
			sourceKind = media.SourceKindStock
		case "artlist":
			sourceKind = media.SourceKindArtlist
		case "youtube":
			sourceKind = media.SourceKindYouTube
		}

		now := time.Now()
		item := &media.Item{
			ID:           clipID,
			WorkspaceID:  workspaceID,
			ProjectID:    projectID,
			SourceKind:   sourceKind,
			MediaType:    media.MediaType(determineMediaType(mediaType)),
			Status:       media.MediaStatusReady,
			Title:        filename,
			Description:  "",
			DurationSecs: duration,
			MetadataJSON: `{"source":"` + source + `","legacy":true}`,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := i.mediaRepo.CreateItem(ctx, item); err != nil {
			return err
		}

		if localPath != "" {
			file := &media.File{
				ID:           "",
				MediaItemID:  item.ID,
				LocationKind: media.LocationKindLocal,
				URI:          localPath,
				Status:       media.MediaStatusReady,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_ = i.mediaRepo.CreateFile(ctx, file)
		}

		if driveLink != "" {
			file := &media.File{
				ID:           "",
				MediaItemID:  item.ID,
				LocationKind: media.LocationKindDrive,
				URI:          driveLink,
				Status:       media.MediaStatusReady,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_ = i.mediaRepo.CreateFile(ctx, file)
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

func determineMediaType(mediaType string) string {
	switch mediaType {
	case "video":
		return string(media.MediaTypeVideo)
	case "audio":
		return string(media.MediaTypeAudio)
	case "image":
		return string(media.MediaTypeImage)
	default:
		return string(media.MediaTypeVideo)
	}
}

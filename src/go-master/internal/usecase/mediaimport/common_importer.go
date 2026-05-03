package mediaimport

import (
	"context"
	"database/sql"
	"time"

	"velox/go-master/internal/core/media"
)

// LegacyRow represents a generic row from a legacy media table.
type LegacyRow interface {
	GetClipID() string
	GetFilename() string
	GetTitle() string
	GetDuration() int
	GetTags() string
	GetMetadata() string
	GetLocalPath() string
	GetDriveLink() string
	GetSourceKind() media.SourceKind
	GetMediaType() media.MediaType
}

// LegacyRowAdapter defines how to query and map a specific legacy table.
type LegacyRowAdapter interface {
	// Query returns the SQL query to fetch rows
	Query() string
	// Scan scans a row into a LegacyRow
	Scan(rows *sql.Rows) (LegacyRow, error)
}

// CommonLegacyImporter provides a unified way to import from legacy tables.
type CommonLegacyImporter struct {
	oldDB     *sql.DB
	mediaRepo media.Repository
}

// NewCommonLegacyImporter creates a new common legacy importer.
func NewCommonLegacyImporter(oldDB *sql.DB, mediaRepo media.Repository) *CommonLegacyImporter {
	return &CommonLegacyImporter{
		oldDB:     oldDB,
		mediaRepo: mediaRepo,
	}
}

// Import imports media items using the provided adapter.
func (i *CommonLegacyImporter) Import(ctx context.Context, workspaceID, projectID string, adapter LegacyRowAdapter) error {
	rows, err := i.oldDB.QueryContext(ctx, adapter.Query())
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		row, err := adapter.Scan(rows)
		if err != nil {
			return err
		}

		now := time.Now()
		title := row.GetTitle()
		if title == "" {
			title = row.GetFilename()
		}

		item := &media.Item{
			ID:           row.GetClipID(),
			WorkspaceID:  workspaceID,
			ProjectID:    projectID,
			SourceKind:   row.GetSourceKind(),
			MediaType:    row.GetMediaType(),
			Status:       media.MediaStatusReady,
			Title:        title,
			Description:  "",
			DurationSecs: row.GetDuration(),
			MetadataJSON: row.GetMetadata(),
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := i.mediaRepo.CreateItem(ctx, item); err != nil {
			return err
		}

		if row.GetLocalPath() != "" {
			file := &media.File{
				ID:           "",
				MediaItemID:  item.ID,
				LocationKind: media.LocationKindLocal,
				URI:          row.GetLocalPath(),
				Status:       media.MediaStatusReady,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_ = i.mediaRepo.CreateFile(ctx, file)
		}

		if row.GetDriveLink() != "" {
			file := &media.File{
				ID:           "",
				MediaItemID:  item.ID,
				LocationKind: media.LocationKindDrive,
				URI:          row.GetDriveLink(),
				Status:       media.MediaStatusReady,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_ = i.mediaRepo.CreateFile(ctx, file)
		}

		if tags := row.GetTags(); tags != "" {
			tagList := parseTags(tags)
			if len(tagList) > 0 {
				_ = i.mediaRepo.AddTags(ctx, item.ID, tagList)
			}
		}
	}

	return rows.Err()
}

package mediaimport

import (
	"context"
	"database/sql"

	"velox/go-master/internal/core/media"
)

// LegacyClipsImporter wraps the common importer for clips table.
type LegacyClipsImporter struct {
	importer *CommonLegacyImporter
}

func NewLegacyClipsImporter(oldDB *sql.DB, mediaRepo media.Repository) *LegacyClipsImporter {
	return &LegacyClipsImporter{
		importer: NewCommonLegacyImporter(oldDB, mediaRepo),
	}
}

func (i *LegacyClipsImporter) Import(ctx context.Context, workspaceID, projectID string) error {
	return i.importer.Import(ctx, workspaceID, projectID, &ClipsLegacyAdapter{})
}

package mediaimport

import (
	"context"
	"database/sql"

	"velox/go-master/internal/core/media"
)

// LegacyArtlistImporter wraps the common importer for artlist_clips table.
type LegacyArtlistImporter struct {
	importer *CommonLegacyImporter
}

func NewLegacyArtlistImporter(oldDB *sql.DB, mediaRepo media.Repository) *LegacyArtlistImporter {
	return &LegacyArtlistImporter{
		importer: NewCommonLegacyImporter(oldDB, mediaRepo),
	}
}

func (i *LegacyArtlistImporter) Import(ctx context.Context, workspaceID, projectID string) error {
	return i.importer.Import(ctx, workspaceID, projectID, &ArtlistLegacyAdapter{})
}

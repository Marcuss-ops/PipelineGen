package mediaimport

import (
	"context"
	"database/sql"

	"velox/go-master/internal/core/media"
)

// LegacyStockImporter wraps the common importer for stock_clips table.
type LegacyStockImporter struct {
	importer *CommonLegacyImporter
}

func NewLegacyStockImporter(oldDB *sql.DB, mediaRepo media.Repository) *LegacyStockImporter {
	return &LegacyStockImporter{
		importer: NewCommonLegacyImporter(oldDB, mediaRepo),
	}
}

func (i *LegacyStockImporter) Import(ctx context.Context, workspaceID, projectID string) error {
	return i.importer.Import(ctx, workspaceID, projectID, &StockLegacyAdapter{})
}

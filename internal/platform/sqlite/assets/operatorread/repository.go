package operatorread

import (
	"context"
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	"go.uber.org/zap"
)

// InventoryReader is the SQLite implementation of the operator read
// model. It is intentionally read-only and projects rows for the UI.
type InventoryReader struct {
	db  *sql.DB
	log *zap.Logger
}

// NewInventoryReader creates a new read-only inventory reader.
func NewInventoryReader(db *sql.DB, log *zap.Logger) *InventoryReader {
	if log == nil {
		log = zap.NewNop()
	}
	return &InventoryReader{db: db, log: log}
}

// List implements operator.AssetInventoryReader.
func (r *InventoryReader) List(ctx context.Context, query operator.AssetInventoryQuery) (operator.AssetInventoryPage, error) {
	return r.list(ctx, query)
}

// Get implements operator.AssetInventoryReader.
func (r *InventoryReader) Get(ctx context.Context, assetID string) (*operator.AssetInspection, error) {
	return r.get(ctx, assetID)
}

// Facets implements operator.AssetInventoryReader.
func (r *InventoryReader) Facets(ctx context.Context) (*operator.AssetInventoryFacets, error) {
	return r.facets(ctx)
}

// parseTime is a shared helper for the read model.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}

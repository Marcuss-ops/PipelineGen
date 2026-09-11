package media

import (
	"context"
	"database/sql"
	"fmt"
)

// PGAssetLocationExists is the PostgreSQL adapter for the reconciler's
// narrow AssetLocationExists port (P2-11, September 2026). It checks the
// authoritative PG asset_locations table (same SSOT as media_assets).
type PGAssetLocationExists struct{ db *sql.DB }

func NewPGAssetLocationExists(db *sql.DB) *PGAssetLocationExists {
	if db == nil {
		panic("media.NewPGAssetLocationExists: db is required")
	}
	return &PGAssetLocationExists{db: db}
}

func (a *PGAssetLocationExists) Exists(ctx context.Context, assetID, externalID string) (bool, error) {
	if assetID != "" {
		var one int
		err := a.db.QueryRowContext(ctx, `SELECT 1 FROM asset_locations WHERE asset_id = $1 LIMIT 1`, assetID).Scan(&one)
		if err == nil {
			return true, nil
		}
		if err != sql.ErrNoRows {
			return false, fmt.Errorf("pg asset_locations asset_id: %w", err)
		}
	}
	if externalID != "" {
		var one int
		err := a.db.QueryRowContext(ctx, `SELECT 1 FROM asset_locations WHERE external_id = $1 LIMIT 1`, externalID).Scan(&one)
		if err == nil {
			return true, nil
		}
		if err != sql.ErrNoRows {
			return false, fmt.Errorf("pg asset_locations external_id: %w", err)
		}
	}
	return false, nil
}

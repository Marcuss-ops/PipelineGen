// source_write.go: the shared asset-source write helper.
//
// CanonicalIdentityResolver writes provenance rows inside the caller-owned
// transaction that also upserts media_assets, so the SQL lives in exactly one
// helper and the resolver passes either *sql.DB or *sql.Tx through execer.
// The former Ledger adapter (auto-commit registry writes) had no production
// caller and was retired on 2026-11-18; this helper is what canonical identity
// resolution actually uses.
package mediaregistry

import (
	"context"
	"database/sql"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// execer is the narrow write surface shared by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func registerSource(ctx context.Context, e execer, src capregistry.AssetSource) error {
	if err := validateAssetSource(src); err != nil {
		return err
	}
	primary := 0
	if src.IsPrimary {
		primary = 1
	}
	_, err := e.ExecContext(ctx, `
		INSERT INTO media_asset_sources
		(source_id, asset_id, content_sha256, source_type, source_uri,
		 source_version, discovered_at, is_primary)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id) DO UPDATE SET
			content_sha256 = excluded.content_sha256,
			source_version = excluded.source_version,
			discovered_at  = excluded.discovered_at,
			is_primary     = excluded.is_primary`,
		src.SourceID, src.AssetID, src.ContentSHA256, src.SourceType, src.SourceURI,
		src.SourceVersion, src.DiscoveredAt, primary)
	if err != nil {
		return fmt.Errorf("register asset source %q: %w", src.SourceID, err)
	}
	return nil
}

func validateAssetSource(src capregistry.AssetSource) error {
	if src.SourceID == "" || src.AssetID == "" || src.SourceType == "" || src.SourceURI == "" || src.DiscoveredAt == "" {
		return fmt.Errorf("%w: source_id, asset_id, source_type, source_uri and discovered_at are required", capregistry.ErrAssetSourceInvalid)
	}
	return nil
}

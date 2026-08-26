package providermetadata

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"go.uber.org/zap"
)

// AssetMetadataRepository is the SQLite-backed concrete for reading and
// writing asset provider metadata and source-separated tags.
type AssetMetadataRepository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewAssetMetadataRepository creates a new AssetMetadataRepository.
func NewAssetMetadataRepository(db *sql.DB, log *zap.Logger) *AssetMetadataRepository {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetMetadataRepository{db: db, log: log}
}

var _ assets.ProviderMetadataRepository = (*AssetMetadataRepository)(nil)
var _ assets.AssetTagRepository = (*AssetMetadataRepository)(nil)

// UpsertProviderMetadata inserts or replaces the structured provider
// metadata row for an asset. The updated_at column is refreshed on
// every write.
func (r *AssetMetadataRepository) UpsertProviderMetadata(ctx context.Context, meta assets.ProviderMetadata) error {
	if meta.AssetID == "" {
		return fmt.Errorf("AssetMetadataRepository.UpsertProviderMetadata: AssetID is required")
	}
	if meta.Provider == "" {
		return fmt.Errorf("AssetMetadataRepository.UpsertProviderMetadata: Provider is required")
	}
	if meta.ExternalID == "" {
		return fmt.Errorf("AssetMetadataRepository.UpsertProviderMetadata: ExternalID is required")
	}

	const stmt = `
		INSERT INTO asset_provider_metadata (
			asset_id, provider, external_id, creator, country, location,
			collection_id, collection_title, page_url, thumbnail_url,
			preview_url, license_class, provider_metadata_hash, raw_metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id) DO UPDATE SET
			provider = excluded.provider,
			external_id = excluded.external_id,
			creator = excluded.creator,
			country = excluded.country,
			location = excluded.location,
			collection_id = excluded.collection_id,
			collection_title = excluded.collection_title,
			page_url = excluded.page_url,
			thumbnail_url = excluded.thumbnail_url,
			preview_url = excluded.preview_url,
			license_class = excluded.license_class,
			provider_metadata_hash = excluded.provider_metadata_hash,
			raw_metadata_json = excluded.raw_metadata_json,
			updated_at = datetime('now')
	`
	_, err := r.db.ExecContext(ctx, stmt,
		meta.AssetID, meta.Provider, meta.ExternalID, meta.Creator,
		meta.Country, meta.Location, meta.CollectionID, meta.CollectionTitle,
		meta.PageURL, meta.ThumbnailURL, meta.PreviewURL, meta.LicenseClass,
		meta.ProviderMetadataHash, nullString(meta.RawMetadataJSON),
	)
	if err != nil {
		return fmt.Errorf("AssetMetadataRepository.UpsertProviderMetadata: %w", err)
	}
	return nil
}

// GetProviderMetadata returns the provider metadata for an asset, or
// nil if none exists.
func (r *AssetMetadataRepository) GetProviderMetadata(ctx context.Context, assetID string) (*assets.ProviderMetadata, error) {
	if assetID == "" {
		return nil, fmt.Errorf("AssetMetadataRepository.GetProviderMetadata: AssetID is required")
	}

	const stmt = `
		SELECT asset_id, provider, external_id, creator, country, location,
		       collection_id, collection_title, page_url, thumbnail_url,
		       preview_url, license_class, provider_metadata_hash,
		       COALESCE(raw_metadata_json, '') AS raw_metadata_json,
		       fetched_at, updated_at
		FROM asset_provider_metadata
		WHERE asset_id = ?
	`
	row := r.db.QueryRowContext(ctx, stmt, assetID)
	var m assets.ProviderMetadata
	var raw string
	err := row.Scan(
		&m.AssetID, &m.Provider, &m.ExternalID, &m.Creator, &m.Country,
		&m.Location, &m.CollectionID, &m.CollectionTitle, &m.PageURL,
		&m.ThumbnailURL, &m.PreviewURL, &m.LicenseClass, &m.ProviderMetadataHash,
		&raw, &m.FetchedAt, &m.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("AssetMetadataRepository.GetProviderMetadata: %w", err)
	}
	m.RawMetadataJSON = raw
	return &m, nil
}

// ReplaceTagsBySource atomically replaces all tags for an asset that
// belong to a given source, leaving other sources untouched. This is
// the canonical way to keep provider, semantic, manual, etc. tags
// separated.
func (r *AssetMetadataRepository) ReplaceTagsBySource(ctx context.Context, assetID string, source assets.TagSource, tags []assets.AssetTag) error {
	if assetID == "" {
		return fmt.Errorf("AssetMetadataRepository.ReplaceTagsBySource: AssetID is required")
	}
	if !isValidTagSource(source) {
		return fmt.Errorf("AssetMetadataRepository.ReplaceTagsBySource: invalid source %q", source)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("AssetMetadataRepository.ReplaceTagsBySource: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM asset_tags WHERE asset_id = ? AND source = ?", assetID, string(source)); err != nil {
		return fmt.Errorf("AssetMetadataRepository.ReplaceTagsBySource: delete existing: %w", err)
	}

	if len(tags) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO asset_tags (asset_id, tag, normalized_tag, source, confidence, language, created_at)
			VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
		`)
		if err != nil {
			return fmt.Errorf("AssetMetadataRepository.ReplaceTagsBySource: prepare insert: %w", err)
		}
		defer stmt.Close()

		for _, t := range tags {
			normalized := strings.ToLower(strings.TrimSpace(t.NormalizedTag))
			if normalized == "" {
				normalized = strings.ToLower(strings.TrimSpace(t.Tag))
			}
			if normalized == "" {
				continue
			}
			if _, err := stmt.ExecContext(ctx,
				assetID, strings.TrimSpace(t.Tag), normalized, string(source),
				t.Confidence, nullString(t.Language),
			); err != nil {
				return fmt.Errorf("AssetMetadataRepository.ReplaceTagsBySource: insert tag %q: %w", t.Tag, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("AssetMetadataRepository.ReplaceTagsBySource: commit: %w", err)
	}
	return nil
}

// ListTagsByAsset returns every tag for an asset, ordered by source and
// normalized tag.
func (r *AssetMetadataRepository) ListTagsByAsset(ctx context.Context, assetID string) ([]assets.AssetTag, error) {
	if assetID == "" {
		return nil, fmt.Errorf("AssetMetadataRepository.ListTagsByAsset: AssetID is required")
	}
	const stmt = `
		SELECT asset_id, tag, normalized_tag, source, confidence, language, created_at
		FROM asset_tags
		WHERE asset_id = ?
		ORDER BY source, normalized_tag
	`
	rows, err := r.db.QueryContext(ctx, stmt, assetID)
	if err != nil {
		return nil, fmt.Errorf("AssetMetadataRepository.ListTagsByAsset: %w", err)
	}
	defer rows.Close()
	return scanAssetTags(rows)
}

// ListTagsBySource returns tags for an asset filtered by source.
func (r *AssetMetadataRepository) ListTagsBySource(ctx context.Context, assetID string, source assets.TagSource) ([]assets.AssetTag, error) {
	if assetID == "" {
		return nil, fmt.Errorf("AssetMetadataRepository.ListTagsBySource: AssetID is required")
	}
	if !isValidTagSource(source) {
		return nil, fmt.Errorf("AssetMetadataRepository.ListTagsBySource: invalid source %q", source)
	}
	const stmt = `
		SELECT asset_id, tag, normalized_tag, source, confidence, language, created_at
		FROM asset_tags
		WHERE asset_id = ? AND source = ?
		ORDER BY normalized_tag
	`
	rows, err := r.db.QueryContext(ctx, stmt, assetID, string(source))
	if err != nil {
		return nil, fmt.Errorf("AssetMetadataRepository.ListTagsBySource: %w", err)
	}
	defer rows.Close()
	return scanAssetTags(rows)
}

func scanAssetTags(rows *sql.Rows) ([]assets.AssetTag, error) {
	var out []assets.AssetTag
	for rows.Next() {
		var t assets.AssetTag
		var source string
		var confidence sql.NullFloat64
		var language sql.NullString
		if err := rows.Scan(&t.AssetID, &t.Tag, &t.NormalizedTag, &source, &confidence, &language, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan asset tag: %w", err)
		}
		t.Source = assets.TagSource(source)
		t.Confidence = confidenceFloat(confidence)
		if language.Valid {
			t.Language = language.String
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func confidenceFloat(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0
}

func isValidTagSource(s assets.TagSource) bool {
	for _, v := range assets.ValidTagSources {
		if v == s {
			return true
		}
	}
	return false
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

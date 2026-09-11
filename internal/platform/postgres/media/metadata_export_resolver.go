package media

import (
	"context"
	"database/sql"
	"fmt"

	appexport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/metadataexport"
)

// MetadataExportResolver reads media identity and event scope from the
// PostgreSQL media SSOT while retaining delivery history on the operational
// SQLite database. This split prevents exports from silently mixing stale
// media rows with current operational delivery records.
type MetadataExportResolver struct {
	mediaDB    *sql.DB
	operations *sql.DB
}

var _ appexport.AssetResolver = (*MetadataExportResolver)(nil)

// NewMetadataExportResolver constructs the split metadata-export reader.
// mediaDB is authoritative for media assets, locations, provenance, and the
// PostgreSQL media outbox; operations is authoritative only for delivery_log.
func NewMetadataExportResolver(mediaDB, operations *sql.DB) appexport.AssetResolver {
	if mediaDB == nil || operations == nil {
		return nil
	}
	return &MetadataExportResolver{mediaDB: mediaDB, operations: operations}
}

// ResolveAssetIDs resolves explicit IDs or the media outbox scope for a job.
// Event scope is read from PostgreSQL because media events are emitted into
// the PostgreSQL outbox by the canonical media committer.
func (r *MetadataExportResolver) ResolveAssetIDs(ctx context.Context, jobID string, explicitIDs []string) ([]string, error) {
	if len(explicitIDs) > 0 {
		ids := append([]string(nil), explicitIDs...)
		return ids, nil
	}
	if r == nil || r.mediaDB == nil || jobID == "" {
		return nil, nil
	}

	rows, err := r.mediaDB.QueryContext(ctx, `
		SELECT DISTINCT aggregate_id
		FROM outbox_events
		WHERE aggregate_id <> ''
		  AND (payload_json LIKE $1 OR aggregate_id = $2)
		ORDER BY aggregate_id
		LIMIT 500
	`, "%"+jobID+"%", jobID)
	if err != nil {
		return nil, fmt.Errorf("postgres metadata export: resolve job scope %q: %w", jobID, err)
	}
	defer rows.Close()

	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres metadata export: scan job scope: %w", err)
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres metadata export: iterate job scope: %w", err)
	}
	return ids, nil
}

// LoadTechnicalSection reads technical media metadata and the canonical Drive
// location from PostgreSQL. Missing assets are represented as nil, nil.
func (r *MetadataExportResolver) LoadTechnicalSection(ctx context.Context, assetID string) (map[string]any, error) {
	if r == nil || r.mediaDB == nil {
		return nil, fmt.Errorf("postgres metadata export: media reader unavailable")
	}

	var (
		id, source, name, mediaType, category string
		quality                               sql.NullFloat64
		driveFileID, driveLink                sql.NullString
	)
	err := r.mediaDB.QueryRowContext(ctx, `
		SELECT a.id, COALESCE(a.source, ''), COALESCE(a.name, ''),
		       COALESCE(a.media_type, ''), COALESCE(a.category, ''),
		       a.quality_score,
		       COALESCE(l.external_id, ''), COALESCE(l.web_view_link, '')
		FROM media_assets a
		LEFT JOIN asset_locations l
		  ON l.asset_id = a.id AND l.location_kind = 'drive'
		WHERE a.id = $1
		LIMIT 1
	`, assetID).Scan(&id, &source, &name, &mediaType, &category, &quality, &driveFileID, &driveLink)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres metadata export: load technical %q: %w", assetID, err)
	}

	return map[string]any{
		"asset_id":      id,
		"source":        source,
		"name":          name,
		"media_type":    mediaType,
		"category":      category,
		"drive_file_id": driveFileID.String,
		"drive_link":    driveLink.String,
		"quality_score": quality.Float64,
	}, nil
}

// LoadProvenanceSection reads the current source ledger from PostgreSQL.
func (r *MetadataExportResolver) LoadProvenanceSection(ctx context.Context, assetID string) (map[string]any, error) {
	if r == nil || r.mediaDB == nil {
		return nil, fmt.Errorf("postgres metadata export: media reader unavailable")
	}

	var source string
	err := r.mediaDB.QueryRowContext(ctx, `
		SELECT COALESCE(source_type, '')
		FROM media_asset_sources
		WHERE asset_id = $1
		ORDER BY is_primary DESC, discovered_at DESC
		LIMIT 1
	`, assetID).Scan(&source)
	if err == sql.ErrNoRows {
		// The asset itself remains the authoritative fallback when an older
		// row has not yet been copied into the provenance ledger.
		err = r.mediaDB.QueryRowContext(ctx,
			`SELECT COALESCE(source, '') FROM media_assets WHERE id = $1`, assetID).Scan(&source)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres metadata export: load provenance %q: %w", assetID, err)
	}
	return map[string]any{"source": source}, nil
}

// LoadDeliverySection intentionally reads only the operational delivery log
// from SQLite. Delivery history is not media authority and is not moved to
// PostgreSQL merely to simplify this reader.
func (r *MetadataExportResolver) LoadDeliverySection(ctx context.Context, assetID string) ([]any, error) {
	if r == nil || r.operations == nil {
		return nil, fmt.Errorf("postgres metadata export: delivery reader unavailable")
	}

	rows, err := r.operations.QueryContext(ctx, `
		SELECT delivery_id, endpoint_url, status_code, response_hash, delivered_at, note
		FROM delivery_log
		WHERE asset_id = ?
		ORDER BY delivered_at DESC
		LIMIT 50
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("sqlite metadata export: load delivery %q: %w", assetID, err)
	}
	defer rows.Close()

	out := make([]any, 0, 16)
	for rows.Next() {
		var deliveryID, endpointURL, responseHash, deliveredAt, note string
		var statusCode sql.NullInt64
		if err := rows.Scan(&deliveryID, &endpointURL, &statusCode, &responseHash, &deliveredAt, &note); err != nil {
			return nil, fmt.Errorf("sqlite metadata export: scan delivery %q: %w", assetID, err)
		}
		out = append(out, map[string]any{
			"delivery_id":   deliveryID,
			"endpoint_url":  endpointURL,
			"status_code":   statusCode.Int64,
			"response_hash": responseHash,
			"delivered_at":  deliveredAt,
			"note":          note,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite metadata export: iterate delivery %q: %w", assetID, err)
	}
	return out, nil
}

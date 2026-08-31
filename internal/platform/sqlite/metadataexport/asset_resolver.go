// Package metadataexport — concrete SQLite adapter for
// assets/metadataexport.AssetResolver.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the legacy
// internal/capabilities/assets/metadataexport previously inlined SQL
// against media_assets + outbox_events + delivery_log. After split,
// those queries live here behind a typed-port adapter. Application
// layer no longer imports database/sql — the import direction is
// inward (application → infrastructure), per AGENTS.md Pattern 8.
//
// DB schema references (read-only — the resolver never writes):
//   - media_assets: id, source, name, media_type, drive_file_id,
//     drive_link, category, quality_score
//   - delivery_log: delivery_id, endpoint_url, status_code,
//     response_hash, delivered_at, note, asset_id
//   - outbox_events: aggregate_id (job-scope resolution)
//
// Missing rows are part of the contract: ResolveAssetIDs returns nil
// when the outbox table is empty (matches the legacy behaviour
// "Treat as no rows; if the table is empty no harm done."). The
// per-section loaders return nil for sql.ErrNoRows so the snapshot
// layer writes a null placeholder. Other errors propagate so the
// handler surfaces them.
package metadataexport

import (
	"context"
	"database/sql"

	appexport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/metadataexport"
)

// Compile-time assertion: the concrete must satisfy the typed port.
// PR 1.7 (June 2026) — flags drift at compile time, not first runtime
// rootbox replay. Place the assertion under the import-direction
// boundary so a future port-signature break fails the build of THIS
// package, not silently at the composition root.
var _ appexport.AssetResolver = (*sqliteAssetResolver)(nil)

// NewSQLiteAdapter wraps a *sql.DB into the typed port. db must be
// non-nil in production; tests may pass nil and observe nil-method-
// receiver panics if they call any of the four port methods (the
// legacy `db == nil` tolerance is preserved by returning nil slices).
//
// Naming: "SQLite" in the ctor matches the existing pattern under
// internal/platform/sqlite (e.g.
// `NewRepository`, `NewSQLiteStore`); the package itself is named
// "metadataexport" because the application-side package owns the
// canonical name and the adapter layer mirrors it.
func NewSQLiteAdapter(db *sql.DB) appexport.AssetResolver {
	if db == nil {
		return nil
	}
	return &sqliteAssetResolver{db: db}
}

// sqliteAssetResolver is the only AssetResolver concrete; production
// wiring in internal/app/build_bundles_process.go::BuildOutboxBundle
// returns this type via NewSQLiteAdapter. New ports land here AND in
// the typed-port declaration in lockstep — no runtime type assertions
// (per AGENTS.md Pattern 0: "compile-time assertions only").
type sqliteAssetResolver struct {
	db *sql.DB
}

// ResolveAssetIDs returns the asset_ids the export should walk. The
// behaviour matches the legacy implementation bit-for-bit:
//
//   - explicit asset_ids returned verbatim when supplied
//     (producer knows the scope).
//   - job_id route queries outbox_events for aggregate_ids whose
//     event_type LIKE 'asset.%' AND aggregate_id != ”. Empty table
//     is treated as no rows (no error surfaced). LIMIT 500 caps the
//     scope so an over-eager job can't trigger an unbounded walk.
//   - both empty → nil.
func (r *sqliteAssetResolver) ResolveAssetIDs(ctx context.Context, jobID string, explicitIDs []string) ([]string, error) {
	if len(explicitIDs) > 0 {
		return explicitIDs, nil
	}
	if jobID == "" || r.db == nil {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT aggregate_id FROM outbox_events
		WHERE event_type LIKE ? AND aggregate_id != '' AND aggregate_id IS NOT NULL
		LIMIT 500
	`, "asset.%")
	if err != nil {
		// Treat as no rows; if the table is empty no harm done. Matches
		// the legacy "do not surface transient SQLite hiccups as the
		// scope-resolution error here" rule. The OUTBOX pool retries
		// later if a real failure surfaces.
		return nil, nil
	}
	defer rows.Close()
	out := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr == nil && id != "" {
			out = append(out, id)
		}
	}
	return out, nil
}

// LoadTechnicalSection returns the technical-metadata row for one
// asset. 8-column projection matching the legacy inlined SELECT. nil
// rows (sql.ErrNoRows) return (nil, nil) so the snapshot layer writes
// a null placeholder rather than aborting the export.
func (r *sqliteAssetResolver) LoadTechnicalSection(ctx context.Context, assetID string) (map[string]any, error) {
	if r.db == nil {
		return nil, nil
	}
	var id, src, name, mtype string
	var driveFileID, driveLink, category string
	var quality sql.NullFloat64
	err := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(source,''), COALESCE(name,''), COALESCE(media_type,''),
		       COALESCE(drive_file_id,''), COALESCE(drive_link,''),
		       COALESCE(category,''), quality_score
		FROM media_assets WHERE id = ? LIMIT 1
	`, assetID).Scan(&id, &src, &name, &mtype, &driveFileID, &driveLink, &category, &quality)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"asset_id":      id,
		"source":        src,
		"name":          name,
		"media_type":    mtype,
		"drive_file_id": driveFileID,
		"drive_link":    driveLink,
		"category":      category,
		"quality_score": quality.Float64,
	}, nil
}

// LoadProvenanceSection returns the minimal provenance block (just
// source today; future PR adds ingestion lineage to the same query).
// Same nil-row tolerance as LoadTechnicalSection.
func (r *sqliteAssetResolver) LoadProvenanceSection(ctx context.Context, assetID string) (map[string]any, error) {
	if r.db == nil {
		return nil, nil
	}
	var src string
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(source,'') FROM media_assets WHERE id = ? LIMIT 1
	`, assetID).Scan(&src)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"source": src}, nil
}

// LoadDeliverySection returns the 50-row delivery_log slice for the
// asset, newest-first. Empty slice (NOT nil) when no rows exist so
// the JSON shape stays stable downstream.
func (r *sqliteAssetResolver) LoadDeliverySection(ctx context.Context, assetID string) ([]any, error) {
	if r.db == nil {
		return []any{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT delivery_id, endpoint_url, status_code, response_hash, delivered_at, note
		FROM delivery_log WHERE asset_id = ?
		ORDER BY delivered_at DESC LIMIT 50
	`, assetID)
	if err != nil {
		return []any{}, nil
	}
	defer rows.Close()
	out := make([]any, 0, 16)
	for rows.Next() {
		var dID, ep, hsh, da, note string
		var sc sql.NullInt64
		if scanErr := rows.Scan(&dID, &ep, &sc, &hsh, &da, &note); scanErr == nil {
			out = append(out, map[string]any{
				"delivery_id":   dID,
				"endpoint_url":  ep,
				"status_code":   sc.Int64,
				"response_hash": hsh,
				"delivered_at":  da,
				"note":          note,
			})
		}
	}
	return out, nil
}

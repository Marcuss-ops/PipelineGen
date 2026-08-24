// Package assets — repository adapter SQL helpers (Wave C: moved
// from internal/kernel/asset/store_core.go).
//
// The `Repository` interface stays in domain (canonical contract).
// The concrete `assetRepositoryAdapter` struct + `AssetRepository()`
// factory + supporting SQL helpers (`FindByExternalRef`,
// `listAssetsByFilter`) migrate here. The slim
// `internal/kernel/asset/store_core.go` keeps only the `Service`
// type and its interface-resolution helpers.
package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── assetRepositoryAdapter (migrated from store_core.go) ─────────────

type assetRepositoryAdapter struct {
	store *AssetStoreSQLite
}

func (a *assetRepositoryAdapter) Upsert(ctx context.Context, assetItem *asset.Asset) error {
	return a.store.Save(ctx, &asset.Details{Asset: assetItem})
}

func (a *assetRepositoryAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	det, err := a.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if det == nil {
		return nil, nil
	}
	return det.Asset, nil
}

func (a *assetRepositoryAdapter) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	return a.store.listAssetsByFilter(ctx, filter)
}

func (a *assetRepositoryAdapter) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	conds := []string{SoftDeleteFilter()}
	args := []any{}
	if filter.Source != "" {
		conds = append(conds, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.MediaType != "" {
		conds = append(conds, "media_type = ?")
		args = append(args, filter.MediaType)
	}
	if len(filter.States) > 0 {
		phs := make([]string, len(filter.States))
		for i := range filter.States {
			phs[i] = "?"
		}
		conds = append(conds, "lifecycle_state IN ("+strings.Join(phs, ",")+")")
		for _, st := range filter.States {
			args = append(args, st)
		}
	}
	var n int64
	err := a.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_assets WHERE "+strings.Join(conds, " AND "),
		args...).Scan(&n)
	return n, err
}

func (a *assetRepositoryAdapter) SoftDelete(ctx context.Context, id string) error {
	return a.store.Delete(ctx, id)
}

func (a *assetRepositoryAdapter) Restore(ctx context.Context, id string) error {
	nowStr := timeutil.FormatRFC3339(time.Now())
	_, err := a.store.db.ExecContext(ctx,
		"UPDATE media_assets SET lifecycle_state = 'ACTIVE', deleted_at = NULL, updated_at = ? WHERE id = ?",
		nowStr, id)
	return err
}

func (a *assetRepositoryAdapter) HardDelete(ctx context.Context, id string) error {
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_locations WHERE asset_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_processing WHERE asset_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM asset_versions WHERE asset_id = ?", id)
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_assets WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// FindByExternalRef looks up an asset by its external provider
// reference. For google_drive it matches on drive_file_id; for all
// other providers it matches on source + metadata_json.external_id.
func (a *assetRepositoryAdapter) FindByExternalRef(ctx context.Context, provider, externalID string) (*asset.Asset, error) {
	if provider == "" || externalID == "" {
		return nil, nil
	}
	var row *sql.Row
	if provider == "google_drive" {
		query := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE drive_file_id = ? AND " + SoftDeleteFilter() + " LIMIT 1"
		row = a.store.db.QueryRowContext(ctx, query, externalID)
	} else {
		query := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE source = ? AND json_extract(COALESCE(metadata_json,'{}'), '$.external_id') = ? AND " + SoftDeleteFilter() + " LIMIT 1"
		row = a.store.db.QueryRowContext(ctx, query, provider, externalID)
	}
	assetItem, err := ScanCanonicalAssetRowPublic(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("FindByExternalRef(%s, %s): %w", provider, externalID, err)
	}
	return assetItem, nil
}

// AssetRepository returns a Repository adapter backed by the LOCAL
// store. Caller-side promotion to legacy *asset.AssetStoreSQLite (via
// HYBRID embed) keeps backward compatibility for composition roots
// that haven't migrated yet.
func (s *AssetStoreSQLite) AssetRepository() asset.Repository {
	return &assetRepositoryAdapter{store: s}
}

// Repository returns the canonical Repository adapter, satisfying
// the asset.assetStoreAdapter interface (which expects Repository(),
// not AssetRepository()).
//
// Step 1 fix (June 2026): the type-assertion in
// asset.Service.Repository() casts s.store.(assetStoreAdapter) and
// calls a.Repository(). Before this shadow method, AssetStoreSQLite
// only exposed AssetRepository() — the interface match failed
// silently (typed-nil), causing "AssetRepo is required but not wired"
// at every YouTube service composition.
func (s *AssetStoreSQLite) Repository() asset.Repository {
	return s.AssetRepository()
}

// ── listAssetsByFilter helper ────────────────────────────────────────

func (s *AssetStoreSQLite) listAssetsByFilter(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	cond := SoftDeleteFilter()
	args := []any{}

	if filter.Source != "" {
		cond += " AND source = ?"
		args = append(args, filter.Source)
	}
	if filter.MediaType != "" {
		cond += " AND media_type = ?"
		args = append(args, filter.MediaType)
	}
	if len(filter.States) > 0 {
		phs := make([]string, len(filter.States))
		for i := range filter.States {
			phs[i] = "?"
		}
		cond += " AND lifecycle_state IN (" + strings.Join(phs, ",") + ")"
		for _, st := range filter.States {
			args = append(args, st)
		}
	}
	if len(filter.IDs) > 0 {
		phs := make([]string, len(filter.IDs))
		for i := range filter.IDs {
			phs[i] = "?"
		}
		cond += " AND id IN (" + strings.Join(phs, ",") + ")"
		for _, id := range filter.IDs {
			args = append(args, id)
		}
	}
	if len(filter.ExcludeIDs) > 0 {
		phs := make([]string, len(filter.ExcludeIDs))
		for i := range filter.ExcludeIDs {
			phs[i] = "?"
		}
		cond += " AND id NOT IN (" + strings.Join(phs, ",") + ")"
		for _, id := range filter.ExcludeIDs {
			args = append(args, id)
		}
	}
	if filter.IsFolder != nil {
		cond += " AND is_folder = ?"
		v := 0
		if *filter.IsFolder {
			v = 1
		}
		args = append(args, v)
	}
	if filter.Category != "" {
		cond += " AND category = ?"
		args = append(args, filter.Category)
	}
	if filter.Group != "" {
		cond += " AND group_name = ?"
		args = append(args, filter.Group)
	}

	query := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE " + cond + " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += " OFFSET ?"
			args = append(args, filter.Offset)
		}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("assets.listAssetsByFilter: %w", err)
	}
	defer rows.Close()

	out := make([]*asset.Asset, 0)
	for rows.Next() {
		a, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, fmt.Errorf("assets.listAssetsByFilter scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Package assets — canonical AssetStoreSQLite struct + read surface.
//
// The store is a repository/read adapter. Durable writes are delegated to the
// canonical asset writer wired by composition; there is deliberately no SQL
// fallback in this file.
package imagesregistry

import (
	"context"
	"database/sql"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// ── AssetStoreSQLite (canonical Wave A receiver) ────────────────────

type AssetStoreSQLite struct {
	db              *sql.DB
	log             *zap.Logger
	canonicalSave   func(context.Context, *asset.Details) error
	canonicalDelete func(context.Context, string) error

	// batchCache is the LRU-style cache for BatchGetByIDs (search
	// hydration). Lazily initialised on first BatchGetByIDs call.
	batchCache *batchCache
}

func (s *AssetStoreSQLite) SetCanonicalSave(fn func(context.Context, *asset.Details) error) {
	if s != nil {
		s.canonicalSave = fn
	}
}

// SetCanonicalDelete wires the canonical lifecycle mutation. Delete remains
// on the legacy Store interface during cutover, but it no longer owns SQL.
func (s *AssetStoreSQLite) SetCanonicalDelete(fn func(context.Context, string) error) {
	if s != nil {
		s.canonicalDelete = fn
	}
}

// NewAssetStoreSQLite creates a new Wave A AssetStoreSQLite with
// the given database connection and logger (nil-safe).
func NewAssetStoreSQLite(db *sql.DB, log *zap.Logger) *AssetStoreSQLite {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetStoreSQLite{db: db, log: log}
}

// ── canonical Get / delegated mutations / List ─────────────────────

// Get retrieves a non-tombstoned asset by id, populated via the
// canonical MediaAssetColumns projection in store_helpers.go and the
// canonical ScanCanonicalAssetRowPublic in scan_helpers.go.
//
// Returns (nil, nil) when the asset does not exist (callers tolerate
// lookups and treat (nil,nil) as "not found").
func (s *AssetStoreSQLite) Get(ctx context.Context, id string) (*asset.Details, error) {
	if id == "" {
		return nil, nil
	}
	query := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE " + SoftDeleteFilter() + " AND id = ? LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, id)
	a, err := ScanCanonicalAssetRowPublic(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assets.Get: %w", err)
	}
	return &asset.Details{Asset: a}, nil
}

// Save is retained only as a compatibility surface while callers migrate to
// persistence.AssetCommitter. It MUST delegate to the composition-wired
// canonical writer; absence of that writer is a boot/wiring defect and fails
// closed instead of opening a second SQL path.
func (s *AssetStoreSQLite) Save(ctx context.Context, details *asset.Details) error {
	if details == nil || details.Asset == nil {
		return fmt.Errorf("assets.Save: nil details or asset")
	}
	if s == nil || s.canonicalSave == nil {
		return fmt.Errorf("assets.Save: canonical AssetCommitter is required; repository SQL fallback has been removed")
	}
	return s.canonicalSave(ctx, details)
}

// Delete is retained only for the legacy Store contract. The mutation itself
// belongs to the canonical asset writer and is never performed by this
// repository.
func (s *AssetStoreSQLite) Delete(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("assets.Delete: asset id is required")
	}
	if s == nil || s.canonicalDelete == nil {
		return fmt.Errorf("assets.Delete: canonical asset mutator is required; repository SQL fallback has been removed")
	}
	return s.canonicalDelete(ctx, id)
}

// List returns canonical asset summaries matching the supplied
// filter. Implements the same projection as the legacy struct's List.
func (s *AssetStoreSQLite) List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error) {
	args := []any{}
	conds := []string{SoftDeleteFilter()}

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
	if len(filter.IDs) > 0 {
		phs := make([]string, len(filter.IDs))
		for i := range filter.IDs {
			phs[i] = "?"
		}
		conds = append(conds, "id IN ("+strings.Join(phs, ",")+")")
		for _, id := range filter.IDs {
			args = append(args, id)
		}
	}
	if len(filter.ExcludeIDs) > 0 {
		phs := make([]string, len(filter.ExcludeIDs))
		for i := range filter.ExcludeIDs {
			phs[i] = "?"
		}
		conds = append(conds, "id NOT IN ("+strings.Join(phs, ",")+")")
		for _, id := range filter.ExcludeIDs {
			args = append(args, id)
		}
	}
	if filter.IsFolder != nil {
		conds = append(conds, "is_folder = ?")
		v := 0
		if *filter.IsFolder {
			v = 1
		}
		args = append(args, v)
	}

	query := "SELECT id, COALESCE(source,''), COALESCE(name,''), COALESCE(filename,''), " +
		"COALESCE(media_type,''), COALESCE(category,''), COALESCE(lifecycle_state,'ACTIVE'), " +
		"created_at, COALESCE(updated_at,'') " +
		"FROM media_assets WHERE " + strings.Join(conds, " AND ") +
		" ORDER BY created_at DESC"
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
		return nil, fmt.Errorf("assets.List: %w", err)
	}
	defer rows.Close()

	var out []*asset.Summary
	for rows.Next() {
		var sum asset.Summary
		var sourceStr, nameStr, filenameStr, mediaTypeStr, categoryStr, lifecycleStr sql.NullString
		var createdAtStr, updatedAtStr sql.NullString
		if err := rows.Scan(
			&sum.ID, &sourceStr, &nameStr, &filenameStr,
			&mediaTypeStr, &categoryStr, &lifecycleStr,
			&createdAtStr, &updatedAtStr,
		); err != nil {
			return nil, fmt.Errorf("assets.List scan: %w", err)
		}
		sum.Source = asset.Source(sourceStr.String)
		sum.Name = nameStr.String
		sum.Filename = filenameStr.String
		sum.MediaType = asset.MediaType(mediaTypeStr.String)
		sum.Category = categoryStr.String
		sum.LifecycleState = asset.LifecycleState(lifecycleStr.String)
		if createdAtStr.Valid {
			sum.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
		}
		if updatedAtStr.Valid {
			sum.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr.String)
		}
		out = append(out, &sum)
	}
	return out, rows.Err()
}

// NewService is the canonical surface for constructing the high-level asset
// Service. The Service remains in the domain package; persistence writes are
// injected separately through the canonical writer callbacks above.
func NewService(store *AssetStoreSQLite, log *zap.Logger) *detail.Service {
	if log == nil {
		log = zap.NewNop()
	}
	return detail.NewService(store, log)
}

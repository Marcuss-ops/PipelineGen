package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// assetFilterPredicates builds the shared WHERE clause + positional arguments
// for every media asset listing/count, so the summary projection, the
// full-asset projection and the count cannot drift apart.
//
// The predicate set mirrors the legacy SQLite repository exactly (source,
// media_type, lifecycle states, id include/exclude, category, group). `IsFolder`
// is a legacy SQLite media-plane column with no PostgreSQL counterpart and is
// therefore not applied; `HasEmbedding`/`WorkspaceID`/`IsAdmin` are ignored by
// the legacy repository too, so ignoring them preserves behaviour.
func assetFilterPredicates(filter asset.Filter) (string, []any) {
	conds := []string{"lifecycle_state <> 'DELETED'"}
	args := []any{}

	addEq := func(column, value string) {
		if value == "" {
			return
		}
		args = append(args, value)
		conds = append(conds, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	addEq("source", filter.Source)
	addEq("media_type", filter.MediaType)
	addEq("category", filter.Category)
	addEq("group_name", filter.Group)

	addSet := func(column string, values []string, negated bool) {
		if len(values) == 0 {
			return
		}
		placeholders := make([]string, 0, len(values))
		for _, value := range values {
			args = append(args, value)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		operator := "IN"
		if negated {
			operator = "NOT IN"
		}
		conds = append(conds, column+" "+operator+" ("+strings.Join(placeholders, ", ")+")")
	}
	addSet("lifecycle_state", filter.States, false)
	addSet("id", filter.IDs, false)
	addSet("id", filter.ExcludeIDs, true)

	return strings.Join(conds, " AND "), args
}

// PostgresAssetStore is the PostgreSQL media-SSOT implementation of the
// consumer-facing asset read contract used by the admin console and the
// operator API: `Get(ctx, id) (*asset.Details, error)`,
// `List(ctx, filter) ([]*asset.Summary, error)` and
// `Count(ctx, filter) (int64, error)`.
//
// It is the PostgreSQL counterpart of the SQLite `*detail.Service` (over
// imagesregistry.AssetStoreSQLite): ONE read contract, two engine
// implementations, selected by composition. Mirroring the legacy filter
// semantics is deliberate — a caller must not be able to tell which engine
// backs it except by the data it sees.
//
// It is READ-ONLY by construction: media mutations stay owned by
// *PostgresMediaCommitter, so this type cannot become a second media writer.
type PostgresAssetStore struct {
	reader *MediaSearcher
}

// NewPostgresAssetStore wires the read store over the media read repository.
// A nil reader returns nil so composition can treat "no media SSOT" as the
// documented degrade signal instead of a half-wired store.
func NewPostgresAssetStore(reader *MediaSearcher) *PostgresAssetStore {
	if reader == nil {
		return nil
	}
	return &PostgresAssetStore{reader: reader}
}

// Get returns the canonical asset plus its location projection for one id,
// delegating to the single hydration site. A missing row returns (nil, nil),
// the same contract as detail.Service.Get.
func (s *PostgresAssetStore) Get(ctx context.Context, id string) (*asset.Details, error) {
	if s == nil || s.reader == nil {
		return nil, errors.New("postgres media: asset store not wired")
	}
	return s.reader.GetAssetDetails(ctx, id)
}

// List returns the canonical asset summaries matching the filter. The
// projection, predicates and ordering mirror the legacy SQLite List exactly.
func (s *PostgresAssetStore) List(ctx context.Context, filter asset.Filter) ([]*asset.Summary, error) {
	if s == nil || s.reader == nil || s.reader.db == nil {
		return nil, errors.New("postgres media: asset store not wired")
	}

	where, args := assetFilterPredicates(filter)
	query := "SELECT id, COALESCE(source, ''), COALESCE(name, ''), COALESCE(filename, ''), " +
		"COALESCE(media_type, ''), COALESCE(category, ''), COALESCE(lifecycle_state, 'ACTIVE'), " +
		"COALESCE(created_at, ''), COALESCE(updated_at, '') " +
		"FROM media_assets WHERE " + where +
		" ORDER BY created_at DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
		if filter.Offset > 0 {
			args = append(args, filter.Offset)
			query += fmt.Sprintf(" OFFSET $%d", len(args))
		}
	}

	rows, err := s.reader.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media: list assets: %w", err)
	}
	defer rows.Close()

	var out []*asset.Summary
	for rows.Next() {
		var summary asset.Summary
		var source, name, filename, mediaType, category, lifecycleState, createdAt, updatedAt sql.NullString
		if err := rows.Scan(
			&summary.ID, &source, &name, &filename,
			&mediaType, &category, &lifecycleState,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres media: list assets scan: %w", err)
		}
		summary.Source = asset.Source(source.String)
		summary.Name = name.String
		summary.Filename = filename.String
		summary.MediaType = asset.MediaType(mediaType.String)
		summary.Category = category.String
		summary.LifecycleState = asset.LifecycleState(lifecycleState.String)
		if createdAt.Valid {
			summary.CreatedAt = parseRFC3339(createdAt.String)
		}
		if updatedAt.Valid {
			summary.UpdatedAt = parseRFC3339(updatedAt.String)
		}
		out = append(out, &summary)
	}
	return out, rows.Err()
}

// Count returns the number of canonical assets matching the filter, using the
// same predicates and soft-delete exclusion as List (pagination is ignored,
// exactly like the legacy asset repository Count).
func (s *PostgresAssetStore) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	if s == nil || s.reader == nil || s.reader.db == nil {
		return 0, errors.New("postgres media: asset store not wired")
	}
	where, args := assetFilterPredicates(filter)
	var n int64
	if err := s.reader.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE "+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres media: count assets: %w", err)
	}
	return n, nil
}

// MediaClipAssetReader adapts the PostgreSQL media read repository to the clips
// capability's consumer-owned read contract: asset by id, filtered asset list,
// count.
//
// It exists as a distinct type (rather than reusing PostgresAssetStore) because
// the admin/operator surface reads `*asset.Details` while the clips use cases
// read `*asset.Asset`; one type cannot honestly serve both signatures. Both sit
// on the SAME *MediaSearcher over the SAME media SSOT — no second media read
// registry is created.
type MediaClipAssetReader struct {
	reader *MediaSearcher
}

// NewMediaClipAssetReader wires the clips read adapter over the media read
// repository. A nil reader returns nil (degrade signal).
func NewMediaClipAssetReader(reader *MediaSearcher) *MediaClipAssetReader {
	if reader == nil {
		return nil
	}
	return &MediaClipAssetReader{reader: reader}
}

// Get returns the canonical kernel asset for one id. A missing row returns
// (nil, nil), the same contract as the legacy detail.Repository.Get.
func (r *MediaClipAssetReader) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if r == nil || r.reader == nil {
		return nil, errors.New("postgres media: clip asset reader not wired")
	}
	return r.reader.GetClip(ctx, id)
}

// List returns the canonical kernel assets matching the filter, mirroring the
// legacy detail.Repository.List projection and ordering.
func (r *MediaClipAssetReader) List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error) {
	if r == nil || r.reader == nil || r.reader.db == nil {
		return nil, errors.New("postgres media: clip asset reader not wired")
	}

	where, args := assetFilterPredicates(filter)
	query := "SELECT " + mediaAssetReadColumns + " FROM media_assets WHERE " + where + " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
		if filter.Offset > 0 {
			args = append(args, filter.Offset)
			query += fmt.Sprintf(" OFFSET $%d", len(args))
		}
	}

	rows, err := r.reader.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media: list clip assets: %w", err)
	}
	defer rows.Close()

	out := make([]*asset.Asset, 0)
	for rows.Next() {
		rec, err := scanMediaAssetRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: list clip assets scan: %w", err)
		}
		out = append(out, rec.HydrateAsset())
	}
	return out, rows.Err()
}

// Count returns the number of canonical assets matching the filter.
func (r *MediaClipAssetReader) Count(ctx context.Context, filter asset.Filter) (int64, error) {
	if r == nil || r.reader == nil || r.reader.db == nil {
		return 0, errors.New("postgres media: clip asset reader not wired")
	}
	where, args := assetFilterPredicates(filter)
	var n int64
	if err := r.reader.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE "+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres media: count clip assets: %w", err)
	}
	return n, nil
}

// Compile-time self-check: the adapter must always satisfy the very shape the
// clips capability declares, so a signature drift fails the build here rather
// than at the composition root.
var _ interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	List(ctx context.Context, filter asset.Filter) ([]*asset.Asset, error)
	Count(ctx context.Context, filter asset.Filter) (int64, error)
} = (*MediaClipAssetReader)(nil)

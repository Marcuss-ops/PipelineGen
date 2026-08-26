// Package assets — search SQL queries (Wave C: moved from
// internal/kernel/asset/search_core.go).
//
// AdvancedSearchRequest/AdvancedSearchResult/SearchRequest/SearchResult/
// Searcher/EntityExtraction* types stay in domain (canonical contracts).
// The 4 SQL receivers migrate here.
package imagesregistry

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	sqlutil "github.com/Marcuss-ops/PipelineGen/pkg/sqlutil"
)

// SearchableLifecycleFilter returns the canonical SQL fragment that
// restricts search results to the two lifecycle states that clients
// are allowed to see: ACTIVE and PUBLISHED.
//
// PR-QDRANT-SEARCH-LIFECYCLE-FILTER (T5 fix): the previous search
// path used SoftDeleteFilter() which only excluded terminal DELETED
// assets. Assets in DELETE_REQUESTED, DRIVE_DELETE_PENDING,
// DRIVE_DELETED, INDEX_DELETE_PENDING, INDEX_DELETED, PREPARING,
// STAGING, PROCESSING, ERROR all leaked into search results.
// The Qdrant path (CompileQdrantFilter) correctly restricts to
// ACTIVE-only; this function brings the local SQLite path into
// parity with the canonical SearchableLifecycleStates allowlist
// declared at internal/capabilities/assets/search/ports.go.
//
// godlike/06 SSOT: SoftDeleteFilter() (lifecycle_state != 'DELETED')
// is the correct general-purpose exclusion for CRUD operations
// (Get, List, Resolve, Dedup). Search is a STRICTER surface —
// only searchable states should surface. The two filters are
// intentionally different and coexist.
func SearchableLifecycleFilter() string {
	return "lifecycle_state IN ('ACTIVE', 'PUBLISHED')"
}

// ClassifiedAssetFilter returns the canonical SQL fragment that excludes
// unclassified assets (empty asset_kind) from the search surface.
//
// PR-PLANNER-LEAKAGE-CLEANUP (August 2026): StockRust/one-off test clips
// were committed to media_assets with source=stock + PUBLISHED but empty
// asset_kind (they bypassed the canonical MediaCommitter taxonomy). Those
// rows are NOT semantic-searchable — they carry a test-marker embedding and
// empty search_text — and leaked into catalog search ahead of real clips at
// the 0.1 RRF score tie. The canonical taxonomy gate (asset_kind != ”) is
// the single exclusion signal shared with the Qdrant filter compiler's
// must_not:is_empty(asset_kind) clause.
//
// godlike/06 SSOT: this fragment is the local (SQLite) owner of the
// classified-asset boundary; the Qdrant owner is
// internal/platform/qdrant/search/filter_compiler.go. Both MUST
// exclude empty asset_kind so the two catalog-search backends agree.
func ClassifiedAssetFilter() string {
	return "COALESCE(asset_kind, '') != ''"
}

// buildSearchableMediaAssetQuery mirrors buildMediaAssetQuery but uses
// SearchableLifecycleFilter (the stricter ACTIVE-only+PUBLISHED filter)
// instead of SoftDeleteFilter. Used exclusively by the 3 search functions
// (SearchClips, SearchClipsByKeywords, SearchStockByKeywords) so search
// results never include DELETED/DELETE_REQUESTED/DRIVE_DELETE_PENDING/...
// assets.
//
// godlike/06 SSOT: buildMediaAssetQuery stays as-is for non-search
// paths (Get, List, Resolve, Dedup) — those paths correctly use
// SoftDeleteFilter().
func buildSearchableMediaAssetQuery(source string) string {
	q := "SELECT " + MediaAssetColumns + " FROM media_assets WHERE " + SearchableLifecycleFilter()
	if source != "" && source != "all" && source != "unified" {
		q += " AND source = ?"
	}
	return q
}

// allowedSortColumns maps user-facing sort keys to the canonical
// media_assets column name used in the ORDER BY clause. Unknown keys
// are rejected (ErrInvalidSortColumn) instead of silently falling back
// to created_at, so a typo in an API client is surfaced at request
// time rather than silently producing wrong-but-valid results.
var allowedSortColumns = map[string]string{
	"duration": "duration_ms",
	"name":     "name",
	"source":   "source",
}

// ErrInvalidSortColumn is returned when req.SortBy is not in
// allowedSortColumns.
var ErrInvalidSortColumn = fmt.Errorf("invalid sort column: not in allowedSortColumns")

// resolveSortColumn returns the canonical media_assets column name for
// the given user-facing sort key. An empty key returns the default
// ("created_at") for backward compatibility; an unknown non-empty key
// returns an error.
func resolveSortColumn(key string) (string, error) {
	if key == "" {
		return "created_at", nil
	}
	col, ok := allowedSortColumns[key]
	if !ok {
		return "", fmt.Errorf("%w: %q (allowed: duration, name, source)", ErrInvalidSortColumn, key)
	}
	return col, nil
}

// resolveSortDir returns "ASC" or "DESC".
func resolveSortDir(asc bool) string {
	if asc {
		return "ASC"
	}
	return "DESC"
}

// ── SQL receivers (migrated from search_core.go) ─────────────────────

// SearchClips searches clips by tag or name.
//
// Layered strategy:
//
//  1. Fast path: indexed clip_search_terms table (O(log n) per term).
//  2. Fallback: LIKE on tags, name and richer metadata fields (AND
//     semantics).
//  3. Recursive fallback: OR semantics when AND yields zero results.
//
// Results are scored by keyword match quality, quality score,
// duplicate penalty and sponsor penalty (see scoreClips).
//
// Package layout: related but distinct search paths live in dedicated
// files:
//
//   - search_queries.go — SearchClips + SearchClipsAdvanced (this file).
//   - search_terms_queries.go — SearchByTerms + fetchClipsByIDs
//     (indexed lookup).
//   - search_queries.go SearchStockByKeywords (source='stock' shortcut).
//   - clip_list_queries.go — list ops + LastUpdatedAtForTerm.
func (s *AssetStoreSQLite) SearchClips(ctx context.Context, source, tag string) ([]*asset.Asset, error) {
	keywords := strings.Fields(tag)
	if len(keywords) == 0 {
		keywords = []string{tag}
	}

	// Fast path: use indexed search terms table.
	clips, err := s.SearchByTerms(ctx, source, keywords, 50)
	if err == nil && len(clips) > 0 {
		scored := detail.ScoreClips(clips, keywords)
		return scored, nil
	}

	// Fallback: LIKE on tags, name, and richer metadata fields.
	columns := clipSearchColumns()
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*asset.Asset{}, nil
	}

	query := buildSearchableMediaAssetQuery(source) + " AND (" + conditionSQL + ")"

	finalArgs := []any{}
	if source != "" && source != "all" && source != "unified" {
		finalArgs = append(finalArgs, source)
	}
	finalArgs = append(finalArgs, args...)

	rows, err := s.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, clip)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(results) > 0 {
		return detail.ScoreClips(results, keywords), nil
	}

	if len(keywords) > 1 {
		orCondition, orArgs := sqlutil.BuildFallbackLikeConditionsOR(keywords, columns)
		if orCondition != "" {
			orQuery := buildSearchableMediaAssetQuery(source) + " AND (" + orCondition + ")"
			orFinalArgs := []any{}
			if source != "" && source != "all" && source != "unified" {
				orFinalArgs = append(orFinalArgs, source)
			}
			orFinalArgs = append(orFinalArgs, orArgs...)

			orRows, err := s.db.QueryContext(ctx, orQuery, orFinalArgs...)
			if err != nil {
				return nil, err
			}
			defer orRows.Close()

			for orRows.Next() {
				clip, err := ScanCanonicalAssetRowsPublic(orRows)
				if err != nil {
					return nil, err
				}
				results = append(results, clip)
			}
			if err := orRows.Err(); err != nil {
				return nil, err
			}
		}
	}

	return detail.ScoreClips(results, keywords), nil
}

// SearchClipsByKeywords searches clips by keywords using LIKE on the
// media_assets table.
func (s *AssetStoreSQLite) SearchClipsByKeywords(ctx context.Context, source string, keywords []string, limit int) ([]*asset.Asset, error) {
	if len(keywords) == 0 {
		return []*asset.Asset{}, nil
	}

	columns := clipSearchColumns()
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*asset.Asset{}, nil
	}

	query := fmt.Sprintf("%s AND (%s) LIMIT ?", buildSearchableMediaAssetQuery(source), conditionSQL)
	finalArgs := []any{}
	if source != "" && source != "all" && source != "unified" {
		finalArgs = append(finalArgs, source)
	}
	finalArgs = append(finalArgs, args...)
	finalArgs = append(finalArgs, limit)

	rows, err := s.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClipsAdvanced searches clips with structured filters.
func (s *AssetStoreSQLite) SearchClipsAdvanced(ctx context.Context, req detail.AdvancedSearchRequest) (*detail.AdvancedSearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Limit > 500 {
		req.Limit = 500
	}

	// PR-QDRANT-SEARCH-LIFECYCLE-FILTER (T5 fix): use the stricter
	// SearchableLifecycleFilter instead of SoftDeleteFilter so search
	// results never include DELETED/DELETE_REQUESTED/DRIVE_* states.
	conditions := []string{SearchableLifecycleFilter()}
	args := []any{}

	if req.ExcludeUnclassified {
		conditions = append(conditions, ClassifiedAssetFilter())
	}

	if req.Source != "" && req.Source != "all" {
		conditions = append(conditions, "source = ?")
		args = append(args, req.Source)
	}

	if req.Category != "" {
		conditions = append(conditions, "category = ?")
		args = append(args, req.Category)
	}

	if req.Q != "" {
		keywords := strings.Fields(req.Q)
		if len(keywords) > 0 {
			columns := clipSearchColumns()
			cond, kwArgs := sqlutil.BuildFallbackLikeConditionsOR(keywords, columns)
			if cond != "" {
				conditions = append(conditions, "("+cond+")")
				args = append(args, kwArgs...)
			}
		}
	}

	// PR-AGGREGATE-FILTER-UNIFORM (July 2026): Tags filter added.
	// Every listed tag must be present on the `tags` JSON column
	// (AND-semantics, matching the canonical q.Filters.Tags contract
	// from internal/capabilities/assets/search/types.go::Filters.Tags). FTS5
	// is banned per project policy so we route through
	// pkg/sqlutil.BuildFallbackLikeConditions — the single-tag case
	// reduces to a single-column LIKE which the planner can
	// short-circuit when the row is small enough. Coarse substring
	// behaviour on a JSON column is acceptable given the FTS5 ban;
	// production deployments that need exact-token matching should
	// add a `tags_norm` FULL INDEX migration downstream.
	//
	// Forward-pointer (NOT shipped in this commit): a dedicated
	// Language exact-match filter. The media_assets table does not
	// carry a `language` column today (per asset.ScanMediaAsset's
	// 40-column projection in clips_repository.go::MediaAssetColumns),
	// so wiring req.Language reliably requires a schema migration
	// first. Caller-side the filter PASSES THROUGH
	// AdvancedSearchRequest for API symmetry; the SQL filter is
	// deliberately a no-op until the column lands.
	// godlike/07 honest-limitation: this contract is pinned by
	// internal/capabilities/assets/search/cross_provider_test.go::
	// TestFilterLanguageHonestyContract so a future reader / CI
	// gate catches a regression if the projection drifts. WITHOUT
	// the honest test, a casual caller setting q.Filters.Language="en"
	// would see no filter applied AND assume Language filtering works
	// — a hallmarked godlike/07 fake-availability violation.
	if len(req.Tags) > 0 {
		tagCond, tagArgs := sqlutil.BuildFallbackLikeConditions(req.Tags, []string{"tags"})
		if tagCond != "" {
			conditions = append(conditions, "("+tagCond+")")
			args = append(args, tagArgs...)
		}
	}

	if req.MinDuration > 0 {
		conditions = append(conditions, "duration_ms >= ?")
		args = append(args, req.MinDuration*1000)
	}
	if req.MaxDuration > 0 {
		conditions = append(conditions, "duration_ms <= ?")
		args = append(args, req.MaxDuration*1000)
	}

	if req.HasTranscript {
		conditions = append(conditions, "(search_text IS NOT NULL AND search_text != '')")
	}

	if req.HasDriveLink {
		conditions = append(conditions, "(drive_link != '' OR download_link != '')")
	}

	if req.CreatedAfter != "" {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, req.CreatedAfter)
	}
	if req.CreatedBefore != "" {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, req.CreatedBefore)
	}

	whereClause := strings.Join(conditions, " AND ")

	countQuery := "SELECT COUNT(*) FROM media_assets WHERE " + whereClause
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count query: %w", err)
	}

	sortField, err := resolveSortColumn(req.SortBy)
	if err != nil {
		return nil, err
	}
	sortDir := resolveSortDir(req.SortAsc)

	dataQuery := fmt.Sprintf("SELECT %s FROM media_assets WHERE %s ORDER BY %s %s LIMIT ? OFFSET ?",
		MediaAssetColumns, whereClause, sortField, sortDir)
	dataArgs := append(args, req.Limit, req.Offset)

	rows, err := s.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, fmt.Errorf("data query: %w", err)
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}

	return &detail.AdvancedSearchResult{
		Clips:  clips,
		Total:  total,
		Limit:  req.Limit,
		Offset: req.Offset,
	}, rows.Err()
}

// SearchStockByKeywords searches stock clips by keywords using LIKE on
// the media_assets table. The source is hard-coded to 'stock'.
func (s *AssetStoreSQLite) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*asset.Asset, error) {
	if len(keywords) == 0 {
		return []*asset.Asset{}, nil
	}

	columns := clipSearchColumns()
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*asset.Asset{}, nil
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM media_assets
		WHERE source = 'stock' AND `+SearchableLifecycleFilter()+` AND (%s)
		LIMIT ?`,
		MediaAssetColumns,
		conditionSQL,
	)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

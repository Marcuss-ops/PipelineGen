package assets

import (
	"context"
	"fmt"
	"strings"

	
	"github.com/Marcuss-ops/PipelineGen/pkg/sqlutil"
)

// SearchClips searches clips by tag or name.
//
// Layered strategy:
//  1. Fast path: indexed clip_search_terms table (O(log n) per term).
//  2. Fallback: LIKE on tags, name and richer metadata fields (AND semantics).
//  3. Recursive fallback: OR semantics when AND yields zero results.
//
// Results are scored by keyword match quality, quality score, duplicate
// penalty and sponsor penalty (see scoreClips).
//
// Package layout: related but distinct search paths live in dedicated files:
//   - search.go        — SearchClips + SearchClipsAdvanced + keyword LIKE.
//   - search_terms.go  — SearchByTerms + fetchClipsByIDs (indexed lookup).
//   - search_stock.go  — SearchStockByKeywords (source='stock' shortcut).
//   - list_clips.go    — list ops + LastUpdatedAtForTerm.
func (s *AssetStoreSQLite) SearchClips(ctx context.Context, source, tag string) ([]*Asset, error) {
	keywords := strings.Fields(tag)
	if len(keywords) == 0 {
		keywords = []string{tag}
	}

	// Fast path: use indexed search terms table
	clips, err := r.SearchByTerms(ctx, source, keywords, 50)
	if err == nil && len(clips) > 0 {
		scored := scoreClips(clips, keywords)
		return scored, nil
	}

	// Fallback: LIKE on tags, name, and richer metadata fields (AND semantics)
	columns := clipSearchColumns()
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*Asset{}, nil
	}

	query := r.buildMediaAssetQuery(source) + " AND (" + conditionSQL + ")"

	finalArgs := []any{}
	if source != "" && source != "all" && source != "unified" {
		finalArgs = append(finalArgs, source)
	}
	finalArgs = append(finalArgs, args...)

	rows, err := r.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, clip)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// If AND returned results, score and return
	if len(results) > 0 {
		return scoreClips(results, keywords), nil
	}

	// OR fallback: broaden to ANY keyword matching when AND yields zero results.
	if len(keywords) > 1 {
		orCondition, orArgs := sqlutil.BuildFallbackLikeConditionsOR(keywords, columns)
		if orCondition != "" {
			orQuery := r.buildMediaAssetQuery(source) + " AND (" + orCondition + ")"
			orFinalArgs := []any{}
			if source != "" && source != "all" && source != "unified" {
				orFinalArgs = append(orFinalArgs, source)
			}
			orFinalArgs = append(orFinalArgs, orArgs...)

			orRows, err := r.db.QueryContext(ctx, orQuery, orFinalArgs...)
			if err != nil {
				return nil, err
			}
			defer orRows.Close()

			for orRows.Next() {
				clip, err := scanCanonicalAssetRows(orRows)
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

	return scoreClips(results, keywords), nil
}

// SearchClipsByKeywords searches clips by keywords using LIKE on the media_assets table.
func (s *AssetStoreSQLite) SearchClipsByKeywords(ctx context.Context, source string, keywords []string, limit int) ([]*Asset, error) {
	if len(keywords) == 0 {
		return []*Asset{}, nil
	}

	columns := clipSearchColumns()
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*Asset{}, nil
	}

	query := fmt.Sprintf("%s AND (%s) LIMIT ?", r.buildMediaAssetQuery(source), conditionSQL)
	finalArgs := []any{}
	if source != "" && source != "all" && source != "unified" {
		finalArgs = append(finalArgs, source)
	}
	finalArgs = append(finalArgs, args...)
	finalArgs = append(finalArgs, limit)

	rows, err := r.db.QueryContext(ctx, query, finalArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// SearchClipsAdvanced searches clips with structured filters.
//
// Splits the advanced-filter logic into its own type/result pair so the
// public API surface (AdvancedSearchRequest, AdvancedSearchResult) is
// easy to discover and the wider search.go stays focused on the
// keyword-only SearchClips / SearchClipsByKeywords paths.
func (s *AssetStoreSQLite) SearchClipsAdvanced(ctx context.Context, req AdvancedSearchRequest) (*AdvancedSearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 50
	}
	if req.Limit > 500 {
		req.Limit = 500
	}

	conditions := []string{r.SoftDeleteFilter()}
	args := []any{}

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
			cond, kwArgs := sqlutil.BuildFallbackLikeConditions(keywords, columns)
			if cond != "" {
				conditions = append(conditions, "("+cond+")")
				args = append(args, kwArgs...)
			}
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
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count query: %w", err)
	}

	sortField := "created_at"
	if req.SortBy != "" {
		switch req.SortBy {
		case "duration":
			sortField = "duration_ms"
		case "name":
			sortField = "name"
		case "source":
			sortField = "source"
		}
	}
	sortDir := "DESC"
	if req.SortAsc {
		sortDir = "ASC"
	}

	dataQuery := fmt.Sprintf("SELECT %s FROM media_assets WHERE %s ORDER BY %s %s LIMIT ? OFFSET ?",
		mediaAssetColumns, whereClause, sortField, sortDir)
	dataArgs := append(args, req.Limit, req.Offset)

	rows, err := r.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, fmt.Errorf("data query: %w", err)
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}

	return &AdvancedSearchResult{
		Clips:  clips,
		Total:  total,
		Limit:  req.Limit,
		Offset: req.Offset,
	}, rows.Err()
}

// AdvancedSearchRequest contains filters for advanced clip search.
type AdvancedSearchRequest struct {
	Q             string `json:"q"`
	Source        string `json:"source"`
	Category      string `json:"category"`
	MinDuration   int    `json:"min_duration"`
	MaxDuration   int    `json:"max_duration"`
	HasTranscript bool   `json:"has_transcript"`
	HasDriveLink  bool   `json:"has_drive_link"`
	CreatedAfter  string `json:"created_after"`
	CreatedBefore string `json:"created_before"`
	SortBy        string `json:"sort_by"`
	SortAsc       bool   `json:"sort_asc"`
	Limit         int    `json:"limit"`
	Offset        int    `json:"offset"`
}

// AdvancedSearchResult is the response for advanced clip search.
type AdvancedSearchResult struct {
	Clips  []*Asset `json:"clips"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

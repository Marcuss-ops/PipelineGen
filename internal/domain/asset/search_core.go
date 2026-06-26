package asset

import (
	"context"
	"fmt"
	"strings"

	sqlutil "github.com/Marcuss-ops/PipelineGen/pkg/sqlutil"
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
	clips, err := s.SearchByTerms(ctx, source, keywords, 50)
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

	query := buildMediaAssetQuery(source) + " AND (" + conditionSQL + ")"

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
			orQuery := buildMediaAssetQuery(source) + " AND (" + orCondition + ")"
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

	query := fmt.Sprintf("%s AND (%s) LIMIT ?", buildMediaAssetQuery(source), conditionSQL)
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

	conditions := []string{SoftDeleteFilter()}
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
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
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

	rows, err := s.db.QueryContext(ctx, dataQuery, dataArgs...)
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
	Total  int      `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

// SearchStockByKeywords searches stock clips by keywords using LIKE on the media_assets table.
//
// Lives in its own file so the stock-specific query (which hard-codes
// source='stock' and ignores the Repository.source argument) does not
// clutter the user-facing search.go.
func (s *AssetStoreSQLite) SearchStockByKeywords(ctx context.Context, keywords []string, limit int) ([]*Asset, error) {
	if len(keywords) == 0 {
		return []*Asset{}, nil
	}

	columns := clipSearchColumns()
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*Asset{}, nil
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM media_assets
		WHERE source = 'stock' AND `+SoftDeleteFilter()+` AND (%s)
		LIMIT ?`,
		mediaAssetColumns,
		conditionSQL,
	)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
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

// SearchRequest is the generic search REQUEST DTO passed to a Searcher.
// Distinct from the DB-backed YouTube topic entity asset.SearchQuery
// (declared in imagery.go) which has its own ID, Category, etc. — the
// two have separate identities and the splatter of fields they hold was
// the source of an unintentional name collision during Wave-14.
//
// Renaming history (Wave-14, Jun 2026): this type previously used the
// name `SearchQuery` but conflicted with the YouTube topic entity that
// came in from internal/domain/media. The canonical name we inherited
// from `search_types.go` was `SearchQuery` for the request DTO; the
// arrival of the database-backed entity forced a clarification. The
// request DTO kept the role-rich name `SearchRequest` because it is
// precisely that — a request shape, never persisted.
type SearchRequest struct {
	Text      string   `json:"text"`
	Source    string   `json:"source,omitempty"`
	MediaType string   `json:"media_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Limit     int      `json:"limit"`
}

// SearchResult is a scored search hit.
type SearchResult struct {
	Asset *Asset  `json:"asset"`
	Score float64 `json:"score"`
}

// Searcher is the optional interface for semantic/keyword search.
type Searcher interface {
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
}

// Package core provides canonical shared types for the PipelineGen system.
// Analysis types moved here from internal/ml/ollama/types to prevent
// cross-layer imports from handler packages into the ML layer.

// EntityExtractionRequest represents a request to extract entities from a segment.
type EntityExtractionRequest struct {
	SegmentText  string `json:"segment_text"`
	SegmentIndex int    `json:"segment_index"`
	EntityCount  int    `json:"entity_count"`
}

// EntityExtractionResult represents the result of entity extraction for a segment.
type EntityExtractionResult struct {
	SegmentIndex     int               `json:"segment_index"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string `json:"entity_senza_testo"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportanti []string          `json:"parole_importanti"`
	ArtlistPhrases   []string          `json:"artlist_phrases"`
}

// SegmentEntities represents extracted entities for a single segment.
type SegmentEntities struct {
	SegmentIndex     int                 `json:"segment_index"`
	SegmentText      string              `json:"segment_text"`
	FrasiImportanti  []string            `json:"frasi_importanti"`
	EntitaSenzaTesto map[string]string   `json:"entity_senza_testo"`
	NomiSpeciali     []string            `json:"nomi_speciali"`
	ParoleImportanti []string            `json:"parole_importanti"`
	ArtlistPhrases   []string            `json:"artlist_phrases"`
	ArtlistMatches   map[string][]string `json:"artlist_matches"`
}

// FullEntityAnalysis represents the complete entity analysis for a script.
type FullEntityAnalysis struct {
	TotalSegments         int               `json:"total_segments"`
	SegmentEntities       []SegmentEntities `json:"segment_entities"`
	TotalEntities         int               `json:"total_entities"`
	EntityCountPerSegment int               `json:"entity_count_per_segment"`
}

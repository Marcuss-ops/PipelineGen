// Package asset — search DTOs (Wave C / Phase 2 slim).
//
// Phase 2 (Wave C / Blocco 1 asset.Asset SSOT, June 2026): the 4 SQL
// receivers (SearchClips/SearchClipsByKeywords/SearchClipsAdvanced/
// SearchStockByKeywords) that used to live here are now canonical on
// the LOCAL infra sqlite asset store
// (internal/platform/sqlite/assets/search_queries.go)
// and reached via HYBRID-embed promotion.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-21): SearchClipsByKeywords and
// SearchStockByKeywords have since been DELETED from that file (zero call
// sites); SearchClips and SearchClipsAdvanced remain.
// Cross-package scoring
// (`asset.ScoreClips`) is the public surface for the SearchClips
// scoring algorithm; this file no longer holds scoring logic.
//
// This file now carries ONLY the canonical search DTO types
// (AdvancedSearchRequest/Result, SearchRequest/Result, Searcher). No
// SQL primitives, no `database/sql` import.
//
// The LLM-era entity DTOs (EntityExtractionRequest/Result, SegmentEntities,
// FullEntityAnalysis) were removed in the post-cutover NLP cleanup: the
// deterministic VisualNER + phrases.Select chain replaced the Ollama entity
// extraction path, and the canonical typed surface is
// internal/kernel/script.EntityResult.
package detail

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

import "context"

// AdvancedSearchRequest contains filters for advanced clip search.
//
// PR-AGGREGATE-FILTER-UNIFORM (July 2026): Language and Tags added
// to the canonical DTO so the local backend (composed with the
// SQLite AdvancedSearchRepo) can forward every filter the handler
// accepts on q.Filters. Category was already present (the only
// non-Q/Source metadata filter the repo honoured pre-PR-2); Language
// joins via exact-match equality (BCP-47 string compare) and Tags
// join via pkg/sqlutil.BuildFallbackLikeConditions over the JSON
// tags column (FTS5 is banned per project policy). godlike/06 SSOT:
// this DTO is the canonical owner-of-fact for the local-backend
// filter set; do not introduce a parallel Filter struct.
type AdvancedSearchRequest struct {
	Q             string   `json:"q"`
	Source        string   `json:"source"`
	Category      string   `json:"category"`
	MediaType     string   `json:"media_type"`
	Language      string   `json:"language"`
	Tags          []string `json:"tags,omitempty"`
	MinDuration   int      `json:"min_duration"`
	MaxDuration   int      `json:"max_duration"`
	HasTranscript bool     `json:"has_transcript"`
	HasDriveLink  bool     `json:"has_drive_link"`
	// ExcludeUnclassified restricts results to rows classified into the
	// canonical taxonomy (media_assets.asset_kind != ''). When true, the
	// repo appends the ClassifiedAssetFilter fragment so one-off / test
	// artifacts (empty asset_kind) never surface in catalog search.
	ExcludeUnclassified bool   `json:"exclude_unclassified,omitempty"`
	CreatedAfter        string `json:"created_after"`
	CreatedBefore       string `json:"created_before"`
	SortBy              string `json:"sort_by"`
	SortAsc             bool   `json:"sort_asc"`
	Limit               int    `json:"limit"`
	Offset              int    `json:"offset"`
}

// AdvancedSearchResult is the response for advanced clip search.
type AdvancedSearchResult struct {
	Clips  []*asset.Asset `json:"clips"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// SearchRequest is the generic search REQUEST DTO passed to a Searcher.
// Distinct from the DB-backed YouTube topic entity asset.SearchQuery
// (declared in imagery.go) which has its own ID, Category, etc. — the
// two have separate identities and the splatter of fields they hold
// was the source of an unintentional name collision during Wave-14.
//
// Renaming history (Wave-14, Jun 2026): this type previously used the
// name `SearchQuery` but conflicted with the YouTube topic entity
// that came in from internal/kernel/media. The canonical name we
// inherited from `search_types.go` was `SearchQuery` for the request
// DTO; the arrival of the database-backed entity forced a
// clarification. The request DTO kept the role-rich name
// `SearchRequest` because it is precisely that — a request shape,
// never persisted.
type SearchRequest struct {
	Text      string   `json:"text"`
	Source    string   `json:"source,omitempty"`
	MediaType string   `json:"media_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Limit     int      `json:"limit"`
}

// SearchResult is a scored search hit.
type SearchResult struct {
	Asset *asset.Asset `json:"asset"`
	Score float64      `json:"score"`
}

// Searcher is the optional interface for semantic/keyword search.
type Searcher interface {
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
}

// Package core provides canonical shared types for the PipelineGen
// system.
//
// The LLM-era entity analysis types that used to live here moved to the
// canonical typed contracts (internal/kernel/script.EntityResult) when the
// deterministic NLP cutover removed the Ollama entity-extraction path.

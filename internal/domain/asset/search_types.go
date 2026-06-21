package asset

import "context"

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

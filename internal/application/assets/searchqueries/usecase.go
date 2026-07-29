// Package searchqueries — application-layer orchestration for the
// search_queries table (scheduled YouTube topic searches).
//
// Wave 14 problem #3 close-out (June 2026): this use-case was extracted
// from internal/api/assets/handler_searchqueries.go so the handler
// becomes pure transport (Pattern 8 from AGENTS.md: bind / JSON /
// delegate / render). The handler previously embedded the repository
// directly and called `repo.Upsert / ListAll / etc.` inline; transport
// now asks the use-case, which delegates to the Repository port.
//
// Construction pattern: pure — no setters (Pattern 0). Repository is
// the only port; the concrete implementation lives in
// internal/infrastructure/database/sqlite/assets (the SQLite Repository).
// Drift at the use-case / port boundary is impossible by construction —
// Go's structural typing forces any caller of NewUseCase to satisfy
// the Repository interface, and every use-case method passes through
// it, so adding or renaming a port method is a compile-time error
// across the whole composition tree.
//
// Note for grep: the repo method is `ListResultsByQuery`; the use-case
// deliberately renames it to `ListResults` so the application-layer
// API surface no longer leaks the "ByQuery" suffix. If you are
// looking for the history, the rename lives ONLY here — the SQLite
// repository keeps the original name.
package searchqueries

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Repository is the port the use-case depends on. Production wire-up
// uses *assets.SearchQueriesRepository (concrete) which satisfies this
// interface via duck typing (no explicit implements keyword in Go).
//
// The methods mirror — one-to-one — the ones the handler previously
// called inline; each use-case method is a thin pass-through because
// the operations are pure CRUD. Orchestration (e.g. defaults, retries,
// cross-table fan-out) would belong on the use-case if/when added.
type Repository interface {
	ListAll(ctx context.Context) ([]*asset.SearchQuery, error)
	ListActive(ctx context.Context) ([]*asset.SearchQuery, error)
	GetByID(ctx context.Context, id string) (*asset.SearchQuery, error)
	Upsert(ctx context.Context, q *asset.SearchQuery) error
	Delete(ctx context.Context, id string) error
	ListResultsByQuery(ctx context.Context, queryID string) ([]*asset.SearchQueryResult, error)
}

// UseCase is the application-layer entry point for SearchQueries CRUD.
// Operations are intentionally 1-1 with handler routes so each HTTP
// verb maps to one typed method here.
type UseCase struct {
	repo Repository
}

// NewUseCase wires the Repository port. Composition root constructs
// this once at startup.
func NewUseCase(repo Repository) *UseCase {
	return &UseCase{repo: repo}
}

// ListAll returns every search query, active-first.
func (uc *UseCase) ListAll(ctx context.Context) ([]*asset.SearchQuery, error) {
	return uc.repo.ListAll(ctx)
}

// ListActive returns only the active search queries.
func (uc *UseCase) ListActive(ctx context.Context) ([]*asset.SearchQuery, error) {
	return uc.repo.ListActive(ctx)
}

// GetByID returns a single search query by ID.
func (uc *UseCase) GetByID(ctx context.Context, id string) (*asset.SearchQuery, error) {
	return uc.repo.GetByID(ctx, id)
}

// Upsert creates or updates a search query. Defaults (CheckInterval,
// MaxResults, MinScore, timestamps) live on the concrete repository
// implementation; this use-case is a thin pass-through by design.
func (uc *UseCase) Upsert(ctx context.Context, q *asset.SearchQuery) error {
	return uc.repo.Upsert(ctx, q)
}

// Delete removes a search query by ID.
func (uc *UseCase) Delete(ctx context.Context, id string) error {
	return uc.repo.Delete(ctx, id)
}

// ListResults returns the processed search-query results for a given
// query ID.
func (uc *UseCase) ListResults(ctx context.Context, id string) ([]*asset.SearchQueryResult, error) {
	return uc.repo.ListResultsByQuery(ctx, id)
}

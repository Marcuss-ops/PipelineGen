package artlist

import (
	"context"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Search esegue una ricerca di clip.
func (s *Service) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	return s.searchService.Search(ctx, req)
}

// SearchLive esegue una ricerca live tramite scraper.
//
// preferRemote (PR-P2-SEARCH-LIVE, July 2026): when true, the chain is
// reordered so the Node ScraperSearcher is the PRIMARY provider and
// BOTH the local DB-level cache (DBSearcher, indexed terms) AND the
// in-memory TTL cache (CachedSearcher wrapper around scraper) are
// COMPLETELY DROPPED from the chain.
//
// prefer_remote defaulting is endpoint-scoped (user spec): the
// GET /api/artlist/search/live handler defaults to true (operator-
// facing live-search semantics). Internal callers must pass false:
//   - DiscoverAndQueueRun: explicit RunTag workflow → false (legacy
//     cache-first "discover fresh content" semantics is preserved).
//   - run_orchestrator_stages::stageDiscoverClips → SearchLiveAndSave
//     → SearchLive: false (orchestrator retries must not re-hit the
//     scraper for the same term).
func (s *Service) SearchLive(ctx context.Context, term string, limit int, preferRemote bool) ([]Candidate, error) {
	return s.searchService.SearchLive(ctx, term, limit, preferRemote)
}

// SearchCatalogOnly resolves through the Node catalog without contacting the
// provider or consulting the Go materialized-asset database.
func (s *Service) SearchCatalogOnly(ctx context.Context, term string, limit int) ([]Candidate, error) {
	return s.searchService.SearchCatalogOnly(ctx, term, limit)
}

// SearchLiveForceRefresh is the VidRush live-search surface. It bypasses
// provider-side result caches while leaving legacy SearchLive semantics
// unchanged for other callers.
func (s *Service) SearchLiveForceRefresh(ctx context.Context, term string, limit int, preferRemote bool) ([]Candidate, error) {
	return s.searchService.SearchLiveForceRefresh(ctx, term, limit, preferRemote)
}

// DiscoverAndQueueRun scopre clip e accoda un'esecuzione.
func (s *Service) DiscoverAndQueueRun(ctx context.Context, term string, limit int) (*SearchResponse, *RunTagResponse, error) {
	return s.searchService.DiscoverAndQueueRun(ctx, term, limit)
}

// GetRunTag ottiene lo stato di un run.
func (s *Service) GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error) {
	return s.runOrchestrator.GetRunTag(ctx, runID)
}

// GetStats ottiene statistiche sul catalogo Artlist.
func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	return s.diagnosticsService.GetStats(ctx)
}

// Diagnostics ottiene informazioni diagnostiche per un termine.
func (s *Service) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
	return s.diagnosticsService.Diagnostics(ctx, term)
}

// SearchClips cerca clip nel database locale.
func (s *Service) SearchClips(ctx context.Context, term string) []*asset.Asset {
	return s.searchService.SearchClips(ctx, term)
}

// HandleJob gestisce un job dalla coda.
func (s *Service) HandleJob(ctx context.Context, j *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	return s.jobAdapter.HandleJob(ctx, j, tools)
}

// Searchers returns the canonical (scraper, pixabay, pexels) Searcher triplet
// for diagnostic + observability access. Wired at composition time by
// build_bundles_artlist.go::WireArtlist from the infrastructure-layer
// concretes (internal/infrastructure/artlist/scraper + fallback); each
// satisfies the same Searcher port (godlike/06 one-canonical-owner-per-fact).
//
// Per godlike/06 SSOT the construction is the composition root's exclusive
// responsibility — callers MUST NOT call this method to discover "which
// searcher should I use" (the canonical answer is the application's
// SearchLive / DiscoverAndQueueRun fallback chain, NOT a hand-rolled
// dispatcher at the caller). Use the accessor for diagnostic surfaces
// (operator visibility into wired capabilities, build_bundles_artlist_test.go
// PR-ARTLIST-SEARCHERS wiring assertions, future health probes).
func (s *Service) Searchers() (Searcher, Searcher, Searcher) {
	return s.scraperSearcher, s.pixabaySearcher, s.pexelsSearcher
}

// GetJobByRunID ottiene un job per run ID.
func (s *Service) GetJobByRunID(ctx context.Context, runID string) (*appjobs.Job, error) {
	return s.jobAdapter.GetJobByRunID(ctx, runID)
}

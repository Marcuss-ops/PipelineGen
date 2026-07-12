package artlist

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
	"go.uber.org/zap"
)

// SearchService gestisce tutte le operazioni di ricerca Artlist.
type SearchService struct {
	service *Service
	// assetRepo is the canonical writer (PR12b). Late-bound via SetAssetRepo.
	assetRepo asset.Repository
	// dispatcher is the canonical outbox dispatcher port (QDRANT-002).
	// When non-nil, SearchLiveAndSave routes through EnqueueAndIndex
	// instead of raw assetStore.Upsert. Wired from Service.dispatcher.
	dispatcher Dispatcher
	// PR2: injected Searcher implementations from infrastructure.
	// nil means that level is skipped in the fallback chain.
	scraperSearcher Searcher
	pixabaySearcher Searcher
	pexelsSearcher  Searcher
	// searchStrategy controls the Pexels/Pixabay fallback chain
	// (PR-AUDIT-5, July 2026). Wired from the parent Service.
	searchStrategy ArtlistSearchStrategy
	cfg            *config.Config
	log            *zap.Logger
}

// SetAssetRepo injects the canonical assetRepo.
func (ss *SearchService) SetAssetRepo(r asset.Repository) {
	ss.assetRepo = r
}

// NewSearchService creates a new SearchService wired to the Service.
//
// PR1 (User directive, June 2026): fail-closed at construction.
// `dispatcher` is the canonical outbox port that performs the atomic
// media_assets upsert + outbox enqueue (QDRANT-002 contract). The
// legacy nil-dispatcher fallback to raw assetStore.Upsert is REMOVED:
// callers that need asset ingestion MUST wire the canonical dispatcher
// at composition time (see internal/app/module_sources.go::WireArtlist
// which already pre-rejects nil). Returns ErrAssetMutationDispatcherUnavailable
// when dispatcher is nil; the composition root surfaces that error
// before the system comes up mis-configured.
//
// The constructor also keeps NewService building blocks aligned with
// the post-QDRANT-002 PR7 contract that production wiring must enforce
// at composition.
func NewSearchService(s *Service, dispatcher Dispatcher) (*SearchService, error) {
	if dispatcher == nil {
		return nil, ErrAssetMutationDispatcherUnavailable
	}
	ss := &SearchService{service: s, dispatcher: dispatcher, searchStrategy: s.searchStrategy}
	return ss, nil
}

// Search esegue una ricerca di clip nel database Artlist.
func (ss *SearchService) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	s := ss.service
	term := normalizeSearchTerm(req.Term)
	resp := &SearchResponse{OK: true, Term: term}

	if term == "" {
		return resp, nil
	}

	clipsList, err := s.assetStore.SearchClips(ctx, "artlist", term)
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	// Apply limit
	limit := defaults.Int(req.Limit, 8)
	if limit > 50 {
		limit = 50
	}
	if len(clipsList) > limit {
		clipsList = clipsList[:limit]
	}

	resp.Clips = make([]asset.Asset, 0, len(clipsList))
	for _, c := range clipsList {
		if a := toDomain(c); a != nil {
			resp.Clips = append(resp.Clips, *a)
		}
	}
	resp.Source = "database"

	return resp, nil
}

// SearchLive esegue una ricerca live tramite la Searcher fallback chain.
//
// preferRemote (PR-P2-SEARCH-LIVE, July 2026): when true, the chain is
// reordered to make the Node ScraperSearcher the PRIMARY provider and
// drop BOTH the local DB-level cache (DBSearcher, indexed terms) AND
// the in-memory TTL cache (CachedSearcher wrapper around the scraper).
// Other remote providers gated by SearchStrategy (Pixabay, Pexels)
// stay in the chain because they remain genuinely remote fallbacks
// (not local cache). preferRemote=false preserves the legacy
// cache-first semantics: DBSearcher (level 1 fast path) →
// CachedScraper (level 2) → Pixabay/Pexels (per strategy).
//
// godlike/06 SSOT: the chain-order decision lives at the canonical
// buildSearcherChain resolver — this method only threads the flag.
func (ss *SearchService) SearchLive(ctx context.Context, term string, limit int, preferRemote bool) ([]Candidate, error) {
	return ss.searchLiveWithFallbacks(ctx, term, limit, preferRemote)
}

// SearchLiveAndSave esegue una ricerca live e salva i risultati nel database.
//
// QDRANT-002: Routes through dispatcher.EnqueueAndIndex when wired.
// Falls back to raw assetStore.Upsert when dispatcher is nil.
func (ss *SearchService) SearchLiveAndSave(ctx context.Context, originalTerm string, limit int) (*SearchResponse, error) {
	s := ss.service
	normalizedTerm := normalizeSearchTerm(originalTerm)
	// PR-P2-SEARCH-LIVE: SearchLiveAndSave is the orchestrator path
	// (DiscoverAndQueueRun + run_orchestrator_stages::stageDiscoverClips);
	// it intentionally preserves the legacy cache-first semantics so
	// repeated orchestrator runs hit the local cache instead of
	// re-issuing scraper requests on every retry. preferRemote=false.
	candidates, err := ss.SearchLive(ctx, normalizedTerm, limit, false)
	if err != nil {
		return nil, err
	}

	resp := &SearchResponse{OK: true, Term: originalTerm, Source: "live", Clips: make([]asset.Asset, 0, len(candidates))}

	for _, c := range candidates {
		if c.ID == "" {
			s.log.Warn("skipping candidate with missing id", zap.String("title", c.Title))
			continue
		}

		name := c.Title
		if name == "" {
			name = c.ID
		}

		clip := &asset.Asset{
			ID:          c.ID,
			Name:        name,
			Source:      asset.Source("artlist"),
			MediaType:   asset.MediaType("video"),
			Tags:        []string{originalTerm},
			SearchTerms: []string{originalTerm},
			SourceURL:   c.SourceRef,
			ClipPageURL: c.PageURL,
		}
		clip.SetDownloadLink(c.SourceRef)

		// Defensive nil-check on assetStore: production always wires this,
		// but tests do construct bare &Service{...} fixtures to exercise
		// the dispatcher guard. Without the guard, those fixtures would
		// SIGSEGV here before reaching the dispatcher check, masking
		// the typed-sentinel contract. Skip the merge path silently
		// when assetStore is nil — the dispatcher check below is the
		// real surface layer, the merge is just a metadata refresh.
		if s.assetStore != nil {
			if existing, err := s.assetStore.Get(ctx, clip.ID); err == nil && existing != nil {
				if existing.LocalPath() != "" {
					clip.SetLocalPath(existing.LocalPath())
				}
				if existing.FileHash() != "" {
					clip.SetFileHash(existing.FileHash())
				}
				if existing.DriveLink() != "" {
					clip.SetDriveLink(existing.DriveLink())
				}
				if existing.DriveFileID() != "" {
					clip.SetDriveFileID(existing.DriveFileID())
				}
				if existing.DownloadLink() != "" && !strings.Contains(existing.DownloadLink(), "drive.google.com") {
					clip.SetDownloadLink(existing.DownloadLink())
				}
				if existing.ClipPageURL != "" {
					clip.ClipPageURL = existing.ClipPageURL
				}
			}
		}

		// PR1 (User directive, June 2026): the legacy `if dispatcher != nil ...
		// else assetStore.Upsert` fallback is REMOVED. SearchLiveAndSave
		// MUST route every ingested asset through the canonical outbox
		// dispatcher (atomic media_assets upsert + outbox enqueue). The
		// constructor (NewSearchService) already guards nil at construction
		// time; this single call site is the post-construction data mutation.
		// A belt-and-suspenders check at function entry catches runtime
		// tampering (e.g. someone calling SetDispatcher post-construction)
		// and yields a typed sentinel rather than a nil-pointer panic.
		if ss.dispatcher == nil {
			return nil, ErrAssetMutationDispatcherUnavailable
		}
		// Chip 2 (June 2026, fix-FASE9 followups plan): discovery saves the
		// row with STAGING + DISCOVERED states WITHOUT dispatching to Qdrant.
		// The downstream artlist.run processing path emits the canonical
		// asset.index.requested envelope via EnqueueAndIndex once the asset
		// is fully populated (real hash, Drive file id, upload complete).
		// This removes the "premature Qdrant indexing of an incomplete
		// asset" failure mode that the previous discovery-time EnqueueAndIndex
		// call produced (Qdrant saw a half-built asset for some seconds).
		upsertErr := ss.dispatcher.SaveDiscoveredAsset(ctx, clip, asset.StateStaging, asset.StateDiscovered)

		if upsertErr == nil {
			if a := toDomain(clip); a != nil {
				resp.Clips = append(resp.Clips, *a)
			}

			searchText := clip.Name + " " + originalTerm
			// Defensive nil-guard: production always wires assetStore,
			// but tests construct bare &Service{...} fixtures that
			// exercise the dispatcher guard. A nil assetStore here would
			// SIGSEGV before the test could observe the typed-sentinel
			// contract upstream — guard it to keep tests' assertions
			// focused on the dispatcher layer.
			if s.assetStore != nil {
				if updateErr := s.assetStore.UpdateSearchTerms(ctx, clip.ID, "artlist", clip.Name, clip.Tags, searchText); updateErr != nil {
					s.log.Debug("failed to update search terms for clip", zap.String("clip_id", clip.ID), zap.Error(updateErr))
				}
			}

		}
		// P0.6 (June 2026): previous in-process metadataWriter.EnrichAsync
		// fire-and-forget was deleted here. Silent background enrichment
		// violated the no-fake-availability rule (godlike/07) because
		// failures could not be surfaced to the search caller. P0.18
		// reintroduces structured enrichment via the canonical outbox path;
		// until then, search ingestion stores only raw clip metadata and a
		// separate /enrich job handles semantic payload population. The
		// metadataWriter field on SearchService is preserved for the
		// post-P0.18 wired path (struct init + port binding still active).
	}

	return resp, nil
}

// DiscoverAndQueueRun scopre clip e accoda un'esecuzione.
func (ss *SearchService) DiscoverAndQueueRun(ctx context.Context, originalTerm string, limit int) (*SearchResponse, *RunTagResponse, error) {
	s := ss.service
	normalizedTerm := normalizeSearchTerm(originalTerm)
	liveResp, err := ss.SearchLiveAndSave(ctx, originalTerm, limit)
	if err != nil {
		return nil, nil, err
	}

	if liveResp == nil || len(liveResp.Clips) == 0 {
		return liveResp, nil, nil
	}

	// Enqueue processing job through common jobs service
	if s.jobsSvc != nil {
		driveFolderID := s.cfg.Drive.ArtlistFolder()
		if strings.TrimSpace(driveFolderID) == "" {
			s.log.Warn("skipping artlist job enqueue because no root folder is configured", zap.String("term", normalizedTerm), zap.Int("limit", limit))
			return liveResp, nil, nil
		}

		groupName := "Artlist"
		if originalTerm != "" {
			groupName = originalTerm
		}

		dest, err := s.destinationService.ResolveDestination(ctx, groupName, driveFolderID)
		resolvedFolderID := ""
		if err == nil {
			resolvedFolderID = dest.FolderID
		}

		// Fase 5 / Commit 3 (July 2026) — wire the canonical
		// run-level dedup key as the ActiveKey so a replay of the
		// same run (same term + root folder + strategy + dryRun)
		// collapses at the kernel job broker's UNIQUE index on
		// `jobs.active_key`. Per user-spec literal "replay stessa
		// run non duplica" (dedup guarantee #2).
		//
		// Why NOT use the pkg/idempotency.JobKey here: the spec
		// defines 3 canonical keys (AssetKey, JobKey, OutboxKey)
		// for PERSISTENT deduplication (media_assets.id + outbox_events
		// UNIQUE constraints). Run-level ActiveKey is an EPHEMERAL
		// job-queue concurrency lock keyed on highly specific API
		// parameters (term + root folder + strategy + dryRun) that
		// do not belong in a generic provider-agnostic idempotency
		// package. artlist.RunDedupKey is the canonical surface for
		// this concern (godlike/06 SSOT); the API handler
		// (artlist_handlers.go::enqueueArtlistRun) already uses it.
		//
		// The Orchestrator (this function) was the LAST hold-out
		// without ActiveKey — a replay of DiscoverAndQueueRun
		// produced a fresh jobs row with implicit Type+Payload
		// dedup, which is brittle (any payload byte change breaks
		// the dedup). Setting ActiveKey makes the dedup explicit
		// and observable.
		//
		// Strategy and dryRun are empty/false in the orchestrator
		// path (the orchestrator doesn't expose them). This means
		// an API handler replay with the same term + root folder
		// + default strategy + dryRun=false produces the same
		// ActiveKey as an orchestrator replay — the dedup
		// unifies across entry points.
		runActiveKey := artlist.RunDedupKey(normalizedTerm, driveFolderID, "", false)
		job, err := s.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type:       "media.artlist",
			Payload:    (&JobCodec{}).PayloadFromRequest(&RunTagRequest{Term: normalizedTerm, Limit: limit, RootFolderID: driveFolderID}),
			ActiveKey:  runActiveKey,
			MaxRetries: 3,
		})
		if err != nil {
			s.log.Warn("artlist discovery queued save but failed to enqueue job", zap.String("term", normalizedTerm), zap.Error(err))
			return liveResp, nil, nil
		}

		runResp := JobToRunTagResponse(job)
		if runResp != nil {
			runResp.TagFolderID = resolvedFolderID
			if resolvedFolderID != "" {
				runResp.TagFolderLink = "https://drive.google.com/drive/folders/" + resolvedFolderID
			}
		}

		return liveResp, runResp, nil
	}

	return liveResp, nil, nil
}

// SearchClips searches clips in the database
func (ss *SearchService) SearchClips(ctx context.Context, term string) []*asset.Asset {
	s := ss.service
	term = normalizeSearchTerm(term)
	clips, err := s.assetStore.SearchClips(ctx, "artlist", term)
	if err != nil {
		s.log.Error("failed to search clips", zap.Error(err), zap.String("term", term))
		return nil
	}
	return toDomainPtrSlice(clips)
}

// QDRANT-asset-mutation isolation (June 2026): UpsertClip was
// DELETED from SearchService. Production callers in artlist MUST
// route through outbox.Dispatcher.EnqueueAndIndex. The dispatcher
// consumes mutations.AssetMutationPrimitives (see
// internal/application/assets/mutations/primitives.go), so tests that
// previously called ss.UpsertClip now inject a stub dispatcher (see
// dispatcher_stub_test.go) and assert the dispatcher path.

// searchLiveWithFallbacks orchestrates the fallback chain using the
// Searcher port. Implementations come from infrastructure:
//   - DB: in-memory indexed terms (fast) — INCLUDED ONLY when preferRemote=false
//   - CachedSearcher: wraps infrastructure/scraper with L1/L2 cache — INCLUDED ONLY when preferRemote=false
//   - ScraperSearcher: Node Playwright HTTP scraper (PR-P2-SEARCH-LIVE: PRIMARY when preferRemote=true)
//   - Pixabay HTTP (free fallback)
//   - Pexels HTTP (free fallback)
//
// preferRemote (PR-P2-SEARCH-LIVE, July 2026): when true, the chain
// drops DBSearcher + CachedSearcher wrapper entirely so the
// ScraperSearcher is the FIRST consulted provider. Pixabay/Pexels
// stay as genuine remote fallbacks (per SearchStrategy). When false,
// the legacy cache-first chain (DBSearcher → CachedScraper → other
// remotes) is preserved.
//
// godlike/06 SSOT: chain-ordering decisions live at the canonical
// buildSearcherChain resolver; this method only threads the flag
// through and surfaces the prefer_remote dimension in error logs.
func (ss *SearchService) searchLiveWithFallbacks(ctx context.Context, term string, limit int, preferRemote bool) ([]Candidate, error) {
	normalizedTerm := normalizeSearchTerm(term)
	if normalizedTerm == "" {
		return nil, fmt.Errorf("term is required")
	}
	if len(normalizedTerm) < 2 {
		return nil, fmt.Errorf("term must be at least 2 characters, got %q", normalizedTerm)
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	chain := ss.buildSearcherChain(preferRemote)
	if chain == nil {
		return nil, fmt.Errorf("no search providers configured")
	}

	candidates, err := chain.Search(ctx, SearchRequest{Term: normalizedTerm, Limit: limit, PreferRemote: preferRemote})
	if err != nil {
		ss.service.log.Warn("all search providers failed",
			zap.String("term", term),
			zap.Bool("prefer_remote", preferRemote),
			zap.Error(err),
		)
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no results from any search provider for %q (prefer_remote=%t)", normalizedTerm, preferRemote)
	}
	return candidates, nil
}

// buildSearcherChain constructs the Searcher fallback chain from the service
// configuration. Infrastructure searchers are injected here so the application
// layer stays decoupled from concrete implementations.
//
// PR-AUDIT-5 (July 2026): the strategy resolver (ResolveSearcherChain)
// controls which infra searchers are included. Only the DB searcher is
// always appended; scraper/pixabay/pexels are gated by the wired
// SearchStrategy.
//
// PR-P2-SEARCH-LIVE (July 2026): preferRemote is the operator-facing
// flag that LOWERS the chain to REMOTE providers only. Both the local
// DB-level cache (DBSearcher) AND the in-memory TTL cache
// (CachedSearcher wrapper around the scraper) are DROPPED when
// preferRemote=true per user-spec contract: "salta la cache locale
// e interroga sempre Artlist come provider primario. Mantieni la
// cache locale come fallback SOLO se prefer_remote=false".
//
//	Chain ordering under the two modes:
//
//	preferRemote=false:
//	  [DBSearcher, CachedSearcher(scraper), ...pixabay/pexels per strategy]
//
//	preferRemote=true:
//	  [ScraperSearcher(raw), ...pixabay/pexels per strategy]
//	  — NO DBSearcher, NO CachedSearcher wrapper.
//
// Pixabay/Pexels STAY as fallbacks in BOTH modes because they remain
// genuinely remote (not local cache). With preferRemote=true and the
// scraper returning empty results or an error, the chain loops to
// pixabay/pexels via SearcherFallbackChain.Search — but the
// DBSearcher is intentionally NOT reached, so the operator sees the
// remote-fallback semantic instead of a stale DB hit.
//
// godlike/06 SSOT: this resolver is the SINGLE canonical owner of
// the chain-order decision across BOTH modes. Callers MUST NOT
// hand-roll their own chain ordering; they pass the flag and read
// the canonical ordering here.
func (ss *SearchService) buildSearcherChain(preferRemote bool) *SearcherFallbackChain {
	s := ss.service

	var searchers []Searcher

	// Level 1: DB search (fast, indexed) — INCLUDED ONLY when
	// preferRemote=false (PR-P2-SEARCH-LIVE, July 2026). With
	// preferRemote=true the local DB cache is DROPPED entirely per
	// user-spec contract ("salta la cache locale"). Operators
	// forcing the scraper to be primary MUST NOT see stale DB hits
	// (godlike/07 no-fake-availability).
	if !preferRemote && s.assetStore != nil {
		searchers = append(searchers, NewDBSearcher(s.assetStore))
	}

	// Levels 2-*: infrastructure searchers gated by the canonical
	// strategy resolver (PR-AUDIT-5, godlike/06 SSOT).
	strategy := ss.searchStrategy
	if !strategy.IsValid() {
		strategy = DefaultArtlistSearchStrategy
	}

	infraSearchers := ResolveSearcherChain(strategy, s.scraperSearcher, s.pixabaySearcher, s.pexelsSearcher)

	// Wrap scraper with CachedSearcher ONLY when preferRemote=false
	// AND the scraper is gating-allowed by the strategy resolver
	// (PR-P2-SEARCH-LIVE, July 2026). With preferRemote=true the
	// CachedSearcher wrapper is dropped entirely so the scraper is
	// invoked on EVERY request (BYPASS-TTL). Without this, cached
	// hits would mask real scraper failures (godlike/07
	// no-fake-availability) AND the operator would never see fresh
	// results even though the endpoint advertises "live" semantics.
	for _, searcher := range infraSearchers {
		if !preferRemote && searcher == s.scraperSearcher {
			ttlHours := 24
			if s.cfg != nil && s.cfg.External.ArtlistLiveSearchCacheTTLHours > 0 {
				ttlHours = s.cfg.External.ArtlistLiveSearchCacheTTLHours
			}
			cached := NewCachedSearcher(s.scraperSearcher, s.liveCache, ttlHours, s.log)
			searchers = append(searchers, cached)
		} else {
			searchers = append(searchers, searcher)
		}
	}

	if len(searchers) == 0 {
		return nil
	}
	return NewSearcherFallbackChain(searchers...)
}

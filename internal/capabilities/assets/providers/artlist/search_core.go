package artlist

import (
	"context"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
	"go.uber.org/zap"
)

// SearchService coordinates the SQLite search adapter and live provider searchers.
// Construction/wiring stays in the application service, while concrete
// responsibility boundaries live in searcher_sqlite.go and searcher_cache.go.
type SearchService struct {
	service        *Service
	assetRepo      detail.Repository
	dispatcher     Dispatcher
	searchStrategy ArtlistSearchStrategy
}

func (ss *SearchService) SetAssetRepo(r detail.Repository) {
	ss.assetRepo = r
}

func NewSearchService(s *Service, dispatcher Dispatcher) (*SearchService, error) {
	if dispatcher == nil {
		return nil, ErrAssetMutationDispatcherUnavailable
	}
	return &SearchService{
		service:        s,
		dispatcher:     dispatcher,
		searchStrategy: s.searchStrategy,
	}, nil
}

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
	limit := defaults.Int(req.Limit, 8)
	if limit > 50 {
		limit = 50
	}
	if len(clipsList) > limit {
		clipsList = clipsList[:limit]
	}

	resp.Clips = make([]asset.Asset, 0, len(clipsList))
	for _, clip := range clipsList {
		if clip != nil {
			resp.Clips = append(resp.Clips, *clip)
		}
	}
	resp.Source = "database"
	return resp, nil
}

func (ss *SearchService) SearchLive(ctx context.Context, term string, limit int, preferRemote bool) ([]Candidate, error) {
	return ss.searchLiveWithFallbacks(ctx, term, limit, preferRemote, false, ResolveSearchMode("", preferRemote))
}

// SearchCatalogOnly resolves exclusively through the Node provider catalog.
// It never consults the Go DB, in-memory provider cache, or live provider.
func (ss *SearchService) SearchCatalogOnly(ctx context.Context, term string, limit int) ([]Candidate, error) {
	return ss.searchLiveWithFallbacks(ctx, term, limit, false, false, SearchModeCatalogOnly)
}

// SearchLiveForceRefresh is the live-search surface used by VidRush. It
// preserves the ordinary SearchLive behavior for legacy callers while
// forcing the remote provider to resolve a fresh stream URL.
func (ss *SearchService) SearchLiveForceRefresh(ctx context.Context, term string, limit int, preferRemote bool) ([]Candidate, error) {
	return ss.searchLiveWithFallbacks(ctx, term, limit, preferRemote, true, ResolveSearchMode("", preferRemote))
}

func (ss *SearchService) SearchLiveAndSave(ctx context.Context, originalTerm string, limit int) (*SearchResponse, error) {
	s := ss.service
	normalizedTerm := normalizeSearchTerm(originalTerm)
	candidates, err := ss.SearchLive(ctx, normalizedTerm, limit, false)
	if err != nil {
		return nil, err
	}

	hydratedClips := ss.hydrateDiscoveredClips(ctx, originalTerm, candidates)

	resp := &SearchResponse{
		OK:     true,
		Term:   originalTerm,
		Source: "live",
		Clips:  make([]asset.Asset, 0, len(hydratedClips)),
	}
	hasHydratedClip := false
	for _, clip := range hydratedClips {
		if clip == nil {
			continue
		}
		hasHydratedClip = true
		if ss.dispatcher == nil {
			return nil, ErrAssetMutationDispatcherUnavailable
		}
		upsertErr := ss.dispatcher.SaveDiscoveredAsset(ctx, clip, asset.StateStaging, asset.StateDiscovered)
		if upsertErr != nil {
			s.log.Error("artlist discovery: SaveDiscoveredAsset failed",
				zap.String("clip_id", clip.ID),
				zap.Error(upsertErr))
			return nil, fmt.Errorf("save discovered asset %s: %w", clip.ID, upsertErr)
		}
		resp.Clips = append(resp.Clips, *clip)
		if s.assetStore != nil {
			searchText := clip.Name + " " + originalTerm
			if updateErr := s.assetStore.UpdateSearchTerms(ctx, clip.ID, "artlist", clip.Name, clip.Tags, searchText); updateErr != nil {
				s.log.Debug("failed to update search terms for clip", zap.String("clip_id", clip.ID), zap.Error(updateErr))
			}
		}
	}
	if !hasHydratedClip {
		return resp, nil
	}
	return resp, nil
}

func (ss *SearchService) hydrateDiscoveredClips(ctx context.Context, originalTerm string, candidates []Candidate) []*asset.Asset {
	if len(candidates) == 0 {
		return nil
	}

	workers := searchHydrationConcurrency(len(candidates))
	clips, err := concurrent.Map(ctx, candidates, workers, func(ctx context.Context, idx int, candidate Candidate) (*asset.Asset, error) {
		_ = idx
		return ss.hydrateDiscoveredClip(ctx, originalTerm, candidate), nil
	})
	if err != nil {
		if ss.service != nil && ss.service.log != nil {
			ss.service.log.Warn("artlist hydration batch failed; falling back to partial results", zap.Error(err))
		}
		return nil
	}
	return clips
}

func (ss *SearchService) hydrateDiscoveredClip(ctx context.Context, originalTerm string, candidate Candidate) *asset.Asset {
	s := ss.service
	if candidate.ID == "" {
		if s != nil && s.log != nil {
			s.log.Warn("skipping candidate with missing id", zap.String("title", candidate.Title))
		}
		return nil
	}

	pageURL := candidate.PageURL
	if pageURL == "" {
		pageURL = "https://artlist.io/stock-footage/clip/" + candidate.ID
	}

	// Hydrate search metadata with the full clip detail page before
	// persisting the discovered asset. The detail fetcher enriches the
	// synthetic search result with creator, categories, duration,
	// dimensions, license, collection, and raw metadata.
	hydrated := &candidate
	if s != nil && s.detailFetcher != nil {
		detailed, detailErr := s.detailFetcher.FetchDetails(ctx, pageURL)
		if detailErr != nil && s.log != nil {
			s.log.Warn("failed to hydrate Artlist clip details, using search metadata",
				zap.String("clip_id", candidate.ID),
				zap.String("page_url", pageURL),
				zap.Error(detailErr))
		} else if detailed != nil {
			hydrated = detailed
		}
	}

	clip := candidateToAsset(hydrated, pageURL)
	clip.SetDownloadLink(candidate.SourceRef)

	// Keep provider-side tags pure; the search term that discovered the
	// clip lives in SearchTerms and Metadata["discovered_by_queries"].
	providerTags := make([]string, len(hydrated.Keywords))
	copy(providerTags, hydrated.Keywords)

	clip.ProviderTags = providerTags
	clip.RebuildTags()

	clip.SearchTerms = deduplicateStrings(
		append([]string{originalTerm}, providerTags...),
	)

	if clip.Metadata == nil {
		clip.Metadata = map[string]any{}
	}
	clip.Metadata["provider_tags"] = providerTags
	clip.Metadata["discovered_by_queries"] = []string{originalTerm}
	clip.Metadata["provider_categories"] = hydrated.Categories
	clip.Metadata["metadata_origin"] = "artlist"

	if s != nil && s.assetStore != nil {
		if existing, getErr := s.assetStore.Get(ctx, clip.ID); getErr == nil && existing != nil {
			if existing.LocalPath() != "" {
				clip.SetLocalPath(existing.LocalPath())
			}
			if existing.LegacyFileMD5() != "" {
				clip.SetLegacyFileMD5(existing.LegacyFileMD5())
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

	return clip
}

func searchHydrationConcurrency(candidateCount int) int {
	if candidateCount <= 1 {
		return 1
	}
	const maxHydrationWorkers = 4
	if candidateCount < maxHydrationWorkers {
		return candidateCount
	}
	return maxHydrationWorkers
}

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

	driveFolderID := s.cfg.Drive.ArtlistFolder()
	if strings.TrimSpace(driveFolderID) == "" {
		s.log.Warn("skipping artlist job enqueue because no root folder is configured",
			zap.String("term", normalizedTerm), zap.Int("limit", limit))
		return liveResp, nil, nil
	}

	groupName := "Artlist"
	if originalTerm != "" {
		groupName = originalTerm
	}
	destination, destinationErr := s.destinationService.ResolveDestination(ctx, groupName, driveFolderID)
	resolvedFolderID := ""
	if destinationErr == nil {
		resolvedFolderID = destination.FolderID
	}

	runActiveKey, dedupErr := RunDedupKey(normalizedTerm, driveFolderID, "", false, limit)
	if dedupErr != nil {
		s.log.Warn("artlist run dedup key construction failed in orchestrator (godlike/07 fail-closed)",
			zap.String("term", normalizedTerm),
			zap.String("root_folder_id", driveFolderID),
			zap.Int("limit", limit),
			zap.Error(dedupErr),
		)
		return liveResp, nil, dedupErr
	}

	enqueued, enqueueErr := s.jobsSvc.Enqueue(ctx, &jobs.EnqueueRequest{
		Type:       "media.artlist",
		Payload:    (&JobCodec{}).PayloadFromRequest(&RunTagRequest{Term: normalizedTerm, Limit: limit, RootFolderID: driveFolderID}),
		ActiveKey:  runActiveKey,
		MaxRetries: 3,
	})
	if enqueueErr != nil {
		s.log.Warn("artlist discovery queued save but failed to enqueue job", zap.String("term", normalizedTerm), zap.Error(enqueueErr))
		return liveResp, nil, nil
	}

	runResp := JobToRunTagResponse(enqueued)
	if runResp != nil {
		runResp.TagFolderID = resolvedFolderID
		if resolvedFolderID != "" {
			runResp.TagFolderLink = "https://drive.google.com/drive/folders/" + resolvedFolderID
		}
	}
	return liveResp, runResp, nil
}

func (ss *SearchService) SearchClips(ctx context.Context, term string) []*asset.Asset {
	s := ss.service
	term = normalizeSearchTerm(term)
	clips, err := s.assetStore.SearchClips(ctx, "artlist", term)
	if err != nil {
		s.log.Error("failed to search clips", zap.Error(err), zap.String("term", term))
		return nil
	}
	return append([]*asset.Asset(nil), clips...)
}

func (ss *SearchService) searchLiveWithFallbacks(ctx context.Context, term string, limit int, preferRemote, forceRefresh bool, mode SearchMode) ([]Candidate, error) {
	mode = ResolveSearchMode(mode, preferRemote)
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

	chain := ss.buildSearcherChain(preferRemote, mode)
	if chain == nil {
		return nil, fmt.Errorf("no search providers configured")
	}
	candidates, err := chain.Search(ctx, SearchRequest{
		Term:         normalizedTerm,
		Limit:        limit,
		Mode:         mode,
		PreferRemote: preferRemote,
		ForceRefresh: forceRefresh,
	})
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

	filterReq := DefaultFilterRequestForTerm(normalizedTerm)
	filterReq.Limit = limit
	filtered, filterStats := DefaultRelevanceFilter(filterReq, candidates)
	LogFilterStats(ss.service.log, filterReq, filterStats)
	return filtered, nil
}

func (ss *SearchService) buildSearcherChain(preferRemote bool, requestedMode ...SearchMode) *SearcherFallbackChain {
	s := ss.service
	mode := SearchMode("")
	if len(requestedMode) > 0 {
		mode = requestedMode[0]
	}
	mode = ResolveSearchMode(mode, preferRemote)
	if mode == SearchModeCatalogOnly {
		if s.scraperSearcher == nil {
			return nil
		}
		return NewSearcherFallbackChain(s.scraperSearcher)
	}
	var searchers []Searcher
	if !preferRemote {
		localSearcher := s.localSearcher
		// Compatibility for unit-level constructions that still provide only
		// AssetStore. Production composition injects LocalSearcher explicitly.
		if localSearcher == nil && s.assetStore != nil {
			localSearcher = NewSQLiteSearcher(s.assetStore)
		}
		if localSearcher != nil {
			searchers = append(searchers, localSearcher)
		}
	}

	strategy := ss.searchStrategy
	if !strategy.IsValid() {
		strategy = DefaultArtlistSearchStrategy
	}
	infraSearchers := ResolveSearcherChain(strategy, s.scraperSearcher, s.pixabaySearcher, s.pexelsSearcher)
	for _, searcher := range infraSearchers {
		if !preferRemote && searcher == s.scraperSearcher {
			ttlHours := 24
			if s.cfg != nil && s.cfg.External.ArtlistLiveSearchCacheTTLHours > 0 {
				ttlHours = s.cfg.External.ArtlistLiveSearchCacheTTLHours
			}
			cachedSearcher := NewCachedSearcher(s.scraperSearcher, s.liveCache, ttlHours, s.log)
			if s.jobsSvc != nil {
				cachedSearcher = NewCachedSearcher(s.scraperSearcher, s.liveCache, ttlHours, s.log, s.jobsSvc)
			}
			searchers = append(searchers, cachedSearcher)
		} else {
			searchers = append(searchers, searcher)
		}
	}
	if len(searchers) == 0 {
		return nil
	}
	return NewSearcherFallbackChain(searchers...)
}

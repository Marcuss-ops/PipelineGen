package artlist

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
	"go.uber.org/zap"
)

// SearchService coordinates Artlist database and live-provider searches.
type SearchService struct {
	service         *Service
	assetRepo       asset.Repository
	dispatcher      Dispatcher
	scraperSearcher Searcher
	pixabaySearcher Searcher
	pexelsSearcher  Searcher
	searchStrategy  ArtlistSearchStrategy
	cfg             *config.Config
	log             *zap.Logger
}

func (ss *SearchService) SetAssetRepo(r asset.Repository) {
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
		if converted := toDomain(clip); converted != nil {
			resp.Clips = append(resp.Clips, *converted)
		}
	}
	resp.Source = "database"
	return resp, nil
}

func (ss *SearchService) SearchLive(ctx context.Context, term string, limit int, preferRemote bool) ([]Candidate, error) {
	return ss.searchLiveWithFallbacks(ctx, term, limit, preferRemote)
}

func (ss *SearchService) SearchLiveAndSave(ctx context.Context, originalTerm string, limit int) (*SearchResponse, error) {
	s := ss.service
	normalizedTerm := normalizeSearchTerm(originalTerm)
	candidates, err := ss.SearchLive(ctx, normalizedTerm, limit, false)
	if err != nil {
		return nil, err
	}

	resp := &SearchResponse{
		OK:     true,
		Term:   originalTerm,
		Source: "live",
		Clips:  make([]asset.Asset, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		if candidate.ID == "" {
			s.log.Warn("skipping candidate with missing id", zap.String("title", candidate.Title))
			continue
		}

		providerTags := make([]string, len(candidate.Keywords))
		copy(providerTags, candidate.Keywords)

		pageURL := candidate.PageURL
		if pageURL == "" && candidate.ID != "" {
			pageURL = "https://artlist.io/stock-footage/clip/" + candidate.ID
		}
		clip := candidateToAsset(&candidate, pageURL)
		clip.SetDownloadLink(candidate.SourceRef)

		// Inject the original search term into the indexed tags/search terms
		// while keeping provider_tags as the pure provider-supplied keywords.
		clip.ProviderTags = deduplicateStrings(append([]string{originalTerm}, providerTags...))
		clip.RebuildTags()
		clip.SearchTerms = deduplicateStrings(append([]string{originalTerm}, providerTags...))
		if clip.Metadata == nil {
			clip.Metadata = map[string]any{}
		}
		clip.Metadata["provider_tags"] = providerTags
		clip.Metadata["provider_categories"] = candidate.Categories
		clip.Metadata["metadata_origin"] = "artlist"

		if s.assetStore != nil {
			if existing, getErr := s.assetStore.Get(ctx, clip.ID); getErr == nil && existing != nil {
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

		if ss.dispatcher == nil {
			return nil, ErrAssetMutationDispatcherUnavailable
		}
		upsertErr := ss.dispatcher.SaveDiscoveredAsset(ctx, clip, asset.StateStaging, asset.StateDiscovered)
		if upsertErr == nil {
			if converted := toDomain(clip); converted != nil {
				resp.Clips = append(resp.Clips, *converted)
			}
			if s.assetStore != nil {
				searchText := clip.Name + " " + originalTerm
				if updateErr := s.assetStore.UpdateSearchTerms(ctx, clip.ID, "artlist", clip.Name, clip.Tags, searchText); updateErr != nil {
					s.log.Debug("failed to update search terms for clip", zap.String("clip_id", clip.ID), zap.Error(updateErr))
				}
			}
		}
	}
	return resp, nil
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
	return toDomainPtrSlice(clips)
}

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

	filterReq := DefaultFilterRequestForTerm(normalizedTerm)
	filterReq.Limit = limit
	filtered, filterStats := DefaultRelevanceFilter(filterReq, candidates)
	LogFilterStats(ss.service.log, filterReq, filterStats)
	return filtered, nil
}

func (ss *SearchService) buildSearcherChain(preferRemote bool) *SearcherFallbackChain {
	s := ss.service
	var searchers []Searcher
	if !preferRemote && s.assetStore != nil {
		searchers = append(searchers, NewDBSearcher(s.assetStore))
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
			searchers = append(searchers, NewCachedSearcher(s.scraperSearcher, s.liveCache, ttlHours, s.log))
		} else {
			searchers = append(searchers, searcher)
		}
	}
	if len(searchers) == 0 {
		return nil
	}
	return NewSearcherFallbackChain(searchers...)
}

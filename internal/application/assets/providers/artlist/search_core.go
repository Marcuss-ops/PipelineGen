package artlist

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
	"go.uber.org/zap"
)

// SearchService gestisce tutte le operazioni di ricerca Artlist.
type SearchService struct {
	service *Service
	// assetRepo is the canonical writer (PR12b). Late-bound via SetAssetRepo.
	assetRepo asset.Repository
	// PR2: injected Searcher implementations from infrastructure.
	// nil means that level is skipped in the fallback chain.
	scraperSearcher Searcher
	pixabaySearcher Searcher
	pexelsSearcher  Searcher
	cfg             *config.Config
	log             *zap.Logger
}

// SetAssetRepo injects the canonical assetRepo.
func (ss *SearchService) SetAssetRepo(r asset.Repository) {
	ss.assetRepo = r
}

// NewSearchService creates a new SearchService wired to the Service.
func NewSearchService(s *Service) *SearchService {
	return &SearchService{service: s}
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
func (ss *SearchService) SearchLive(ctx context.Context, term string, limit int) ([]Candidate, error) {
	return ss.searchLiveWithFallbacks(ctx, term, limit)
}

// SearchLiveAndSave esegue una ricerca live e salva i risultati nel database.
func (ss *SearchService) SearchLiveAndSave(ctx context.Context, originalTerm string, limit int) (*SearchResponse, error) {
	s := ss.service
	normalizedTerm := normalizeSearchTerm(originalTerm)
	candidates, err := ss.SearchLive(ctx, normalizedTerm, limit)
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

		if err := s.assetStore.Upsert(ctx, clip); err == nil {
			if a := toDomain(clip); a != nil {
				resp.Clips = append(resp.Clips, *a)
			}

			searchText := clip.Name + " " + originalTerm
			if updateErr := s.assetStore.UpdateSearchTerms(ctx, clip.ID, "artlist", clip.Name, clip.Tags, searchText); updateErr != nil {
				s.log.Debug("failed to update search terms for clip", zap.String("clip_id", clip.ID), zap.Error(updateErr))
			}

			if s.metadataWriter != nil {
				s.metadataWriter.EnrichAsync(ctx, clip, normalizedTerm)
			}
		}
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

		job, err := s.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
			Type:       "artlist.run",
			Payload:    (&JobCodec{}).PayloadFromRequest(&RunTagRequest{Term: normalizedTerm, Limit: limit, RootFolderID: driveFolderID}),
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

// UpsertClip inserts or updates a clip in the database.
func (ss *SearchService) UpsertClip(ctx context.Context, clip *asset.Asset) error {
	if ss.assetRepo != nil {
		return ss.assetRepo.Upsert(ctx, clip)
	}
	s := ss.service
	if s.assetStore != nil {
		return s.assetStore.Upsert(ctx, clip)
	}
	return nil
}

// searchLiveWithFallbacks orchestrates the fallback chain using the
// Searcher port. Implementations come from infrastructure:
//   - DB: in-memory indexed terms (fast)
//   - CachedSearcher: wraps infrastructure/scraper with L1/L2 cache
//   - Pixabay HTTP (free fallback)
//   - Pexels HTTP (free fallback)
func (ss *SearchService) searchLiveWithFallbacks(ctx context.Context, term string, limit int) ([]Candidate, error) {
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

	chain := ss.buildSearcherChain()
	if chain == nil {
		return nil, fmt.Errorf("no search providers configured")
	}

	candidates, err := chain.Search(ctx, SearchRequest{Term: normalizedTerm, Limit: limit})
	if err != nil {
		ss.service.log.Warn("all search providers failed",
			zap.String("term", term),
			zap.Error(err),
		)
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no results from any search provider for %q", normalizedTerm)
	}
	return candidates, nil
}

// buildSearcherChain constructs the Searcher fallback chain from the service
// configuration. Infrastructure searchers are injected here so the application
// layer stays decoupled from concrete implementations.
func (ss *SearchService) buildSearcherChain() *SearcherFallbackChain {
	s := ss.service

	var searchers []Searcher

	// Level 1: DB search (fast, indexed).
	if s.assetStore != nil {
		searchers = append(searchers, NewDBSearcher(s.assetStore))
	}

	// Level 2: Cached scraper (in-memory with background refresh).
	// The infrastructure scraper.Provider satisfies Searcher directly.
	// We wrap it with CachedSearcher for L1 in-memory caching.
	if s.scraperSearcher != nil {
		ttlHours := 24
		if s.cfg != nil && s.cfg.External.ArtlistLiveSearchCacheTTLHours > 0 {
			ttlHours = s.cfg.External.ArtlistLiveSearchCacheTTLHours
		}
		cached := NewCachedSearcher(s.scraperSearcher, s.liveCache, ttlHours, s.log)
		searchers = append(searchers, cached)
	}

	// Level 3: Pixabay API (free fallback).
	if s.pixabaySearcher != nil {
		searchers = append(searchers, s.pixabaySearcher)
	}

	// Level 4: Pexels API (free fallback).
	if s.pexelsSearcher != nil {
		searchers = append(searchers, s.pexelsSearcher)
	}

	if len(searchers) == 0 {
		return nil
	}
	return NewSearcherFallbackChain(searchers...)
}

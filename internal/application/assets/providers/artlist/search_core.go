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
	// assetRepo is retained for read-only paths (Search, SearchClips).
	// QDRANT-002 close-out: write paths NO LONGER call assetRepo.Upsert
	// directly — they MUST route through dispatcher.EnqueueAndIndex so
	// media_assets and outbox_events commit atomically.
	assetRepo asset.Repository
	// dispatcher is the canonical outbox dispatcher port (QDRANT-002
	// close-out). REQUIRED for every write path (SearchLiveAndSave +
	// UpsertClip). nil at construction time is a fatal config bug —
	// NewSearchService enforces non-nil with a clear error.
	dispatcher Dispatcher
	// PR2: injected Searcher implementations from infrastructure.
	// nil means that level is skipped in the fallback chain.
	scraperSearcher Searcher
	pixabaySearcher Searcher
	pexelsSearcher  Searcher
	cfg             *config.Config
	log             *zap.Logger
}

// SetAssetRepo injects the canonical assetRepo (read-only paths).
func (ss *SearchService) SetAssetRepo(r asset.Repository) {
	ss.assetRepo = r
}

// NewSearchService creates a new SearchService wired to the Service.
//
// QDRANT-002 close-out (June 2026): dispatcher is REQUIRED. The legacy
// nil-dispatcher-equals-legacy-Upsert fallback has been REMOVED. Every
// caller must wire the canonical outbox dispatcher; production wiring
// lives in BuildProcessBundle → BuildOutboxBundle → artlist.NewService.
func NewSearchService(s *Service, dispatcher Dispatcher) (*SearchService, error) {
	if dispatcher == nil {
		return nil, fmt.Errorf("artlist.NewSearchService: dispatcher is required (QDRANT-002 close-out — every write must route through outbox.Dispatcher.EnqueueAndIndex)")
	}
	return &SearchService{service: s, dispatcher: dispatcher}, nil
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
//
// QDRANT-002 close-out: dispatcher is REQUIRED. Saved candidates are
// persisted via dispatcher.EnqueueAndIndex so the media_assets UPSERT
// and the outbox_events INSERT commit in a single atomic tx. The
// previous `if ss.dispatcher != nil { EnqueueAndIndex } else { raw
// assetStore.Upsert }` dual-path has been eliminated — there is no
// canonical way to ingest a clip without the outbox event, so any
// caller that somehow shows up with a nil dispatcher is a wiring bug
// caught at construction time (NewSearchService returns an error
// instead of producing a zero-dispatcher instance).
func (ss *SearchService) SearchLiveAndSave(ctx context.Context, originalTerm string, limit int) (*SearchResponse, error) {
	if ss.dispatcher == nil {
		return nil, fmt.Errorf("artlist.SearchLiveAndSave: dispatcher is nil — invariant broken (NewSearchService must reject nil at construction; this is defensive only)")
	}
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

		// QDRANT-002 close-out: dispatcher is the canonical write path.
		// No legacy fallback to s.assetStore.Upsert — every ingest
		// MUST emit an asset.index.requested.v1 outbox event so
		// IndexClip runs via the outbox pool (atomic + retry-safe).
		contentHash := clip.FileHash()
		if contentHash == "" {
			contentHash = clip.ID
		}
		upsertErr := ss.dispatcher.EnqueueAndIndex(ctx, clip, contentHash)

		if upsertErr == nil {
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
//
// QDRANT-002 close-out (June 2026): ALWAYS routes through the canonical
// dispatcher.EnqueueAndIndex. The previous dual-path (assetRepo.Upsert
// when wired, assetStore.Upsert as silent fallback) has been REMOVED
// — a clip without an outbox event leaves the Qdrant vector index
// permanently out of sync and is forbidden by the canonical ingest
// contract (see internal/infrastructure/database/sqlite/outbox/repository.go).
//
// callers MUST supply clip.FileHash() != "" for the idempotency_key
// dedup column. When FileHash is empty we fall back to clip.ID so the
// event_key column is never empty (event_key has UNIQUE constraint).
func (ss *SearchService) UpsertClip(ctx context.Context, clip *asset.Asset) error {
	if ss.dispatcher == nil {
		return fmt.Errorf("artlist.UpsertClip: dispatcher is nil — invariant broken (NewSearchService must reject nil at construction)")
	}
	if clip == nil {
		return fmt.Errorf("artlist.UpsertClip: clip is nil")
	}
	contentHash := clip.FileHash()
	if contentHash == "" {
		contentHash = clip.ID
	}
	return ss.dispatcher.EnqueueAndIndex(ctx, clip, contentHash)
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

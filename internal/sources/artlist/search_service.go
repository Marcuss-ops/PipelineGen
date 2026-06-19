package artlist

import (
	"context"
	"strings"

	"go.uber.org/zap"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// SearchService gestisce tutte le operazioni di ricerca Artlist.
type SearchService struct {
	service *Service
	// assetRepo is the canonical writer (PR12b). Late-bound via SetAssetRepo.
	// When nil, UpsertClip falls back to the legacy sqlite.ClipsRepository path so
	// existing callers/tests continue to work. When wired, UpsertClip converts
	// the legacy *models.MediaAsset to *asset.MediaAsset via toDomain, then
	// routes through assetrepo.Upsert — which writes both the new canonical
	// columns and the legacy columns in the same row so legacy readers
	// (sqlite.ClipsRepository) continue to see the data unchanged.
	assetRepo assets.Repository
}

// SetAssetRepo injects the canonical assetRepo. Mirrors the
// SetDispatcher pattern already used in youtube.Service. Idempotent and
// safe to call once during composition root wiring.
func (ss *SearchService) SetAssetRepo(r assets.Repository) {
	ss.assetRepo = r
}

// NewSearchService crea una nuova istanza di SearchService.
func NewSearchService(s *Service) *SearchService {
	return &SearchService{service: s}
}

// ScraperClip represents a clip from the node scraper output
type ScraperClip struct {
	ClipID      string   `json:"clip_id"`
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Name        string   `json:"name"`
	PrimaryURL  string   `json:"primary_url"`
	StreamURLs  []string `json:"stream_urls"`
	ClipPageURL string   `json:"clip_page_url"`
}

// ScraperResponse represents the full response from the node scraper
type ScraperResponse struct {
	Ok        bool          `json:"ok"`
	Term      string        `json:"term"`
	Clips     []ScraperClip `json:"clips"`
	SearchURL string        `json:"search_url"`
	Saved     int           `json:"saved"`
}

// Search esegue una ricerca di clip nel database Artlist.
func (ss *SearchService) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	s := ss.service
	term := normalizeSearchTerm(req.Term)
	resp := &SearchResponse{OK: true, Term: term}

	if term == "" {
		return resp, nil
	}

	clipsList, err := s.artlistRepo.SearchClips(ctx, "artlist", term)
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

	resp.Clips = make([]assets.Asset, 0, len(clipsList))
	for _, c := range clipsList {
		if a := toDomain(c); a != nil {
			resp.Clips = append(resp.Clips, *a)
		}
	}
	resp.Source = "database"

	return resp, nil
}

// SearchLive esegue una ricerca live tramite scraper Node.js.
func (ss *SearchService) SearchLive(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	return ss.searchLiveWithFallbacks(ctx, term, limit)
}

// SearchLiveAndSave esegue una ricerca live e salva i risultati nel database.
// originalTerm è la query piena dell'utente; viene usata per tag e search terms.
// Il termine normalizzato (max 4 parole) viene usato per la ricerca scraper.
func (ss *SearchService) SearchLiveAndSave(ctx context.Context, originalTerm string, limit int) (*SearchResponse, error) {
	s := ss.service
	normalizedTerm := normalizeSearchTerm(originalTerm)
	clips, err := ss.SearchLive(ctx, normalizedTerm, limit)
	if err != nil {
		return nil, err
	}

	resp := &SearchResponse{OK: true, Term: originalTerm, Source: "live", Clips: make([]assets.Asset, 0, len(clips))}

	for _, c := range clips {
		// Handle both clip_id (new format) and id (old format)
		id := defaults.String(c.ClipID, c.ID)
		if id == "" {
			s.log.Warn("skipping clip with missing id", zap.String("clip_id", c.ClipID), zap.String("title", c.Title))
			continue
		}

		name := defaults.String(c.Title, c.Name)
		if name == "" {
			name = id
		}

		// Store the original full query in tags/search_terms so the user's
		// intent is never lost.  The normalized (shorter) term is used only
		// for the search/cache key, not for metadata.
		clip := &assets.Asset{
			ID:          id,
			Name:        name,
			Source:      assets.Source("artlist"),
			MediaType:   assets.MediaType("video"), // Artlist content is always video
			Tags:        []string{originalTerm},
			SearchTerms: []string{originalTerm},
			SourceURL:   c.PrimaryURL,
			ClipPageURL: c.ClipPageURL,
		}
		clip.SetDownloadLink(c.PrimaryURL)

		if existing, err := s.artlistRepo.Get(ctx, clip.ID); err == nil && existing != nil {
			// Preserve existing Drive metadata (upload results)
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
			// Preserve DownloadLink only if it's an Artlist CDN URL (not a Google Drive link).
			// When a clip is already uploaded to Drive, DownloadLink gets overwritten with
			// the Drive download URL, which breaks the scraper's isArtlistURL() check and
			// causes all pipeline items to fail. The fresh Artlist primary_url from the
			// current search should always take precedence for the download step.
			if existing.DownloadLink() != "" && !strings.Contains(existing.DownloadLink(), "drive.google.com") {
				clip.SetDownloadLink(existing.DownloadLink())
			}
			if existing.ClipPageURL != "" {
				clip.ClipPageURL = existing.ClipPageURL
			}
		}

		if err := s.artlistRepo.Upsert(ctx, clip); err == nil {
			if a := toDomain(clip); a != nil {
				resp.Clips = append(resp.Clips, *a)
			}

			// Update search terms index: use normalizedTerm for indexed search
			// (faster AND matching) but also include originalTerm for broader LIKE hits.
			searchText := clip.Name + " " + originalTerm
			if updateErr := s.artlistRepo.UpdateSearchTerms(ctx, clip.ID, "artlist", clip.Name, clip.Tags, searchText); updateErr != nil {
				s.log.Debug("failed to update search terms for clip", zap.String("clip_id", clip.ID), zap.Error(updateErr))
			}

			// Arricchimento semantico in background: popola search_text + embedding_json
			// senza bloccare il flusso di risposta all'utente.
			// Dopo l'enrichment, semantic_enricher.go chiama UpdateSearchTerms di nuovo
			// con i termini ricchi del tagger.
			if s.semanticEnricher != nil {
				s.semanticEnricher.EnrichAsync(ctx, clip, normalizedTerm)
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

		// Synchronously resolve destination folder so we can return the link immediately
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

		// Return job info with resolved folder details
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
func (ss *SearchService) SearchClips(ctx context.Context, term string) []*assets.Asset {
	s := ss.service
	term = normalizeSearchTerm(term)
	clips, err := s.artlistRepo.SearchClips(ctx, "artlist", term)
	if err != nil {
		s.log.Error("failed to search clips", zap.Error(err), zap.String("term", term))
		return nil
	}
	return toDomainPtrSlice(clips)
}

// UpsertClip inserts or updates a clip in the database.
//
// PR12b: when ss.assetRepo is wired (via SetAssetRepo), this method routes
// through toDomain + assetrepo.Upsert instead of the legacy sqlite.ClipsRepository path.
// assetrepo.Upsert writes both new canonical columns (lifecycle_state, category,
// quality_score, scene_type, ...) and the legacy columns (drive_file_id,
// drive_link, download_link, local_path, file_hash, ...) in the same row, so
// legacy readers (sqlite.ClipsRepository.Get) still observe unchanged data. The
// asset.upserted outbox event is emitted in the same transaction so downstream
// consumers (semantic_enricher, dispatcher, search indexers) see the canonical
// pointer.
//
// When ss.assetRepo is nil (default boot path), behavior is unchanged:
// clip is written via sqlite.ClipsRepository.UpsertClip with no outbox event.
func (ss *SearchService) UpsertClip(ctx context.Context, clip *assets.Asset) error {
	if ss.assetRepo != nil {
		return ss.assetRepo.Upsert(ctx, clip)
	}
	s := ss.service
	return s.artlistRepo.Upsert(ctx, clip)
}

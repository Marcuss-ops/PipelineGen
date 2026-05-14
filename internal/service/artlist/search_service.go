package artlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/pkg/models"
)


// SearchService gestisce tutte le operazioni di ricerca Artlist.
type SearchService struct {
	service *Service
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
	term := NormalizeSearchTerm(req.Term)
	resp := &SearchResponse{OK: true, Term: term}

	if term == "" {
		return resp, nil
	}

	clipsList, err := s.artlistRepo.SearchClips(ctx, term)
	if err != nil {
		resp.Error = err.Error()
		return resp, err
	}

	// Apply limit
	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	if len(clipsList) > limit {
		clipsList = clipsList[:limit]
	}

	resp.Clips = make([]models.Clip, 0, len(clipsList))
	for _, c := range clipsList {
		resp.Clips = append(resp.Clips, *c)
	}
	resp.Source = "database"

	return resp, nil
}

// SearchLive esegue una ricerca live tramite scraper Node.js.
func (ss *SearchService) SearchLive(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	s := ss.service
	term = NormalizeSearchTerm(term)
	if term == "" {
		return nil, fmt.Errorf("term is required")
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	if strings.TrimSpace(s.nodeScraperDir) == "" {
		return nil, fmt.Errorf("node scraper directory is not configured")
	}

	scraperDir := s.nodeScraperDir
	if absDir, err := filepath.Abs(scraperDir); err == nil {
		scraperDir = absDir
	}
	scriptPath := filepath.Join(scraperDir, "artlist_search.js")

	if _, err := exec.LookPath("node"); err != nil {
		return nil, fmt.Errorf("node not found in PATH")
	}

	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	args := []string{scriptPath, "--term", term, "--limit", strconv.Itoa(limit)}

	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = scraperDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	s.log.Info("Running live Artlist search", zap.String("term", term), zap.Int("limit", limit), zap.String("script_path", scriptPath))

	if err := cmd.Run(); err != nil {
		s.log.Error("Artlist scraper failed", zap.Error(err), zap.String("stderr", stderr.String()))
		return nil, fmt.Errorf("scraper failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	stdoutStr := stdout.String()
	s.log.Info("Scraper raw output received", zap.Int("bytes", len(stdoutStr)))

	var response ScraperResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		s.log.Error("failed to decode scraper response", zap.Error(err), zap.String("output", stdoutStr))
		return nil, fmt.Errorf("failed to decode scraper response: %w", err)
	}

	s.log.Info("Live Artlist search completed", zap.String("term", term), zap.Int("clips_found", len(response.Clips)))

	return response.Clips, nil
}

// SearchLiveAndSave esegue una ricerca live e salva i risultati nel database.
func (ss *SearchService) SearchLiveAndSave(ctx context.Context, term string, limit int) (*SearchResponse, error) {
	s := ss.service
	term = NormalizeSearchTerm(term)
	clips, err := ss.SearchLive(ctx, term, limit)
	if err != nil {
		return nil, err
	}

	resp := &SearchResponse{OK: true, Term: term, Source: "live", Clips: make([]models.Clip, 0, len(clips))}

	for _, c := range clips {
		// Handle both clip_id (new format) and id (old format)
		id := c.ClipID
		if id == "" {
			id = c.ID
		}
		if id == "" {
			s.log.Warn("skipping clip with missing id", zap.String("clip_id", c.ClipID), zap.String("title", c.Title))
			continue
		}

		name := c.Title
		if name == "" {
			name = c.Name
		}
		if name == "" {
			name = id
		}

		clip := &models.Clip{
			ID:           id,
			Name:         name,
			Tags:         []string{term},
			SearchTerms:  []string{term}, // Frasi di riferimento
			ExternalURL:  c.PrimaryURL,
			DownloadLink: c.PrimaryURL,
		}

		if existing, err := s.artlistRepo.GetClip(ctx, clip.ID); err == nil && existing != nil {
			// Preserve existing fields
			if existing.LocalPath != "" {
				clip.LocalPath = existing.LocalPath
			}
			if existing.FileHash != "" {
				clip.FileHash = existing.FileHash
			}
			if existing.DriveLink != "" {
				clip.DriveLink = existing.DriveLink
			}
			if existing.DriveFileID != "" {
				clip.DriveFileID = existing.DriveFileID
			}
			if existing.DownloadLink != "" {
				clip.DownloadLink = existing.DownloadLink
			}
		}

		if err := s.artlistRepo.UpsertClip(ctx, clip); err == nil {
			resp.Clips = append(resp.Clips, *clip)
		}
	}

	return resp, nil
}

// DiscoverAndQueueRun scopre clip e accoda un'esecuzione.
func (ss *SearchService) DiscoverAndQueueRun(ctx context.Context, term string, limit int) (*SearchResponse, *RunTagResponse, error) {
	s := ss.service
	term = NormalizeSearchTerm(term)
	liveResp, err := ss.SearchLiveAndSave(ctx, term, limit)
	if err != nil {
		return nil, nil, err
	}

	if liveResp == nil || len(liveResp.Clips) == 0 {
		return liveResp, nil, nil
	}

	// Enqueue processing job through common jobs service
	if s.jobsSvc != nil {
		driveFolderID := ""
		if s.cfg != nil {
			driveFolderID = s.cfg.Harvester.DriveFolderID
		}
		
		// Synchronously resolve destination folder so we can return the link immediately
		groupName := "Artlist"
		if term != "" {
			groupName = term
		}

		dest, err := s.destinationService.ResolveDestination(ctx, groupName, driveFolderID)
		resolvedFolderID := ""
		if err == nil {
			resolvedFolderID = dest.FolderID
		}

		job, err := s.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{

			Type:       models.JobTypeArtlistRun,
			Payload:    (&RunTagRequest{Term: term, Limit: limit, RootFolderID: driveFolderID}).ToMap(),
			MaxRetries: 3,
			ActiveKey:  RunDedupKey(term, driveFolderID, "", false),
		})
		if err != nil {
			s.log.Warn("artlist discovery queued save but failed to enqueue job", zap.String("term", term), zap.Error(err))
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
func (ss *SearchService) SearchClips(ctx context.Context, term string) []*models.Clip {
	s := ss.service
	clips, err := s.artlistRepo.SearchClips(ctx, term)
	if err != nil {
		s.log.Error("failed to search clips", zap.Error(err), zap.String("term", term))
		return nil
	}
	return clips
}

// UpsertClip inserts or updates a clip in the database
func (ss *SearchService) UpsertClip(ctx context.Context, clip *models.Clip) error {
	s := ss.service
	return s.artlistRepo.UpsertClip(ctx, clip)
}

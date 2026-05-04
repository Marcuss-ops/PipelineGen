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

func (s *Service) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	resp := &SearchResponse{OK: true, Term: req.Term}

	if req.Term == "" {
		return resp, nil
	}

	clipsList, err := s.artlistRepo.SearchClips(ctx, req.Term)
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

func (s *Service) SearchLive(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	term = strings.TrimSpace(term)
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

func (s *Service) SearchLiveAndSave(ctx context.Context, term string, limit int) (*SearchResponse, error) {
	clips, err := s.SearchLive(ctx, term, limit)
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

func (s *Service) DiscoverAndQueueRun(ctx context.Context, term string, limit int) (*SearchResponse, *RunTagResponse, error) {
	liveResp, err := s.SearchLiveAndSave(ctx, term, limit)
	if err != nil {
		return nil, nil, err
	}

	if liveResp == nil || len(liveResp.Clips) == 0 {
		return liveResp, nil, nil
	}

	// Enqueue processing job through common jobs service
	if s.jobsSvc != nil {
		driveFolderID := ""
		if s.driveService != nil {
			driveFolderID = s.driveService.GetDriveFolderID()
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
		// Return job info - caller can poll for completion if needed
		return liveResp, JobToRunTagResponse(job), nil
	}

	return liveResp, nil, nil
}

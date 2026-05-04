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
	"velox/go-master/pkg/models"
)

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

func (s *Service) SearchLive(ctx context.Context, term string, limit int) ([]map[string]interface{}, error) {
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

	var payload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		s.log.Error("failed to decode scraper response", zap.Error(err), zap.String("output", stdoutStr))
		return nil, fmt.Errorf("failed to decode scraper response: %w", err)
	}

	clipsRaw, ok := payload["clips"]
	if !ok {
		return []map[string]interface{}{}, nil
	}

	clipsJSON, err := json.Marshal(clipsRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal clips: %w", err)
	}

	var clips []map[string]interface{}
	if err := json.Unmarshal(clipsJSON, &clips); err != nil {
		return nil, fmt.Errorf("failed to unmarshal clips: %w", err)
	}

	s.log.Info("Live Artlist search completed", zap.String("term", term), zap.Int("clips_found", len(clips)))

	return clips, nil
}

func (s *Service) SearchLiveAndSave(ctx context.Context, term string, limit int) (*SearchResponse, error) {
	clips, err := s.SearchLive(ctx, term, limit)
	if err != nil {
		return nil, err
	}

	resp := &SearchResponse{OK: true, Term: term, Source: "live", Clips: make([]models.Clip, 0, len(clips))}

	for _, c := range clips {
		clip := mapToModelClip(c, term)
		if clip == nil {
			continue
		}
		if existing, err := s.artlistRepo.GetClip(ctx, clip.ID); err == nil && existing != nil {
			clip = preserveExistingClipFields(clip, existing)
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

	driveFolderID := ""
	if s.driveService != nil {
		driveFolderID = s.driveService.GetDriveFolderID()
	}
	runResp, err := s.StartRunTag(ctx, &RunTagRequest{Term: term, Limit: limit, RootFolderID: driveFolderID})
	if err != nil {
		s.log.Warn("artlist discovery queued save but failed to start run", zap.String("term", term), zap.Error(err))
		return liveResp, nil, nil
	}

	return liveResp, runResp, nil
}

package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/pkg/googleaccounting"
)

// GenerateVideoAI generates a video via Google Vids automation
func (s *Service) GenerateVideoAI(ctx context.Context, prompt, style string) (string, error) {
	if strings.TrimSpace(s.googleAccountingURL) == "" {
		return "", fmt.Errorf("google accounting server url not configured")
	}

	// Apply style if provided
	if s.styleRegistry != nil && style != "" {
		prompt = s.styleRegistry.ApplyStyle(prompt, style)
	}

	// For Google Vids, we use a configured project_id or "new"
	videoID := s.vidsProjectID
	if videoID == "" {
		videoID = "new"
	}

	reqBody := googleaccounting.GenerateRequest{
		VideoID:  videoID,
		Prompt:   prompt,
		Headless: true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal google vids request failed: %w", err)
	}

	startURL := strings.TrimRight(s.googleAccountingURL, "/") + "/generate-vids-video"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create google vids request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("google vids start request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google vids start failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var startResp googleaccounting.StartResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return "", fmt.Errorf("decode google vids start response failed: %w", err)
	}
	if strings.TrimSpace(startResp.JobID) == "" {
		return "", fmt.Errorf("google vids start response missing job_id")
	}

	s.log.Info("Google Vids job started", zap.String("job_id", startResp.JobID), zap.String("prompt", prompt))

	job, err := s.waitForGoogleJob(ctx, startResp.JobID)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(job.FilePath) == "" {
		return "", fmt.Errorf("google vids completed without file path")
	}

	// Resolve the absolute path
	resolved := job.FilePath
	if !filepath.IsAbs(resolved) {
		if s.googleAccountingDir != "" {
			resolved = filepath.Join(s.googleAccountingDir, resolved)
		} else if s.gaDownloadDir != "" {
			baseDir := s.gaDownloadDir
			if !filepath.IsAbs(baseDir) && s.imagesDir != "" {
				baseDir = filepath.Join(s.imagesDir, baseDir)
			}
			resolved = filepath.Join(baseDir, resolved)
		}
	}

	// Check if file exists
	if _, err := os.Stat(resolved); err != nil {
		return "", fmt.Errorf("generated video file not found at %s: %w", resolved, err)
	}

	s.log.Info("Google Vids video generated and verified", zap.String("path", resolved))
	return resolved, nil
}

// GenerateAvatarVideo generates an AI Talking Head video via Google Vids
func (s *Service) GenerateAvatarVideo(ctx context.Context, script, avatarID string) (string, error) {
	if strings.TrimSpace(s.googleAccountingURL) == "" {
		return "", fmt.Errorf("google accounting server url not configured")
	}

	videoID := s.vidsProjectID
	if videoID == "" {
		videoID = "new"
	}

	reqBody := googleaccounting.AvatarRequest{
		VideoID:  videoID,
		Script:   script,
		AvatarID: avatarID,
		Headless: true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal avatar request failed: %w", err)
	}

	startURL := strings.TrimRight(s.googleAccountingURL, "/") + "/generate-avatar-video"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create avatar request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("avatar start request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("avatar start failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var startResp googleaccounting.StartResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return "", fmt.Errorf("decode avatar start response failed: %w", err)
	}

	s.log.Info("Avatar job started", zap.String("job_id", startResp.JobID))

	job, err := s.waitForGoogleJob(ctx, startResp.JobID)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(job.FilePath) == "" {
		return "", fmt.Errorf("avatar generation completed without file path")
	}

	// Resolve path
	resolved := job.FilePath
	if !filepath.IsAbs(resolved) {
		if s.googleAccountingDir != "" {
			resolved = filepath.Join(s.googleAccountingDir, resolved)
		} else if s.gaDownloadDir != "" {
			baseDir := s.gaDownloadDir
			if !filepath.IsAbs(baseDir) && s.imagesDir != "" {
				baseDir = filepath.Join(s.imagesDir, baseDir)
			}
			resolved = filepath.Join(baseDir, resolved)
		}
	}

	return resolved, nil
}

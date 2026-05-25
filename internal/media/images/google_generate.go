package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/storage"
	"velox/go-master/internal/pkg/googleaccounting"
)

// GenerateSmartImage tries Google image generation first and falls back to NVIDIA.
// It stores every successfully generated file using the existing ingest pipeline.
func (s *Service) GenerateSmartImage(
	ctx context.Context,
	subject string,
	topic string,
	prompts []string,
	tags []string,
	width, height int,
	model string,
	skipDrive bool,
) (*models.ImageAsset, error) {
	prompt := pickImagePrompt(subject, topic, prompts)
	if prompt == "" {
		return nil, fmt.Errorf("missing image prompt")
	}

	assets, err := s.generateGoogleFlowImages(ctx, prompt, subject, tags)
	if err == nil && len(assets) > 0 {
		return assets[0], nil
	}

	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not configured") {
		s.log.Warn("google image generation failed, falling back to NVIDIA",
			zap.String("subject", subject),
			zap.Error(err),
		)
	}

	asset, err := s.GenerateAImage(ctx, prompt, model, width, height, tags, skipDrive)
	if err != nil {
		return nil, err
	}
	return asset, nil
}

func pickImagePrompt(subject, topic string, prompts []string) string {
	for _, p := range prompts {
		if p = strings.TrimSpace(p); p != "" {
			return p
		}
	}

	subject = strings.TrimSpace(subject)
	topic = strings.TrimSpace(topic)

	switch {
	case subject != "" && topic != "":
		return fmt.Sprintf("cinematic documentary image of %s related to %s", subject, topic)
	case subject != "":
		return fmt.Sprintf("cinematic documentary image of %s", subject)
	case topic != "":
		return fmt.Sprintf("cinematic documentary image of %s", topic)
	default:
		return ""
	}
}

func (s *Service) generateGoogleFlowImages(ctx context.Context, prompt, subject string, tags []string) ([]*models.ImageAsset, error) {
	if strings.TrimSpace(s.googleAccountingURL) == "" {
		return nil, fmt.Errorf("google accounting server url not configured")
	}

	reqBody := googleaccounting.FlowImageRequest{
		Prompt:   prompt,
		Headless: true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal google flow request failed: %w", err)
	}

	startURL := strings.TrimRight(s.googleAccountingURL, "/") + "/generate-flow-images"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create google flow request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google flow start request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google flow start failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var startResp googleaccounting.StartResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return nil, fmt.Errorf("decode google flow start response failed: %w", err)
	}
	if strings.TrimSpace(startResp.JobID) == "" {
		return nil, fmt.Errorf("google flow start response missing job_id")
	}

	job, err := s.waitForGoogleJob(ctx, startResp.JobID)
	if err != nil {
		return nil, err
	}

	files := job.Files
	if len(files) == 0 && strings.TrimSpace(job.FilePath) != "" {
		files = []string{job.FilePath}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("google flow completed without files")
	}

	assets := make([]*models.ImageAsset, 0, len(files))
	slug := Slugify(subject)
	if slug == "" {
		slug = Slugify(prompt)
	}
	description := fmt.Sprintf("AI generated image via Google Flow for prompt: %s", prompt)

	for _, filePath := range files {
		resolved := strings.TrimSpace(filePath)
		if resolved == "" {
			continue
		}
		if !filepath.IsAbs(resolved) {
			// Prova googleAccountingDir (dovrebbe essere la dir absolute del download)
			if s.googleAccountingDir != "" {
				resolved = filepath.Join(s.googleAccountingDir, resolved)
			} else if s.gaDownloadDir != "" {
				// Fallback: gaDownloadDir senza googleAccountingDir
				baseDir := s.gaDownloadDir
				if !filepath.IsAbs(baseDir) && s.imagesDir != "" {
					baseDir = filepath.Join(s.imagesDir, baseDir)
				}
				resolved = filepath.Join(baseDir, resolved)
			}
		}

		content, readErr := os.ReadFile(resolved)
		if readErr != nil {
			s.log.Warn("failed to read google flow image", zap.String("path", resolved), zap.Error(readErr))
			continue
		}

		asset, ingestErr := s.IngestImage(ctx, slug, bytes.NewReader(content), filepath.Base(resolved), "google-flow", description, tags, false)
		if ingestErr != nil {
			s.log.Warn("failed to ingest google flow image", zap.String("path", resolved), zap.Error(ingestErr))
			continue
		}
		assets = append(assets, asset)
	}

	if len(assets) == 0 {
		return nil, fmt.Errorf("google flow generated no ingestible images")
	}

	return assets, nil
}

func (s *Service) waitForGoogleJob(ctx context.Context, jobID string) (*googleaccounting.Job, error) {
	statusURL := strings.TrimRight(s.googleAccountingURL, "/") + "/status/" + url.PathEscape(jobID)

	// Timeout globale di sicurezza: massimo 5 minuti
	const maxWait = 5 * time.Minute
	deadlineCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	// Poll ogni 5 secondi invece di 2 per non stressare il server Python
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		req, err := http.NewRequestWithContext(deadlineCtx, http.MethodGet, statusURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create google job status request failed: %w", err)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("google job status request failed: %w", err)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read google job status failed: %w", readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("google job status failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var job googleaccounting.Job
		if err := json.Unmarshal(body, &job); err != nil {
			return nil, fmt.Errorf("decode google job status failed: %w", err)
		}

		switch strings.ToLower(string(job.Status)) {
		case "done", "completed":
			return &job, nil
		case "failed":
			if job.Error == "" {
				job.Error = "google job failed"
			}
			return &job, fmt.Errorf("google job failed: %s", job.Error)
		}

		select {
		case <-deadlineCtx.Done():
			return nil, deadlineCtx.Err()
		case <-ticker.C:
		}
	}
}

// UploadToStyleDrive carica un'immagine su Drive in una subfolder per stile.
// Crea la struttura: {driveRoot}/{style}/{subject}/
func (s *Service) UploadToStyleDrive(ctx context.Context, asset *models.ImageAsset, style string) (string, string, error) {
	if s.mediaStore == nil {
		return "", "", fmt.Errorf("media store not configured")
	}
	if style == "" {
		return "", "", fmt.Errorf("style is required")
	}

	req := storage.AssetDestinationRequest{
		Source:    storage.SourceImage,
		MediaType: storage.MediaTypeImage,
		Style:     style,
		Subject:   asset.SubjectID,
		Hash:      asset.Hash,
		Ext:       filepath.Ext(asset.PathRel),
	}
	imagePath := filepath.Join(s.imagesDir, asset.PathRel)

	fileID, webLink, err := s.mediaStore.UploadToDrive(ctx, req, imagePath)
	if err != nil {
		return "", "", fmt.Errorf("style-based Drive upload: %w", err)
	}

	s.log.Info("image uploaded to Drive with style",
		zap.String("file_id", fileID),
		zap.String("style", style),
	)
	return webLink, fileID, nil
}

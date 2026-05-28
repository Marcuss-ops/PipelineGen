package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	style string,
	prompts []string,
	tags []string,
	width, height int,
	model string,
	skipDrive bool,
) (*models.ImageAsset, error) {
	cleanPrompt := pickImagePrompt(subject, topic, prompts)
	if cleanPrompt == "" {
		return nil, fmt.Errorf("missing image prompt")
	}

	// Apply style from registry if provided
	styledPrompt := cleanPrompt
	if s.styleRegistry != nil && style != "" {
		styledPrompt = s.styleRegistry.ApplyStyle(cleanPrompt, style)
	}

	// Step 1: Try Google Flow
	assets, err := s.generateGoogleFlowImages(ctx, cleanPrompt, styledPrompt, subject, style, tags, skipDrive)
	if err == nil && len(assets) > 0 {
		return assets[0], nil
	}
	if err != nil {
		s.log.Error("CRITICAL: Google Flow generation FAILED",
			zap.String("subject", subject),
			zap.String("error_type", fmt.Sprintf("%T", err)),
			zap.Error(err),
		)
		s.log.Info("Switching to NVIDIA fallback due to Google Flow failure")
	} else if len(assets) == 0 {
		s.log.Warn("Google Flow returned zero assets, falling back to NVIDIA")
	}

	// Step 2: Fallback to NVIDIA
	slug := Slugify(cleanPrompt)
	if len(slug) > 50 {
		slug = slug[:50]
	}
	nvidiaModel := model
	if nvidiaModel == "" {
		nvidiaModel = "flux-1-dev"
	}
	return s.GenerateStyledImage(ctx, slug, cleanPrompt, style, nvidiaModel, width, height, tags, skipDrive)
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
		return fmt.Sprintf("%s, %s, cinematic landscape", subject, topic)
	case subject != "":
		return fmt.Sprintf("%s, cinematic landscape", subject)
	case topic != "":
		return fmt.Sprintf("%s, cinematic landscape", topic)
	default:
		return ""
	}
}

func (s *Service) generateGoogleFlowImages(ctx context.Context, cleanPrompt, styledPrompt, subject, style string, tags []string, skipDrive bool) ([]*models.ImageAsset, error) {
	s.log.Info("generateGoogleFlowImages: entering function", zap.String("googleAccountingURL", s.googleAccountingURL))

	if strings.TrimSpace(s.googleAccountingURL) == "" {
		return nil, fmt.Errorf("google accounting server url not configured")
	}

	// NEW: Quick health check before starting long job
	pingReq, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(s.googleAccountingURL, "/")+"/health", nil)
	pingResp, pingErr := s.client.Do(pingReq)
	if pingErr != nil {
		return nil, fmt.Errorf("GOOGLE FLOW SERVICE OFFLINE (is uvicorn running on port 8000?): %w", pingErr)
	}
	pingResp.Body.Close()

	// For YouTube format (16:9), add it to the prompt as a hint for Google Flow
	// since direct UI selection is unstable.
	flowPrompt := styledPrompt
	if !strings.Contains(strings.ToLower(flowPrompt), "16:9") {
		flowPrompt += ", 16:9 aspect ratio, wide format"
	}
	reqBody := googleaccounting.FlowImageRequest{
		Prompt:    flowPrompt,
		ProjectID: s.flowProjectID,
		Style:     style,
		Account:   "favamassimo", // Default to favamassimo
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal google flow request failed: %w", err)
	}

	s.log.Info("triggering google flow generation", zap.String("prompt", flowPrompt), zap.String("account", reqBody.Account))

	startURL := strings.TrimRight(s.googleAccountingURL, "/") + "/generate-flow-images"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create google flow request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Error("google flow POST failed", zap.Error(err))
		return nil, fmt.Errorf("google flow start request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		s.log.Error("google flow start failed", zap.Int("code", resp.StatusCode), zap.String("body", string(respBody)))
		return nil, fmt.Errorf("google flow start failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var startResp googleaccounting.StartResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return nil, fmt.Errorf("decode google flow start response failed: %w", err)
	}
	if strings.TrimSpace(startResp.JobID) == "" {
		return nil, fmt.Errorf("google flow start response missing job_id")
	}

	s.log.Info("google flow job started", zap.String("job_id", startResp.JobID))

	job, err := s.waitForGoogleJob(ctx, startResp.JobID)
	if err != nil {
		s.log.Error("google flow job failed or timed out", zap.String("job_id", startResp.JobID), zap.Error(err))
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
		slug = Slugify(cleanPrompt)
	}
	description := fmt.Sprintf("AI generated image via Google Flow for prompt: %s", cleanPrompt)

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

		// Calcola hash SHA-256 e salta se esiste già (è un'immagine vecchia catturata dal DOM)
		hasher := sha256.New()
		hasher.Write(content)
		hash := hex.EncodeToString(hasher.Sum(nil))

		if existing, err := s.repo.GetImageByHash(ctx, hash); err == nil && existing != nil {
			s.log.Info("google flow image already exists in DB, skipping from this run", zap.String("hash", hash))
			assets = append(assets, existing)
			continue
		}

		// Extract GenerationID from the parent folder (e.g. "gen_20260527_...")
		genID := filepath.Base(filepath.Dir(resolved))

		// Ingest with skipMetadata=true as we will upload a group metadata file at the end
		asset, ingestErr := s.IngestImage(ctx, slug, style, genID, bytes.NewReader(content), filepath.Base(resolved), "google-flow", description, tags, skipDrive, true)
		if ingestErr != nil {
			s.log.Warn("failed to ingest google flow image", zap.String("path", resolved), zap.Error(ingestErr))
			continue
		}
		assets = append(assets, asset)
	}

	if len(assets) == 0 {
		return nil, fmt.Errorf("google flow generated no ingestible images")
	}

	s.log.Info("google flow generation finished", zap.Int("assets", len(assets)), zap.Bool("skip_drive", skipDrive))

	// 8. Upload unified metadata.json for the entire group
	if !skipDrive && len(assets) > 0 {
		// Use the genID from the first asset's directory structure
		firstPath := assets[0].PathRel
		dirName := filepath.Base(filepath.Dir(firstPath))
		
		s.log.Info("triggering batch metadata upload", zap.String("gen_id", dirName), zap.String("style", style))
		s.UploadBatchMetadata(ctx, dirName, slug, style, cleanPrompt, "google-flow", assets)
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

	// Recuperiamo la descrizione originale o usiamo un prompt fallback se non c'è.
	prompt := asset.Description
	generator := "nvidia"
	if asset.SourceURL == "google-flow" || strings.Contains(strings.ToLower(prompt), "google flow") {
		generator = "google-flow"
	} else if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(asset.MetadataJSON), &meta); err == nil {
			if genVal, ok := meta["generator"].(string); ok && genVal != "" {
				generator = genVal
			}
		}
	}

	if strings.HasPrefix(prompt, "AI generated image") {
		parts := strings.SplitN(prompt, "for prompt: ", 2)
		if len(parts) == 2 {
			prompt = parts[1]
		}
	}
	if prompt == "" {
		prompt = asset.SubjectID // Fallback to subject
	}
	
	s.uploadImageMetadata(ctx, req, prompt, style, generator, fileID, webLink, asset.Hash, imagePath, asset.Width, asset.Height)

	s.log.Info("image uploaded to Drive with style",
		zap.String("file_id", fileID),
		zap.String("style", style),
	)
	return webLink, fileID, nil
}

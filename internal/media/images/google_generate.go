package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
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

	// Check if this image has already been generated and is in the DB
	if s.repo != nil && s.repo.DB() != nil {
		desc := fmt.Sprintf("AI generated image via Google Vids for prompt: %s", cleanPrompt)
		var img models.ImageAsset
		var name, urlVal, tagsJSON, metaJSON, createdAtStr, fileHash, localPath, driveFileID sql.NullString
		err := s.repo.DB().QueryRowContext(ctx, `
			SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id
			FROM media_assets
			WHERE media_type = 'image' AND name = ?
			LIMIT 1
		`, desc).Scan(&img.SlugID, &name, &urlVal, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID)
		if err == nil {
			s.log.Info("REUSING already generated Google Vids image from database", zap.String("prompt", cleanPrompt))
			img.Description = name.String
			img.SourceURL = urlVal.String
			img.Hash = fileHash.String
			img.PathRel = localPath.String
			img.DriveFileID = driveFileID.String
			if createdAtStr.Valid {
				img.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr.String)
			}
			if tagsJSON.Valid && tagsJSON.String != "" {
				_ = json.Unmarshal([]byte(tagsJSON.String), &img.Tags)
			}
			if metaJSON.Valid && metaJSON.String != "" {
				img.MetadataJSON = metaJSON.String
			}
			return &img, nil
		}
	}

	// Apply style from registry if provided
	styledPrompt := cleanPrompt
	if s.styleRegistry != nil && style != "" {
		styledPrompt = s.styleRegistry.ApplyStyle(cleanPrompt, style)
	}

	// Step 1: Try Google Vids image synthesis
	asset, err := s.generateGoogleVidsImage(ctx, cleanPrompt, styledPrompt, subject, style, tags, width, height, skipDrive)
	if err == nil && asset != nil {
		return asset, nil
	}
	if err != nil {
		s.log.Error("CRITICAL: Google Vids image generation FAILED",
			zap.String("subject", subject),
			zap.String("error_type", fmt.Sprintf("%T", err)),
			zap.Error(err),
		)
		s.log.Info("Switching to NVIDIA fallback due to Google Vids failure")
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

func (s *Service) generateGoogleVidsImage(ctx context.Context, cleanPrompt, styledPrompt, subject, style string, tags []string, width, height int, skipDrive bool) (*models.ImageAsset, error) {
	s.log.Info("generateGoogleVidsImage: entering function", zap.String("googleAccountingURL", s.googleAccountingURL))

	if strings.TrimSpace(s.googleAccountingURL) == "" {
		return nil, fmt.Errorf("google accounting server url not configured")
	}

	// Quick health check before starting long job
	pingReq, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(s.googleAccountingURL, "/")+"/health", nil)
	pingResp, pingErr := s.client.Do(pingReq)
	if pingErr != nil {
		return nil, fmt.Errorf("google vids service offline: %w", pingErr)
	}
	pingResp.Body.Close()

	// For YouTube vertical format (9:16), add it to the prompt as a hint for Google Vids.
	vidsPrompt := styledPrompt
	if height > width && !strings.Contains(strings.ToLower(vidsPrompt), "9:16") {
		vidsPrompt += ", 9:16 aspect ratio, vertical format, youtube shorts format"
	}
	videoID := strings.TrimSpace(s.vidsProjectID)
	if videoID == "" {
		videoID = "new"
	}
	reqBody := googleaccounting.VidsImageRequest{
		VideoID:       videoID,
		Prompt:        vidsPrompt,
		Account:       "favamassimo", // Default to favamassimo
		DriveFolderID: s.effectiveImagesDriveFolderID(),
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal google vids request failed: %w", err)
	}

	s.log.Info("triggering google vids image generation", zap.String("prompt", vidsPrompt), zap.String("account", reqBody.Account))

	startURL := strings.TrimRight(s.googleAccountingURL, "/") + "/generate-vids-images"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create google vids request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Error("google vids POST failed", zap.Error(err))
		return nil, fmt.Errorf("google vids start request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		s.log.Error("google vids start failed", zap.Int("code", resp.StatusCode), zap.String("body", string(respBody)))
		return nil, fmt.Errorf("google vids start failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var startResp googleaccounting.StartResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return nil, fmt.Errorf("decode google vids start response failed: %w", err)
	}
	if strings.TrimSpace(startResp.JobID) == "" {
		return nil, fmt.Errorf("google vids start response missing job_id")
	}

	s.log.Info("google vids job started", zap.String("job_id", startResp.JobID))

	job, err := s.waitForGoogleJob(ctx, startResp.JobID)
	if err != nil {
		s.log.Error("google vids job failed or timed out", zap.String("job_id", startResp.JobID), zap.Error(err))
		return nil, err
	}

	slug := Slugify(subject)
	if slug == "" {
		slug = Slugify(cleanPrompt)
	}
	if len(slug) > 60 {
		slug = slug[:60]
		slug = strings.TrimRight(slug, "-")
	}
	description := fmt.Sprintf("AI generated image via Google Vids for prompt: %s", cleanPrompt)

	resolved := job.FilePath
	if resolved == "" && len(job.Files) > 0 {
		resolved = job.Files[0]
	}
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

	content, readErr := os.ReadFile(resolved)
	if readErr != nil {
		s.log.Warn("failed to read google vids image", zap.String("path", resolved), zap.Error(readErr))
		return nil, fmt.Errorf("google vids image read failed: %w", readErr)
	}

	hasher := sha256.New()
	hasher.Write(content)
	hash := hex.EncodeToString(hasher.Sum(nil))

	if existing, err := s.repo.GetImageByHash(ctx, hash); err == nil && existing != nil {
		s.log.Info("google vids image already exists in DB, skipping from this run", zap.String("hash", hash))
		return existing, nil
	}

	genID := slug
	asset, ingestErr := s.IngestImage(ctx, slug, style, genID, bytes.NewReader(content), filepath.Base(resolved), "google-vids", description, tags, skipDrive, false)
	if ingestErr != nil {
		s.log.Warn("failed to ingest google vids image", zap.String("path", resolved), zap.Error(ingestErr))
		return nil, ingestErr
	}

	if !skipDrive {
		s.log.Info("google vids generation finished", zap.String("gen_id", genID), zap.Bool("skip_drive", skipDrive))
	}

	return asset, nil
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
	if asset.SourceURL == "google-vids" || strings.Contains(strings.ToLower(prompt), "google vids") {
		generator = "google-vids"
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

	// Call unified tagger ONCE and reuse result
	metaResult, metaErr := s.tagImageMetadata(ctx, prompt, style, generator, asset.Hash, imagePath, asset.Width, asset.Height)
	if metaErr == nil && metaResult != nil {
		s.uploadImageMetadata(ctx, req, metaResult)
	}

	s.log.Info("image uploaded to Drive with style",
		zap.String("file_id", fileID),
		zap.String("style", style),
	)
	return webLink, fileID, nil
}

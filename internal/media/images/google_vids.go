package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/storage"
	"velox/go-master/internal/pkg/googleaccounting"
)

// metadataPayload contains fields for the metadata.json uploaded alongside generated videos.
// Più campi = migliore ricerca semantica quando attiveremo il vector store.
type metadataPayload struct {
	Prompt      string   `json:"prompt"`
	Subject     string   `json:"subject"`
	Style       string   `json:"style"`
	Generator   string   `json:"generator"`
	Tags        []string `json:"tags"`
	FileID      string   `json:"file_id"`
	DriveLink   string   `json:"drive_link"`
	Hash        string   `json:"hash"`
	FileSize    int64    `json:"file_size"`
	Width       int      `json:"width"`
	Height      int      `json:"height"`
	DurationSec int      `json:"duration_sec"`
	CreatedAt   string   `json:"created_at"`
}

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
		Style:    style,
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

	// Upload su Drive e registra in media_assets
	if err := s.RegisterVideoAsset(ctx, resolved, prompt, "google-vids", style, 8, "", ""); err != nil {
		s.log.Warn("failed to register video asset in DB", zap.Error(err))
	}

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

	// Registra l'avatar video in media_assets
	if err := s.RegisterVideoAsset(ctx, resolved, script, "google-vids-avatar", avatarID, 30, "", ""); err != nil {
		s.log.Warn("failed to register avatar video asset in DB", zap.Error(err))
	}

	return resolved, nil
}

// RegisterVideoAsset uploada su Drive e crea un record in media_assets per un video generato.
// Se driveFileID e driveLink sono già noti (es. da fullimages), li usa senza ri-uploadare.
// Sul Drive crea la struttura: <videoRoot>/<style>/<subject>/<hash>.mp4 + metadata.json
func (s *Service) RegisterVideoAsset(ctx context.Context, filePath, description, source, style string, durationSec int, existingDriveFileID, existingDriveLink string) error {
	if s.stockRepo == nil {
		return fmt.Errorf("stock repo not configured")
	}
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("video file not found: %w", err)
	}

	id := fmt.Sprintf("vid_%x_%d", sha256Hash(filePath), time.Now().Unix())
	subject := Slugify(description)
	name := description
	if len(name) > 80 {
		name = name[:80]
	}
	if style != "" {
		name = fmt.Sprintf("[%s] %s", style, name)
	}

	// Upload to Drive solo se non abbiamo già driveFileID
	var driveFileID, driveLink string
	uploaded := false
	if existingDriveFileID != "" {
		driveFileID = existingDriveFileID
		driveLink = existingDriveLink
	} else if s.mediaStore != nil {
		req := storage.AssetDestinationRequest{
			Source:    storage.SourceImage,
			MediaType: storage.MediaTypeImageVideo,
			Subject:   subject,
			Hash:      id,
			Ext:       ".mp4",
			Style:     style,
		}
		fid, wl, err := s.mediaStore.UploadToDrive(ctx, req, filePath)
		if err != nil {
			s.log.Warn("RegisterVideoAsset: Drive upload failed (non fatale)", zap.Error(err))
		} else {
			driveFileID = fid
			driveLink = wl
			uploaded = true
			s.log.Info("RegisterVideoAsset: Drive upload successful", zap.String("file_id", fid))

			// Upload metadata.json to the same Drive folder
			s.uploadVideoMetadata(ctx, req, description, style, source, fid, wl, durationSec, id, filePath)
		}
	}

	clip := &models.MediaAsset{
		ID:          id,
		Name:        name,
		Source:      source,
		MediaType:   "video",
		LocalPath:   filePath,
		DriveFileID: driveFileID,
		DriveLink:   driveLink,
		Status:      "ready",
		Duration:    durationSec,
		CreatedAt:   time.Now(),
	}
	clip.SetMetadataString("prompt", description)
	clip.SetMetadataString("style", style)
	clip.SetMetadataString("generator", source)

	if err := s.stockRepo.UpsertClip(ctx, clip); err != nil {
		return err
	}

	// Delete local file after successful Drive upload + DB registration
	if uploaded && filePath != "" {
		if err := os.Remove(filePath); err != nil {
			s.log.Warn("RegisterVideoAsset: failed to remove local file", zap.String("path", filePath), zap.Error(err))
		} else {
			s.log.Info("RegisterVideoAsset: local file removed after Drive upload", zap.String("path", filePath))
		}
	}

	return nil
}

// uploadVideoMetadata scrive un file metadata.json nella stessa cartella Drive del video.
func (s *Service) uploadVideoMetadata(ctx context.Context, req storage.AssetDestinationRequest, prompt, style, generator, fileID, driveLink string, durationSec int, hash, localPath string) {
	subject, tags := extractSubjectAndTags(prompt)
	fi, err := os.Stat(localPath)
	fileSize := int64(0)
	if err == nil {
		fileSize = fi.Size()
	}

	meta := metadataPayload{
		Prompt:      prompt,
		Subject:     subject,
		Style:       style,
		Generator:   generator,
		Tags:        tags,
		FileID:      fileID,
		DriveLink:   driveLink,
		Hash:        hash,
		FileSize:    fileSize,
		Width:       1920,
		Height:      1080,
		DurationSec: durationSec,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		s.log.Warn("uploadVideoMetadata: failed to marshal metadata", zap.Error(err))
		return
	}

	tmpPath := filepath.Join(s.tempDir, "metadata_"+req.Hash+".json")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		s.log.Warn("uploadVideoMetadata: failed to write temp metadata file", zap.Error(err))
		return
	}
	defer os.Remove(tmpPath)

	metaReq := req
	metaReq.Hash = "metadata"
	metaReq.Ext = ".json"
	if _, _, err := s.mediaStore.UploadToDrive(ctx, metaReq, tmpPath); err != nil {
		s.log.Warn("uploadVideoMetadata: failed to upload metadata.json", zap.Error(err))
		return
	}
	s.log.Info("uploadVideoMetadata: metadata.json uploaded", zap.String("prompt", prompt), zap.String("style", style))
}

// extractSubjectAndTags estrae subject e tags dal prompt per la ricerca semantica.
func extractSubjectAndTags(prompt string) (subject string, tags []string) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "unknown", nil
	}

	parts := strings.Split(prompt, ",")
	subject = strings.TrimSpace(parts[0])
	if len(subject) > 60 {
		subject = subject[:60]
	}

	seen := make(map[string]bool)
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		lower := strings.ToLower(t)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		tags = append(tags, t)
	}
	return subject, tags
}

// sha256Hash calcola l'hash SHA256 di una stringa (es. percorso file).
func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}

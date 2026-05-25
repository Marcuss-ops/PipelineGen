package images

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/media/ingest"
	"velox/go-master/internal/media/models"
)
func (s *Service) SyncAssets() error {
	return nil
}

func (s *Service) SyncFromDrive(ctx context.Context) error {
	if s.driveSvc == nil || s.driveFolderID == "" {
		return fmt.Errorf("drive service or folder ID not configured")
	}

	s.log.Info("Starting images sync from Drive", zap.String("folder_id", s.driveFolderID))
	return s.syncFolderRecursive(ctx, s.driveFolderID, "")
}

func (s *Service) syncFolderRecursive(ctx context.Context, folderID, folderPath string) error {
	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	fl, err := s.driveSvc.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType, webViewLink, webContentLink)").
		PageSize(1000).
		Context(ctx).
		Do()
	if err != nil {
		return err
	}

	for _, file := range fl.Files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			newPath := filepath.Join(folderPath, file.Name)
			if err := s.syncFolderRecursive(ctx, file.Id, newPath); err != nil {
				s.log.Warn("failed to sync subfolder", zap.String("id", file.Id), zap.Error(err))
			}
			continue
		}

		// Skip non-image files (basic check)
		lowerName := strings.ToLower(file.Name)
		if !strings.HasSuffix(lowerName, ".jpg") && !strings.HasSuffix(lowerName, ".jpeg") &&
			!strings.HasSuffix(lowerName, ".png") && !strings.HasSuffix(lowerName, ".webp") {
			continue
		}

		// Check if already exists by Drive ID
		existing, err := s.repo.GetByDriveFileID(ctx, file.Id)
		if err == nil && existing != nil {
			continue
		}

		// Create metadata-only record
		// Note: We don't have the hash yet, so we use a placeholder or the Drive ID
		// IngestImage would be better but it downloads the file.
		// For population, we just want the record.

		asset := &models.ImageAsset{
			SubjectID:    Slugify(file.Name),
			Hash:         "drive_" + file.Id, // Placeholder hash
			PathRel:      "",                 // No local path yet
			SourceURL:    file.WebViewLink,
			Description:  "Synced from Drive: " + file.Name,
			DriveFileID:  file.Id,
			Status:       "ready",
			MetadataJSON: "{}",
		}

		if _, err := s.repo.AddImage(asset); err != nil {
			s.log.Warn("failed to add synced image", zap.String("name", file.Name), zap.Error(err))
		}
	}

	return nil
}

func (s *Service) GenerateAImage(prompt, model string, width, height int, tags []string) (*models.ImageAsset, error) {
	var invokeURL string
	var payload map[string]interface{}
	var useCloudAuth bool
	var sourceLabel string
	resolvedModel := strings.TrimSpace(model)

	// Default resolution if not provided
	if width <= 0 {
		width = 1024
	}
	if height <= 0 {
		height = 1024
	}

	if resolvedModel == "" {
		if s.nvidiaAPIKey != "" && s.nvidiaAPIKey != "PASTE_YOUR_NVIDIA_API_KEY_HERE" {
			resolvedModel = "flux-1-dev"
		} else {
			resolvedModel = "local-nim"
		}
	}

	switch resolvedModel {
	case "flux-1-dev":
		invokeURL = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.1-dev"
		payload = map[string]interface{}{
			"prompt":    prompt,
			"mode":      "base",
			"cfg_scale": 3.5,
			"width":     width,
			"height":    height,
			"seed":      0,
			"steps":     50,
		}
		useCloudAuth = true
		sourceLabel = "flux-1-dev"

	case "flux-2-klein":
		invokeURL = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.2-klein-4b"
		payload = map[string]interface{}{
			"prompt": prompt,
			"width":  width,
			"height": height,
			"seed":   0,
			"steps":  4,
		}
		useCloudAuth = true
		sourceLabel = "flux-2-klein"

	case "local-nim", "":
		invokeURL = "http://localhost:8000/v1/infer"
		payload = map[string]interface{}{
			"prompt": prompt,
			"mode":   "base",
			"seed":   0,
			"steps":  30,
		}
		useCloudAuth = false
		sourceLabel = "nvidia-local"
		resolvedModel = "local-nim"

	default:
		return nil, fmt.Errorf("unsupported model: %s", model)
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", invokeURL, strings.NewReader(string(jsonPayload)))
	if err != nil {
		return nil, err
	}

	if useCloudAuth {
		if s.nvidiaAPIKey == "" || s.nvidiaAPIKey == "PASTE_YOUR_NVIDIA_API_KEY_HERE" {
			return nil, fmt.Errorf("NVIDIA API key not configured (required for cloud models)")
		}
		req.Header.Set("Authorization", "Bearer "+s.nvidiaAPIKey)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s error (status %d): %s", resolvedModel, resp.StatusCode, string(body))
	}

	var responseBody struct {
		Image     string `json:"image"`
		Artifacts []struct {
			Base64 string `json:"base64"`
		} `json:"artifacts"`
	}

	if err := json.Unmarshal(body, &responseBody); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var base64Data string
	if responseBody.Image != "" {
		base64Data = responseBody.Image
	} else if len(responseBody.Artifacts) > 0 {
		base64Data = responseBody.Artifacts[0].Base64
	}

	if base64Data == "" {
		return nil, fmt.Errorf("no image data found in response")
	}

	// Decode base64
	imageData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 image: %w", err)
	}

	// Ingest image
	slug := Slugify(prompt)
	if len(slug) > 50 {
		slug = slug[:50]
	}
	filename := fmt.Sprintf("%s_%d.png", sourceLabel, time.Now().Unix())
	description := fmt.Sprintf("AI generated image via %s for prompt: %s", resolvedModel, prompt)

	return s.IngestImage(context.Background(), slug, strings.NewReader(string(imageData)), filename, sourceLabel, description, tags)
}

func (s *Service) AnimateImage(ctx context.Context, imageHash string, duration int) (string, error) {
	// 1. Get image from repo
	asset, err := s.repo.GetImageByHash(imageHash)
	if err != nil {
		return "", fmt.Errorf("image not found: %w", err)
	}

	fullPath := filepath.Join(s.imagesDir, asset.PathRel)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return "", fmt.Errorf("local file not found: %s", fullPath)
	}

	// 2. Prepare output path
	outputName := fmt.Sprintf("animate_%s.mp4", imageHash)
	outputPath := filepath.Join(s.animationsDir, outputName)

	// 3. Run script
	scriptPath := filepath.Join(s.scriptsDir, "animate_image.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		// Fallback for development if scripts is in current dir
		scriptPath = "scripts/animate_image.py"
	}

	durStr := fmt.Sprintf("%d", duration)
	if duration <= 0 {
		durStr = "7"
	}

	cmd := exec.CommandContext(ctx, "python3", scriptPath, fullPath, "--output", outputPath, "--duration", durStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.log.Error("Animation script failed", zap.Error(err), zap.String("output", string(output)))
		return "", fmt.Errorf("animation failed: %w", err)
	}

	s.log.Info("Animation created", zap.String("path", outputPath))

	// 4. Se l'ingest pipeline è disponibile, usala per stock clip
	if s.ingestSvc != nil {
		_, err := s.ingestSvc.Ingest(ctx, &ingest.Request{
			Kind:      string(ingest.KindStock),
			LocalPath: outputPath,
			Name:      "AI Animation: " + asset.SubjectID,
			Filename:  outputName,
			Group:     "ai-generated",
			Source:    "nvidia-animation",
			SourceID:  imageHash,
			Duration:  duration,
		})
		if err != nil {
			s.log.Warn("Ingest pipeline failed for animated clip", zap.Error(err))
		}
		return outputPath, nil
	}

	// 5. Fallback: upload manuale a Drive
	var driveVideoID string
	if s.driveSvc != nil && s.driveFolderID != "" {
		s.log.Info("Uploading animated video to Google Drive", zap.String("filename", outputName))

		videoFile, err := os.Open(outputPath)
		if err == nil {
			driveFile := &driveapi.File{
				Name:    outputName,
				Parents: []string{s.driveFolderID},
			}

			res, err := s.driveSvc.Files.Create(driveFile).
				Media(videoFile).
				Fields("id").
				Do()

			videoFile.Close()

			if err != nil {
				s.log.Error("Drive video upload failed", zap.Error(err))
			} else {
				driveVideoID = res.Id
				s.log.Info("Drive video upload successful", zap.String("file_id", driveVideoID))
			}
		}
	}

	// 6. Salva nel DB stock (fallback)
	if s.stockRepo != nil {
		clip := &models.MediaAsset{
			ID:          "ai_" + imageHash,
			Name:        "AI Animation: " + asset.SubjectID,
			Filename:    outputName,
			Group:       "ai-generated",
			MediaType:   "video",
			DriveFileID: driveVideoID,
			LocalPath:   outputPath,
			Duration:    duration,
			Source:      "nvidia-animation",
			Status:      "ready",
			CreatedAt:   time.Now(),
		}

		if err := s.stockRepo.UpsertClip(ctx, clip); err != nil {
			s.log.Warn("Failed to ingest animated clip into stock DB", zap.Error(err))
		} else {
			s.log.Info("Animated clip ingested into stock DB", zap.String("clip_id", clip.ID))
		}
	}

	return outputPath, nil
}

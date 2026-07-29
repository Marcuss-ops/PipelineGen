package images

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ── Drive Upload ───────────────────────────────────────────────────────

// UploadToStyleDrive uploads an image to Drive in a style-based subfolder
// via the canonical delivery.Publisher.Publish canal.
func (s *ImageStorageService) UploadToStyleDrive(ctx context.Context, imgAsset *asset.ImageAsset, style string) (string, string, error) {
	if s.publisher == nil {
		return "", "", fmt.Errorf("publisher not configured")
	}
	if style == "" {
		return "", "", fmt.Errorf("style is required")
	}

	imagePath := filepath.Join(s.imagesDir, imgAsset.PathRel)

	result, err := s.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationImage,
		LocalPath:      imagePath,
		Filename:       filepath.Base(imagePath),
		Style:          style,
		Subject:        imgAsset.SubjectID,
		ConflictPolicy: delivery.ConflictSkip,
	})
	if err != nil {
		return "", "", fmt.Errorf("style-based Drive upload: %w", err)
	}
	fileID := result.FileID
	webLink := result.WebViewLink

	prompt := imgAsset.Description
	generator := string(asset.ProviderNVIDIA)
	if d, ok := asset.DefaultProviderRegistry().Match(imgAsset.SourceURL); ok && d.Origin == asset.ImageOriginGenerated {
		generator = string(d.ID)
	} else if d, ok := asset.DefaultProviderRegistry().Match(prompt); ok && d.Origin == asset.ImageOriginGenerated {
		generator = string(d.ID)
	} else if imgAsset.MetadataJSON != "" && imgAsset.MetadataJSON != "{}" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(imgAsset.MetadataJSON), &meta); err == nil {
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
		prompt = imgAsset.SubjectID
	}

	metaResult, metaErr := s.meta.tagImageMetadata(ctx, prompt, style, generator, imgAsset.Hash, imagePath, imgAsset.Width, imgAsset.Height)
	if metaErr == nil && metaResult != nil {
		s.meta.uploadImageMetadata(ctx, style, imgAsset.SubjectID, metaResult)
	}

	s.log.Info("image uploaded to Drive with style", zap.String("file_id", fileID), zap.String("style", style))
	return webLink, fileID, nil
}

// FormatDriveLink returns a Google Drive web-view link for the given file ID.
func (s *ImageStorageService) FormatDriveLink(id string) string {
	if id == "" {
		return ""
	}
	return "https://drive.google.com/file/d/" + id
}

// ── Drive Sync ─────────────────────────────────────────────────────────

// SyncFromDrive syncs image assets from Google Drive to the local DB.
func (s *ImageStorageService) SyncFromDrive(ctx context.Context) error {
	if s.driveReader == nil || s.driveFolderID == "" {
		return fmt.Errorf("drive service or folder ID not configured")
	}
	s.log.Info("Starting images sync from Drive", zap.String("folder_id", s.driveFolderID))
	return s.syncFolderRecursive(ctx, s.driveFolderID, "")
}

func (s *ImageStorageService) syncFolderRecursive(ctx context.Context, folderID, folderPath string) error {
	files, err := s.driveReader.ListFiles(ctx, folderID)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			newPath := filepath.Join(folderPath, file.Name)
			if err := s.syncFolderRecursive(ctx, file.ID, newPath); err != nil {
				s.log.Warn("failed to sync subfolder", zap.String("id", file.ID), zap.Error(err))
			}
			continue
		}
		lowerName := strings.ToLower(file.Name)
		if !strings.HasSuffix(lowerName, ".jpg") && !strings.HasSuffix(lowerName, ".jpeg") &&
			!strings.HasSuffix(lowerName, ".png") && !strings.HasSuffix(lowerName, ".webp") {
			continue
		}
		existing, err := s.repo.GetByDriveFileID(ctx, file.ID)
		if err == nil && existing != nil {
			continue
		}
		imgAsset := &asset.ImageAsset{
			SubjectID:    textutil.Slugify(file.Name),
			Hash:         "drive_" + file.ID,
			PathRel:      "",
			SourceURL:    file.WebViewLink,
			Description:  "Synced from Drive: " + file.Name,
			DriveFileID:  file.ID,
			Status:       "ready",
			MetadataJSON: "{}",
		}
		if _, err := s.repo.AddImage(ctx, imgAsset); err != nil {
			s.log.Warn("failed to add synced image", zap.String("name", file.Name), zap.Error(err))
		}
	}
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func (s *ImageStorageService) aiImageDriveRootForSource(source, style string) string {
	if s == nil || s.cfg == nil {
		return ""
	}
	if !asset.IsAIImageSource(source) {
		return ""
	}
	styleFolders := map[string]string{
		"medieval":         "1yfCnjvpZ3ZuFs7W0pRFNGzapRLGIykPi",
		"whiteboard":       "1Znu_g8pUOXkXHG-1XkLMOcYN69umrlae",
		"anime":            "1e1pW8ZaQYTwDV0po6tIxx_vUql_6CD_v",
		"cinematic":        "1t6bhe8kquPqk7ypYzbobHqUq-HGjVdZw",
		"sketch":           "1QrC74aZ8It43pQa5l5G6BNWcc18ksIo2",
		"watercolor":       "1tzvn5PkOwZk3DPjjr8sIXKr9LKeM--rB",
		"cyberpunk":        "1x8xcUFtIj7hkGF6CsPJCM822ooJL9kMu",
		"realistic":        "1b5iP5aHekJUL1FB9ZC-WGkWxoDULyU9X",
		"heritage":         "1l_cdMqhKrstV94V7Ym7wemJTUZjjWLq_",
		"kawaii":           "1K5IcI3sC5qLID0M1ulSoUC355S_3lUNh",
		"professional-doc": "1g2Ef3yQCDWZ78YqnOnwhKmIghGJvPOPa",
		"cartoon":          "1ab_YSfuKpj4CCh9twk3st5zv9fvMwS8B",
		"retro-print":      "1141lRohkIiXp8NjGQlGj4bLLaQw6nCDb",
		"papercraft":       "1yWlji7wololy_q3l8GAcmmF8goxJmOih",
		"gothic":           "1CNNcNWY4YXyat9eqUsmsUEGeMmTXJY3t",
		"oil-painting":     "1mI07oRaeabhGSmjdyKOICl5vSK6uSO7i",
		"3d-render":        "1MWZy1rDXQKoAr0HRVMc7BdGAvqCaSe1y",
	}
	if folderID, ok := styleFolders[strings.ToLower(style)]; ok {
		return folderID
	}
	return s.cfg.Drive.ImagesFolder()
}

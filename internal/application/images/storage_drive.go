package images

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	audio "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// ── Drive Upload ───────────────────────────────────────────────────────

// UploadToStyleDrive uploads an image to Drive in a style-based subfolder.
func (s *ImageStorageService) UploadToStyleDrive(ctx context.Context, imgAsset *asset.ImageAsset, style string) (string, string, error) {
	if s.mediaStore == nil {
		return "", "", fmt.Errorf("media store not configured")
	}
	if style == "" {
		return "", "", fmt.Errorf("style is required")
	}

	req := drive.AssetDestinationRequest{
		Source:            drive.SourceImage,
		MediaType:         drive.MediaTypeImage,
		Style:             style,
		Subject:           imgAsset.SubjectID,
		Hash:              imgAsset.Hash,
		Ext:               filepath.Ext(imgAsset.PathRel),
		DriveRootOverride: s.cfg.Drive.ImagesFolder(),
	}
	imagePath := filepath.Join(s.imagesDir, imgAsset.PathRel)

	fileID, webLink, err := s.mediaStore.UploadToDrive(ctx, req, imagePath)
	if err != nil {
		return "", "", fmt.Errorf("style-based Drive upload: %w", err)
	}

	prompt := imgAsset.Description
	generator := "nvidia"
	if imgAsset.SourceURL == "google-vids" || imgAsset.SourceURL == "google-slides" || textutil.ContainsCI(prompt, "google vids") || textutil.ContainsCI(prompt, "google slides") {
		generator = "google-slides"
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
		s.meta.uploadImageMetadata(ctx, req, metaResult)
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

// ── Video Asset Registration ───────────────────────────────────────────

// RegisterVideoAsset uploads a video to Drive and creates a record in media_assets.
func (s *ImageStorageService) RegisterVideoAsset(ctx context.Context, filePath, description, source, style string, durationSec int, existingDriveFileID, existingDriveLink string) error {
	if s.stockRepo == nil {
		return fmt.Errorf("stock repo not configured")
	}
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("video file not found: %w", err)
	}

	id := fmt.Sprintf("vid_%x_%d", sha256Hash(filePath), time.Now().Unix())
	subject := textutil.Slugify(description)
	name := description
	if len(name) > 80 {
		name = name[:80]
	}
	if style != "" {
		name = fmt.Sprintf("[%s] %s", style, name)
	}

	uploaded := false
	var folderID string
	var semanticMeta *semantic.Payload
	var driveFileID, driveLink string
	if existingDriveFileID != "" {
		driveFileID = existingDriveFileID
		driveLink = existingDriveLink
	} else if s.mediaStore != nil {
		req := drive.AssetDestinationRequest{
			Source:    drive.SourceImage,
			MediaType: drive.MediaTypeImageVideo,
			Subject:   subject,
			Hash:      id,
			Ext:       ".mp4",
			Style:     style,
		}
		folderID, _ = s.mediaStore.EnsureDriveFolder(ctx, req)
		fid, wl, err := s.mediaStore.UploadToDrive(ctx, req, filePath)
		if err != nil {
			s.log.Warn("RegisterVideoAsset: Drive upload failed (non fatale)", zap.Error(err))
		} else {
			driveFileID = fid
			driveLink = wl
			uploaded = true
			s.log.Info("RegisterVideoAsset: Drive upload successful", zap.String("file_id", fid))
			semanticMeta = s.uploadVideoMetadata(ctx, req, description, style, source, fid, wl, durationSec, id, filePath, folderID)
		}
	}

	clip := &asset.Asset{
		ID:        id,
		Name:      name,
		Source:    asset.Source(source),
		MediaType: asset.MediaType("video"),
		CreatedAt: time.Now(),
	}
	clip.SetDriveFileID(driveFileID)
	clip.SetDriveLink(driveLink)
	clip.SetMetadataString("prompt", description)
	clip.SetMetadataString("style", style)
	clip.SetMetadataString("generator", source)

	if semanticMeta != nil {
		clip.SearchText = semanticMeta.SearchText
		clip.Tags = uniqueAppend(clip.Tags, semanticMeta.Tags...)
		if style != "" {
			clip.Group = style
		} else if len(semanticMeta.Subjects) > 0 {
			clip.Group = semanticMeta.Subjects[0]
		}
	} else if style != "" {
		clip.Group = style
	}

	if s.dispatcher != nil {
		contentHash := sha256Hash(filePath + id)
		if err := s.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
			return fmt.Errorf("dispatcher.EnqueueAndIndex video %s: %w", id, err)
		}
		s.log.Debug("RegisterVideoAsset: saved via dispatcher", zap.String("id", id))
	} else if err := s.stockRepo.Upsert(ctx, clip); err != nil {
		return err
	}

	if uploaded && s.mediaStore != nil {
		s.registerAudioClip(ctx, filePath, description, style, source, durationSec, id, subject)
	}

	if uploaded && filePath != "" {
		if err := os.Remove(filePath); err != nil {
			s.log.Warn("RegisterVideoAsset: failed to remove local file", zap.String("path", filePath), zap.Error(err))
		} else {
			s.log.Info("RegisterVideoAsset: local file removed after Drive upload", zap.String("path", filePath))
		}
	}

	return nil
}

func (s *ImageStorageService) uploadVideoMetadata(ctx context.Context, req drive.AssetDestinationRequest, prompt, style, generator, fileID, driveLink string, durationSec int, hash, localPath, folderID string) *SemanticMetadataPayload {
	if s.meta == nil || s.meta.metaWriter == nil {
		s.log.Warn("uploadVideoMetadata: metadata writer not configured")
		return nil
	}

	result, err := s.meta.metaWriter.Write(ctx, semantic.WriteRequest{
		AssetID:    hash,
		AssetType:  "video",
		MediaType:  "video",
		Source:     "generated",
		Generator:  generator,
		Style:      style,
		Prompt:     prompt,
		LocalPath:  localPath,
		TempDir:    s.tempDir,
		Extensions: semantic.BuildVideoExtension(durationSec, 0, "", false),
		Assets: []map[string]any{
			{"file_id": fileID, "drive_link": driveLink, "duration_sec": durationSec, "hash": hash},
		},
	})
	if err != nil {
		s.log.Warn("uploadVideoMetadata: metadata writer failed", zap.Error(err))
		return nil
	}

	metaReq := req
	metaReq.Hash = "metadata"
	metaReq.Ext = ".json"
	if _, _, err := s.mediaStore.UploadToDrive(ctx, metaReq, result.LocalPath); err != nil {
		s.log.Warn("uploadVideoMetadata: failed to upload metadata.json", zap.Error(err))
		return result.Payload
	}
	s.log.Info("uploadVideoMetadata: metadata.json uploaded",
		zap.String("asset_type", result.Payload.AssetType),
		zap.String("style", style),
		zap.String("search_text", result.Payload.SearchText),
	)
	return result.Payload
}

func (s *ImageStorageService) registerAudioClip(ctx context.Context, videoPath, description, style, source string, durationSec int, videoID, subject string) {
	if s.meta == nil || s.meta.metaWriter == nil {
		s.log.Warn("registerAudioClip: metadata writer not configured")
		return
	}

	audioPath := filepath.Join(s.tempDir, videoID+"_audio.mp3")
	if err := audio.ExtractClip(ctx, "", videoPath, audioPath, 3); err != nil {
		s.log.Warn("registerAudioClip: audio extraction failed", zap.String("video_id", videoID), zap.Error(err))
		return
	}
	defer os.Remove(audioPath)

	req := drive.AssetDestinationRequest{
		Source:    drive.SourceSoundEffect,
		MediaType: drive.MediaTypeSoundEffect,
		Subject:   subject,
		Hash:      videoID + "_audio",
		Ext:       ".mp3",
		Style:     style,
	}

	folderID, err := s.mediaStore.EnsureDriveFolder(ctx, req)
	if err != nil {
		s.log.Warn("registerAudioClip: EnsureDriveFolder failed", zap.Error(err))
		return
	}

	fileID, webLink, err := s.mediaStore.UploadToDrive(ctx, req, audioPath)
	if err != nil {
		s.log.Warn("registerAudioClip: Drive upload failed", zap.Error(err))
		return
	}

	result, err := s.meta.metaWriter.Write(ctx, semantic.WriteRequest{
		AssetID:    videoID + "_audio",
		AssetType:  "sound_effect",
		MediaType:  "audio",
		Source:     source,
		Generator:  source,
		Style:      style,
		Prompt:     description,
		TempDir:    s.tempDir,
		Extensions: semantic.BuildAudioExtension(3, 0, 0, true, videoID),
	})

	var searchText string
	var tags []string
	if err == nil && result != nil && result.Payload != nil {
		searchText = result.Payload.SearchText
		tags = result.Payload.Tags
		audioReq := req
		audioReq.Hash = "metadata"
		audioReq.Ext = ".json"
		if _, _, err := s.mediaStore.UploadToDrive(ctx, audioReq, result.LocalPath); err != nil {
			s.log.Warn("registerAudioClip: metadata upload failed", zap.Error(err))
		}
	} else {
		s.log.Warn("registerAudioClip: metadata writer failed", zap.Error(err))
	}

	clip := &asset.Asset{
		ID:         videoID + "_audio",
		Name:       description + " (audio)",
		Source:     asset.Source(source),
		MediaType:  asset.MediaType("sound_effect"),
		Duration:   3000 * time.Millisecond,
		CreatedAt:  time.Now(),
		SearchText: searchText,
		Tags:       tags,
	}
	clip.SetLocalPath(audioPath)
	clip.SetDriveFileID(fileID)
	clip.SetDriveLink(webLink)
	clip.SetFolderID(folderID)
	if style != "" {
		clip.Group = style
	}

	if s.dispatcher != nil {
		contentHash := sha256Hash(audioPath)
		if err := s.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
			s.log.Warn("registerAudioClip: dispatcher upsert failed", zap.Error(err))
			return
		}
		s.log.Debug("registerAudioClip: saved via dispatcher", zap.String("id", clip.ID))
	} else if err := s.stockRepo.Upsert(ctx, clip); err != nil {
		s.log.Warn("registerAudioClip: DB upsert failed", zap.Error(err))
		return
	}
	s.log.Info("registerAudioClip: audio extracted, uploaded, and registered",
		zap.String("video_id", videoID),
		zap.String("audio_id", clip.ID),
		zap.String("drive_link", webLink),
		zap.Int("tags_count", len(tags)),
	)
}

// ── Drive Sync ─────────────────────────────────────────────────────────

// SyncFromDrive syncs image assets from Google Drive to the local DB.
func (s *ImageStorageService) SyncFromDrive(ctx context.Context) error {
	if s.driveSvc == nil || s.driveFolderID == "" {
		return fmt.Errorf("drive service or folder ID not configured")
	}
	s.log.Info("Starting images sync from Drive", zap.String("folder_id", s.driveFolderID))
	return s.syncFolderRecursive(ctx, s.driveFolderID, "")
}

func (s *ImageStorageService) syncFolderRecursive(ctx context.Context, folderID, folderPath string) error {
	uploader := &drive.Uploader{Service: s.driveSvc}
	files, err := uploader.ListFiles(ctx, folderID)
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
	if !isAIImageSource(source) {
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

package images

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/media/storage"
	"velox/go-master/internal/pkg/media/audio"
)

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
	var folderID string
	var semanticMeta *SemanticMetadataPayload
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

		// Get the Drive folder ID before uploading (needed for metadata aggregation)
		folderID, _ = s.mediaStore.EnsureDriveFolder(ctx, req)

		fid, wl, err := s.mediaStore.UploadToDrive(ctx, req, filePath)
		if err != nil {
			s.log.Warn("RegisterVideoAsset: Drive upload failed (non fatale)", zap.Error(err))
		} else {
			driveFileID = fid
			driveLink = wl
			uploaded = true
			s.log.Info("RegisterVideoAsset: Drive upload successful", zap.String("file_id", fid))

			// Upload semantic metadata.json to the same Drive folder
			semanticMeta = s.uploadVideoMetadata(ctx, req, description, style, source, fid, wl, durationSec, id, filePath, folderID)
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

	// Populate semantic fields from tagger output
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

	if err := s.stockRepo.UpsertClip(ctx, clip); err != nil {
		return err
	}

	// Estrai audio dal video, taglia a 3 secondi, carica su Drive e registra in DB
	if uploaded && s.mediaStore != nil {
		s.registerAudioClip(ctx, filePath, description, style, source, durationSec, id, subject)
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

// uploadVideoMetadata calls the unified semantic.MetadataWriter and uploads metadata.json to Drive.
// Returns the payload for use in DB fields (search_text, tags, etc.).
func (s *Service) uploadVideoMetadata(ctx context.Context, req storage.AssetDestinationRequest, prompt, style, generator, fileID, driveLink string, durationSec int, hash, localPath, folderID string) *SemanticMetadataPayload {
	if s.metaWriter == nil {
		s.log.Warn("uploadVideoMetadata: metadata writer not configured")
		return nil
	}

	result, err := s.metaWriter.Write(ctx, semantic.WriteRequest{
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

	// Upload metadata.json via Drive
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

// registerAudioClip estrae l'audio dal video, carica su Drive (sound effects) e registra in DB.
// Uses the unified semantic.MetadataWriter for search_text and tags.
func (s *Service) registerAudioClip(ctx context.Context, videoPath, description, style, source string, durationSec int, videoID, subject string) {
	if s.metaWriter == nil {
		s.log.Warn("registerAudioClip: metadata writer not configured")
		return
	}

	audioPath := filepath.Join(s.tempDir, videoID+"_audio.mp3")
	if err := audio.ExtractClip(ctx, "", videoPath, audioPath, 3); err != nil {
		s.log.Warn("registerAudioClip: audio extraction failed", zap.String("video_id", videoID), zap.Error(err))
		return
	}
	defer os.Remove(audioPath)

	req := storage.AssetDestinationRequest{
		Source:    storage.SourceSoundEffect,
		MediaType: storage.MediaTypeSoundEffect,
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

	// Use unified MetadataWriter for audio metadata
	result, err := s.metaWriter.Write(ctx, semantic.WriteRequest{
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
		// Upload metadata.json via Drive
		audioReq := req
		audioReq.Hash = "metadata"
		audioReq.Ext = ".json"
		if _, _, err := s.mediaStore.UploadToDrive(ctx, audioReq, result.LocalPath); err != nil {
			s.log.Warn("registerAudioClip: metadata upload failed", zap.Error(err))
		}
	} else {
		s.log.Warn("registerAudioClip: metadata writer failed", zap.Error(err))
	}

	clip := &models.MediaAsset{
		ID:          videoID + "_audio",
		Name:        description + " (audio)",
		Source:      source,
		MediaType:   "sound_effect",
		LocalPath:   audioPath,
		DriveFileID: fileID,
		DriveLink:   webLink,
		FolderID:    folderID,
		Status:      "ready",
		Duration:    3,
		CreatedAt:   time.Now(),
		SearchText:  searchText,
		Tags:        tags,
	}
	if style != "" {
		clip.Group = style
	}

	if err := s.stockRepo.UpsertClip(ctx, clip); err != nil {
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

// sha256Hash calcola l'hash SHA256 di una stringa (es. percorso file).
func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}

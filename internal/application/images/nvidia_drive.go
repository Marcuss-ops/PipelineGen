package images

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	driveupload "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
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
	uploader := &driveupload.Uploader{Service: s.driveSvc}
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

		// Skip non-image files (basic check)
		lowerName := strings.ToLower(file.Name)
		if !strings.HasSuffix(lowerName, ".jpg") && !strings.HasSuffix(lowerName, ".jpeg") &&
			!strings.HasSuffix(lowerName, ".png") && !strings.HasSuffix(lowerName, ".webp") {
			continue
		}

		// Check if already exists by Drive ID
		existing, err := s.repo.GetByDriveFileID(ctx, file.ID)
		if err == nil && existing != nil {
			continue
		}

		// Create metadata-only record
		asset := &asset.ImageAsset{
			SubjectID:    textutil.Slugify(file.Name),
			Hash:         "drive_" + file.ID, // Placeholder hash
			PathRel:      "",                 // No local path yet
			SourceURL:    file.WebViewLink,
			Description:  "Synced from Drive: " + file.Name,
			DriveFileID:  file.ID,
			Status:       "ready",
			MetadataJSON: "{}",
		}

		if _, err := s.repo.AddImage(ctx, asset); err != nil {
			s.log.Warn("failed to add synced image", zap.String("name", file.Name), zap.Error(err))
		}
	}

	return nil
}

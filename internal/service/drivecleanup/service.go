package drivecleanup

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/repository/clips"
	driveupload "velox/go-master/internal/upload/drive"
	driveutil "velox/go-master/pkg/drive"
)

// Service handles synchronized deletion between SQLite and Google Drive.
type Service struct {
	repo     *clips.Repository
	driveSvc *driveapi.Service
	log      *zap.Logger
	useTrash bool
}

// NewService creates a new drive cleanup service.
// If useTrash is true, files are moved to trash instead of being permanently deleted.
func NewService(repo *clips.Repository, driveSvc *driveapi.Service, log *zap.Logger, useTrash bool) *Service {
	return &Service{
		repo:     repo,
		driveSvc: driveSvc,
		log:      log,
		useTrash: useTrash,
	}
}

// DeleteClipAndDriveFile deletes a clip from both SQLite and Google Drive.
// If useTrash is true, the file is moved to trash; otherwise it's permanently deleted.
func (s *Service) DeleteClipAndDriveFile(ctx context.Context, clipID string, useTrash bool) error {
	clipID = strings.TrimSpace(clipID)
	if clipID == "" {
		return fmt.Errorf("clip id is required")
	}

	clip, err := s.repo.GetClip(ctx, clipID)
	if err != nil {
		return fmt.Errorf("failed to get clip: %w", err)
	}

	fileID := driveutil.FileIDFromLink(clip.DriveLink)
	if fileID == "" {
		fileID = driveutil.FileIDFromLink(clip.DownloadLink)
	}

	if fileID != "" && s.driveSvc != nil {
		uploader := &driveupload.Uploader{
			Service: s.driveSvc,
			Log:     s.log,
		}

		if useTrash {
			if err := uploader.TrashFile(ctx, fileID); err != nil {
				s.log.Error("failed to trash drive file", zap.String("file_id", fileID), zap.Error(err))
				// Continue to delete from DB even if Drive operation fails
			}
		} else {
			if err := uploader.DeleteFile(ctx, fileID); err != nil {
				s.log.Error("failed to delete drive file", zap.String("file_id", fileID), zap.Error(err))
				// Continue to delete from DB even if Drive operation fails
			}
		}
	}

	if err := s.repo.DeleteClip(ctx, clipID); err != nil {
		return fmt.Errorf("failed to delete sqlite clip: %w", err)
	}

	s.log.Info("clip deleted from db and drive",
		zap.String("clip_id", clipID),
		zap.String("file_id", fileID),
		zap.Bool("trashed", useTrash),
	)

	return nil
}

// TrashClip moves the clip's Drive file to trash and removes the SQLite record.
func (s *Service) TrashClip(ctx context.Context, clipID string) error {
	return s.DeleteClipAndDriveFile(ctx, clipID, true)
}

// DeleteClipPermanently deletes the clip's Drive file permanently and removes the SQLite record.
func (s *Service) DeleteClipPermanently(ctx context.Context, clipID string) error {
	return s.DeleteClipAndDriveFile(ctx, clipID, false)
}

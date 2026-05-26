package drivecleanup

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
	driveupload "velox/go-master/internal/upload/drive"
	driveutil "velox/go-master/internal/storage/drive"
)

// Service handles synchronized deletion between SQLite and Google Drive.
type Service struct {
	repo     *clips.Repository
	uploader *driveupload.Uploader
	log      *zap.Logger
	useTrash bool
}

// NewService creates a new drive cleanup service.
// If useTrash is true, files are moved to trash instead of being permanently deleted.
func NewService(repo *clips.Repository, uploader *driveupload.Uploader, log *zap.Logger, useTrash bool) *Service {
	return &Service{
		repo:     repo,
		uploader: uploader,
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

	if fileID != "" && s.uploader != nil {
		if useTrash {
			if err := s.uploader.TrashFile(ctx, fileID); err != nil {
				s.log.Error("failed to trash drive file", zap.String("file_id", fileID), zap.Error(err))
				// Continue to delete from DB even if Drive operation fails
			}
		} else {
			if err := s.uploader.DeleteFile(ctx, fileID); err != nil {
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

// DeleteFolderAndClips deletes a folder from Google Drive and all associated clips from SQLite.
// If useTrash is true, the folder and files are moved to trash.
func (s *Service) DeleteFolderAndClips(ctx context.Context, folderID string, useTrash bool) error {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return fmt.Errorf("folder id is required")
	}

	// Get all clips in this folder
	clips, err := s.repo.ListClipsByFolderID(ctx, folderID)
	if err != nil {
		return fmt.Errorf("failed to list clips in folder: %w", err)
	}

	s.log.Info("deleting folder and clips",
		zap.String("folder_id", folderID),
		zap.Int("clip_count", len(clips)),
		zap.Bool("trash", useTrash),
	)

	// Delete all clips first
	for _, clip := range clips {
		if err := s.DeleteClipAndDriveFile(ctx, clip.ID, useTrash); err != nil {
			s.log.Error("failed to delete clip",
				zap.String("clip_id", clip.ID),
				zap.Error(err),
			)
			// Continue with other clips
		}
	}

	// Delete the folder from Drive
	if s.uploader != nil {
		if useTrash {
			if err := s.uploader.TrashFolder(ctx, folderID); err != nil {
				s.log.Error("failed to trash drive folder", zap.String("folder_id", folderID), zap.Error(err))
			}
		} else {
			if err := s.uploader.DeleteFolder(ctx, folderID); err != nil {
				s.log.Error("failed to delete drive folder", zap.String("folder_id", folderID), zap.Error(err))
			}
		}
	}

	// Delete the clip_folder record if it exists
	if err := s.repo.DeleteClipFolder(ctx, folderID); err != nil {
		s.log.Warn("failed to delete clip folder record", zap.String("folder_id", folderID), zap.Error(err))
	}

	s.log.Info("folder and clips deleted",
		zap.String("folder_id", folderID),
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

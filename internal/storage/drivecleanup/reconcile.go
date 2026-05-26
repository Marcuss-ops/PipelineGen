package drivecleanup

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	driveupload "velox/go-master/internal/upload/drive"
	driveutil "velox/go-master/internal/storage/drive"
)

// ReconcileResult holds the result of a reconcile operation.
type ReconcileResult struct {
	Source               string   `json:"source"`
	DryRun               bool     `json:"dry_run"`
	SQLiteMissingOnDrive int      `json:"sqlite_missing_on_drive"`
	DriveMissingInSQLite int      `json:"drive_missing_in_sqlite"`
	DeletedFromSQLite    int      `json:"deleted_from_sqlite"`
	AddedToSQLite        int      `json:"added_to_sqlite"`
	MissingFileIDs       []string `json:"missing_file_ids,omitempty"`
	OrphanedDriveFileIDs []string `json:"orphaned_drive_file_ids,omitempty"`
}

// Reconcile compares SQLite clips with Drive files and reports mismatches.
// If dryRun is true, no changes are made.
func (s *Service) Reconcile(ctx context.Context, source, rootFolderID string, dryRun bool) (*ReconcileResult, error) {
	result := &ReconcileResult{
		Source: source,
		DryRun: dryRun,
	}

	clipsList, err := s.repo.ListClips(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("failed to list clips: %w", err)
	}

	sqliteClipIDs := make(map[string]bool)
	for _, clip := range clipsList {
		sqliteClipIDs[clip.ID] = true
	}

	for _, clip := range clipsList {
		fileID := driveutil.FileIDFromLink(clip.DriveLink)
		if fileID == "" {
			fileID = driveutil.FileIDFromLink(clip.DownloadLink)
		}

		if fileID == "" {
			continue
		}

		exists, err := s.fileExistsAndNotTrashed(ctx, fileID)
		if err != nil {
			s.log.Warn("failed to check file existence",
				zap.String("file_id", fileID),
				zap.Error(err),
			)
			continue
		}

		if !exists {
			result.SQLiteMissingOnDrive++
			result.MissingFileIDs = append(result.MissingFileIDs, fileID)

			if !dryRun {
				if err := s.repo.DeleteClip(ctx, clip.ID); err != nil {
					s.log.Error("failed to delete orphaned clip record",
						zap.String("clip_id", clip.ID),
						zap.Error(err),
					)
				} else {
					result.DeletedFromSQLite++
				}
			}
		}
	}

	if rootFolderID != "" && s.uploader != nil {
		driveFiles, err := s.listDriveFilesRecursive(ctx, rootFolderID)
		if err != nil {
			s.log.Warn("failed to list drive files",
				zap.String("root_folder_id", rootFolderID),
				zap.Error(err),
			)
		} else {
			for _, file := range driveFiles {
				if file.MimeType == "application/vnd.google-apps.folder" {
					continue
				}
				if !sqliteClipIDs[file.ID] {
					result.DriveMissingInSQLite++
					result.OrphanedDriveFileIDs = append(result.OrphanedDriveFileIDs, file.ID)
				}
			}
		}
	}

	return result, nil
}

func (s *Service) listDriveFilesRecursive(ctx context.Context, folderID string) ([]driveupload.DriveFileInfo, error) {
	var allFiles []driveupload.DriveFileInfo
	err := s.listDriveFilesRecursiveHelper(ctx, folderID, &allFiles)
	return allFiles, err
}

func (s *Service) listDriveFilesRecursiveHelper(ctx context.Context, folderID string, allFiles *[]driveupload.DriveFileInfo) error {
	files, err := s.uploader.ListFiles(ctx, folderID)
	if err != nil {
		return err
	}

	for _, file := range files {
		*allFiles = append(*allFiles, file)
		if file.MimeType == "application/vnd.google-apps.folder" {
			if err := s.listDriveFilesRecursiveHelper(ctx, file.ID, allFiles); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) fileExistsAndNotTrashed(ctx context.Context, fileID string) (bool, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return false, nil
	}

	if s.uploader == nil {
		return false, fmt.Errorf("uploader not configured")
	}

	return s.uploader.FileExists(ctx, fileID)
}

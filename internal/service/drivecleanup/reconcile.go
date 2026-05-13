package drivecleanup

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	driveutil "velox/go-master/pkg/drive"
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
	OrphanedDriveFileIDs  []string `json:"orphaned_drive_file_ids,omitempty"`
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

	if rootFolderID != "" && s.driveSvc != nil {
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
				if !sqliteClipIDs[file.Id] {
					result.DriveMissingInSQLite++
					result.OrphanedDriveFileIDs = append(result.OrphanedDriveFileIDs, file.Id)
				}
			}
		}
	}

	return result, nil
}

func (s *Service) listDriveFilesRecursive(ctx context.Context, folderID string) ([]*driveapi.File, error) {
	var allFiles []*driveapi.File
	err := s.listDriveFilesRecursiveHelper(ctx, folderID, &allFiles)
	return allFiles, err
}

func (s *Service) listDriveFilesRecursiveHelper(ctx context.Context, folderID string, allFiles *[]*driveapi.File) error {
	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	call := s.driveSvc.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType)").
		PageSize(1000).
		Context(ctx)

	return call.Pages(ctx, func(fl *driveapi.FileList) error {
		for _, file := range fl.Files {
			*allFiles = append(*allFiles, file)
			if file.MimeType == "application/vnd.google-apps.folder" {
				if err := s.listDriveFilesRecursiveHelper(ctx, file.Id, allFiles); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Service) fileExistsAndNotTrashed(ctx context.Context, fileID string) (bool, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return false, nil
	}

	file, err := s.driveSvc.Files.Get(fileID).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		return false, nil
	}

	return !file.Trashed, nil
}

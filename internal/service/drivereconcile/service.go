package drivereconcile

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/repository/clips"
	driveutil "velox/go-master/pkg/drive"
)

// ReconcileResult holds the result of a reconcile operation.
type ReconcileResult struct {
	Source                 string `json:"source"`
	DryRun                 bool   `json:"dry_run"`
	SQLiteMissingOnDrive   int    `json:"sqlite_missing_on_drive"`
	DriveMissingInSQLite   int    `json:"drive_missing_in_sqlite"`
	WouldDeleteSQLite      int    `json:"would_delete_sqlite"`
	WouldInsertSQLite      int    `json:"would_insert_sqlite"`
	MissingFileIDs        []string `json:"missing_file_ids,omitempty"`
}

// Service handles reconciliation between SQLite and Google Drive.
type Service struct {
	repo     *clips.Repository
	driveSvc *driveapi.Service
	log      *zap.Logger
}

// NewService creates a new reconcile service.
func NewService(repo *clips.Repository, driveSvc *driveapi.Service, log *zap.Logger) *Service {
	return &Service{
		repo:     repo,
		driveSvc: driveSvc,
		log:      log,
	}
}

// Reconcile compares SQLite clips with Drive files and reports mismatches.
// If dryRun is true, no changes are made.
func (s *Service) Reconcile(ctx context.Context, source, rootFolderID string, dryRun bool) (*ReconcileResult, error) {
	result := &ReconcileResult{
		Source: source,
		DryRun: dryRun,
	}

	// Get all clips from SQLite for the source
	clips, err := s.repo.ListClips(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("failed to list clips: %w", err)
	}

	// Build set of SQLite clip IDs for Drive -> SQLite check
	sqliteClipIDs := make(map[string]bool)
	for _, clip := range clips {
		sqliteClipIDs[clip.ID] = true
	}

	// Check each clip's Drive file (SQLite -> Drive)
	for _, clip := range clips {
		fileID := driveutil.FileIDFromLink(clip.DriveLink)
		if fileID == "" {
			fileID = driveutil.FileIDFromLink(clip.DownloadLink)
		}

		if fileID == "" {
			continue
		}

		// Check if file exists on Drive and is not trashed
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
			result.WouldDeleteSQLite++
			result.MissingFileIDs = append(result.MissingFileIDs, fileID)
		}
	}

	// Drive -> SQLite check (if rootFolderID is provided)
	if rootFolderID != "" && s.driveSvc != nil {
		driveFiles, err := s.listDriveFilesRecursive(ctx, rootFolderID)
		if err != nil {
			s.log.Warn("failed to list drive files",
				zap.String("root_folder_id", rootFolderID),
				zap.Error(err),
			)
		} else {
			for _, file := range driveFiles {
				// Skip folders
				if file.MimeType == "application/vnd.google-apps.folder" {
					continue
				}
				// Check if this file exists in SQLite
				if !sqliteClipIDs[file.Id] {
					result.DriveMissingInSQLite++
					result.WouldInsertSQLite++
				}
			}
		}
	}

	return result, nil
}

// listDriveFilesRecursive lists all files in a Drive folder recursively.
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
			// Recurse into folders
			if file.MimeType == "application/vnd.google-apps.folder" {
				if err := s.listDriveFilesRecursiveHelper(ctx, file.Id, allFiles); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// fileExistsAndNotTrashed checks if a file exists on Drive and is not in trash.
func (s *Service) fileExistsAndNotTrashed(ctx context.Context, fileID string) (bool, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return false, nil
	}

	file, err := s.driveSvc.Files.Get(fileID).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		// File doesn't exist or API error
		return false, nil
	}

	return !file.Trashed, nil
}

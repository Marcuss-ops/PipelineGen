package storage

import (
	"context"
	"fmt"
)

// Service orchestrates Drive file/folder operations through a narrow DrivePort.
type Service struct {
	drive DrivePort
	log   Logger
}

// NewService creates a storage Service.
func NewService(drive DrivePort, log Logger) *Service {
	return &Service{drive: drive, log: log}
}

// ── File operations ──────────────────────────────────────────────────

// ListFilesRequest is the input for listing files in a folder.
type ListFilesRequest struct {
	FolderID string `json:"folder_id"`
}

// ListFilesResult is the output of a list-files operation.
type ListFilesResult struct {
	FolderID string      `json:"folder_id"`
	Files    []DriveFile `json:"files"`
	Count    int         `json:"count"`
}

// ListFiles returns non-trashed files in a Drive folder.
func (s *Service) ListFiles(ctx context.Context, req ListFilesRequest) (*ListFilesResult, error) {
	if req.FolderID == "" {
		return nil, fmt.Errorf("folder_id is required")
	}
	if s.drive == nil {
		return nil, fmt.Errorf("drive port not configured")
	}
	files, err := s.drive.ListFiles(ctx, req.FolderID)
	if err != nil {
		s.log.Error("list files failed", "folder_id", req.FolderID, "error", err)
		return nil, fmt.Errorf("list files: %w", err)
	}
	return &ListFilesResult{
		FolderID: req.FolderID,
		Files:    files,
		Count:    len(files),
	}, nil
}

// MoveFilesRequest is the input for moving files between folders.
type MoveFilesRequest struct {
	FileIDs      []string `json:"file_ids"`
	FromFolderID string   `json:"from_folder_id"`
	ToFolderID   string   `json:"to_folder_id"`
}

// MoveFilesResult is the output of a move-files operation.
type MoveFilesResult struct {
	Moved      int      `json:"moved"`
	Errors     []string `json:"errors,omitempty"`
	ErrorCount int      `json:"error_count"`
}

// MoveFiles moves one or more files from one Drive folder to another.
func (s *Service) MoveFiles(ctx context.Context, req MoveFilesRequest) (*MoveFilesResult, error) {
	if req.ToFolderID == "" {
		return nil, fmt.Errorf("to_folder_id is required")
	}
	if len(req.FileIDs) == 0 {
		return nil, fmt.Errorf("at least one file_id is required")
	}
	if s.drive == nil {
		return nil, fmt.Errorf("drive port not configured")
	}

	result := &MoveFilesResult{}
	for _, fid := range req.FileIDs {
		if err := s.drive.MoveFile(ctx, fid, req.FromFolderID, req.ToFolderID); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", fid, err))
			continue
		}
		result.Moved++
	}
	result.ErrorCount = len(result.Errors)
	return result, nil
}

// RenameFileRequest is the input for renaming a Drive file or folder.
type RenameFileRequest struct {
	FileID  string `json:"file_id"`
	NewName string `json:"new_name"`
}

// RenameFileResult is the output of a rename operation.
type RenameFileResult struct {
	FileID  string `json:"file_id"`
	NewName string `json:"new_name"`
}

// RenameFile renames a file or folder on Drive.
func (s *Service) RenameFile(ctx context.Context, req RenameFileRequest) (*RenameFileResult, error) {
	if req.FileID == "" {
		return nil, fmt.Errorf("file_id is required")
	}
	if req.NewName == "" {
		return nil, fmt.Errorf("new_name is required")
	}
	if s.drive == nil {
		return nil, fmt.Errorf("drive port not configured")
	}
	if err := s.drive.RenameFile(ctx, req.FileID, req.NewName); err != nil {
		s.log.Error("rename file failed", "file_id", req.FileID, "new_name", req.NewName, "error", err)
		return nil, fmt.Errorf("rename file: %w", err)
	}
	s.log.Info("file renamed", "file_id", req.FileID, "new_name", req.NewName)
	return &RenameFileResult{FileID: req.FileID, NewName: req.NewName}, nil
}

// ── Folder operations ────────────────────────────────────────────────

// CreateFolderRequest is the input for creating a Drive folder.
type CreateFolderRequest struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

// CreateFolderResult is the output of a folder creation operation.
type CreateFolderResult struct {
	FolderID string `json:"folder_id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

// CreateFolder creates (or reuses) a folder on Drive.
func (s *Service) CreateFolder(ctx context.Context, req CreateFolderRequest) (*CreateFolderResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if s.drive == nil {
		return nil, fmt.Errorf("drive port not configured")
	}
	folderID, err := s.drive.GetOrCreateFolder(ctx, req.Name, req.ParentID)
	if err != nil {
		s.log.Error("create folder failed", "name", req.Name, "parent_id", req.ParentID, "error", err)
		return nil, fmt.Errorf("create folder: %w", err)
	}
	s.log.Info("folder ready", "folder_id", folderID, "name", req.Name)
	return &CreateFolderResult{
		FolderID: folderID,
		Name:     req.Name,
		ParentID: req.ParentID,
	}, nil
}

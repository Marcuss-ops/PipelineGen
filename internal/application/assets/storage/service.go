// Package storage provides the application-layer storage orchestration.
// It exposes a DrivePort abstraction so HTTP handlers never depend on
// the concrete Drive infrastructure directly.
package storage

import (
	"context"
	"fmt"
)

// DriveFile is the canonical file descriptor returned by ListFiles.
type DriveFile struct {
	ID       string
	Name     string
	MimeType string
}

// DrivePort is the narrow storage abstraction consumed by this service.
// The concrete implementation is wired in internal/app/module_assets.go
// via storageDriveAdapter (adapting drive.Uploader).
type DrivePort interface {
	ListFiles(ctx context.Context, folderID string) ([]DriveFile, error)
	MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
	RenameFile(ctx context.Context, fileID, newName string) error
}

// Logger is the structured-logging interface consumed by this service.
// Implementations adapt *zap.Logger or other structured loggers.
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
}

// Service orchestrates storage operations through the DrivePort.
type Service struct {
	drive DrivePort
	log   Logger
}

// NewService creates a storage Service.
func NewService(drive DrivePort, log Logger) *Service {
	return &Service{drive: drive, log: log}
}

// ListFilesResult is the output of a list-files operation.
type ListFilesResult struct {
	Files []DriveFile `json:"files"`
}

// ListFiles lists files in a Drive folder.
func (s *Service) ListFiles(ctx context.Context, folderID string) (*ListFilesResult, error) {
	if s.drive == nil {
		return nil, fmt.Errorf("drive port not wired")
	}
	files, err := s.drive.ListFiles(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	return &ListFilesResult{Files: files}, nil
}

// MoveFile moves a file between Drive folders.
func (s *Service) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	if s.drive == nil {
		return fmt.Errorf("drive port not wired")
	}
	return s.drive.MoveFile(ctx, fileID, fromFolderID, toFolderID)
}

// CreateFolder creates a folder on Drive.
func (s *Service) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if s.drive == nil {
		return "", fmt.Errorf("drive port not wired")
	}
	return s.drive.GetOrCreateFolder(ctx, name, parentID)
}

// RenameFile renames a file on Drive.
func (s *Service) RenameFile(ctx context.Context, fileID, newName string) error {
	if s.drive == nil {
		return fmt.Errorf("drive port not wired")
	}
	return s.drive.RenameFile(ctx, fileID, newName)
}

// Package storage provides application-layer use cases for Drive file/folder
// management: list files, move files, create folders, and rename files.
// All operations delegate through a narrow DrivePort to keep the layer
// infrastructure-free.
package storage

import "context"

// DriveFile is a summary entry returned by ListFiles.
type DriveFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type,omitempty"`
}

// DrivePort is the narrow interface for Google Drive file/folder operations.
// The concrete adapter lives in internal/app (storageDriveAdapter) and wraps
// drive.Uploader.
type DrivePort interface {
	ListFiles(ctx context.Context, folderID string) ([]DriveFile, error)
	MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
	RenameFile(ctx context.Context, fileID, newName string) error
}

// Logger is a narrow logging port (mirrors the pattern in diagnostics + search).
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
}

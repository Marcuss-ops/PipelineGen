package storage

import "context"

// DriveFile represents a file descriptor from Drive.
type DriveFile struct {
	ID       string
	Name     string
	MimeType string
}

// DrivePort is the storage interface for Drive operations.
type DrivePort interface {
	ListFiles(ctx context.Context, folderID string) ([]DriveFile, error)
	MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
	RenameFile(ctx context.Context, fileID, newName string) error
}

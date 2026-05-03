// Package drive defines adapter interfaces for Google Drive operations.
//
// STATUS: EXPERIMENTAL - Interface defined but not yet implemented or used.
// TODO: Implement and migrate Drive upload to use this adapter.
package drive

import "context"

type UploadInput struct {
	LocalPath string
	FolderID  string
	Name      string
}

type UploadResult struct {
	FileID string
	URL    string
}

type DriveAdapter interface {
	Upload(ctx context.Context, input UploadInput) (*UploadResult, error)
}

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

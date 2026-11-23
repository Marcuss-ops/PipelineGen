package core

import (
	"context"
	"io"
)

// DriveFileInfo is the application-layer representation of a Google Drive
// file entry returned by ListFiles. Defined locally so the DriveReaderPort
// stays free of internal/platform/drive imports (godlike/06
// import-boundary discipline). The composition root adapts the concrete
// drive.DriveFileInfo to this type.
type DriveFileInfo struct {
	ID       string
	MimeType string
	// Name is the file's display name (used by the destination reconciler for
	// its removal audit trail).
	Name string
	// PipelineOwned reports that the file was written by this pipeline (the
	// Drive adapter decides it from the P0.6 idempotency-key appProperty).
	// Operator uploads are never flagged, so the reconciler can never delete a
	// human's file.
	PipelineOwned bool
}

// DriveReaderPort is the Google Drive read-side port used by the
// stock pipeline to download a single file and to list the contents
// of a Drive folder (so a folder URL can be expanded to the first
// video). The concrete *drive.Uploader is adapted at the composition
// root via a thin wrapper.
type DriveReaderPort interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	ListFiles(ctx context.Context, parentID string) ([]DriveFileInfo, error)
	// TrashFile moves one file to the Drive trash. Used exclusively by the
	// post-publish destination reconciler (step_publish.go) to remove
	// stale artifacts of earlier plans.
	TrashFile(ctx context.Context, fileID string) error
}

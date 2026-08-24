// Package aistock — typed ports for AI-generated stock clip ingestion.
package clips

import (
	"context"
	"io"
)

// DriveReaderPort is the narrow surface needed to read a video file from
// Google Drive. It is intentionally smaller than the full drive.Reader so
// the use case depends only on what it actually uses.
type DriveReaderPort interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	GetFileMeta(ctx context.Context, fileID string) (*DriveFileMeta, error)
}

// DriveFileMeta holds the metadata we need from a Drive file.
type DriveFileMeta struct {
	Name string
}

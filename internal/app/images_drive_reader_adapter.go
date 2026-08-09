package app

import (
	"context"
	"fmt"

	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// imagesDriveReaderAdapter keeps Drive-specific DTOs at the composition root.
type imagesDriveReader interface {
	ListFiles(context.Context, string) ([]drive.DriveFileInfo, error)
}

type imagesDriveReaderAdapter struct {
	reader imagesDriveReader
}

var _ imgservice.DriveReader = (*imagesDriveReaderAdapter)(nil)

func newImagesDriveReaderAdapter(reader imagesDriveReader) imgservice.DriveReader {
	if reader == nil {
		return nil
	}
	return &imagesDriveReaderAdapter{reader: reader}
}

func (a *imagesDriveReaderAdapter) ListFiles(ctx context.Context, parentID string) ([]imgservice.DriveFile, error) {
	if a == nil || a.reader == nil {
		return nil, fmt.Errorf("images DriveReader is not configured")
	}
	files, err := a.reader.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]imgservice.DriveFile, len(files))
	for i, file := range files {
		out[i] = imgservice.DriveFile{
			ID:          file.ID,
			Name:        file.Name,
			MimeType:    file.MimeType,
			WebViewLink: file.WebViewLink,
		}
	}
	return out, nil
}

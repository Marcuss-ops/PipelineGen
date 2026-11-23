package assets

import (
	"context"
	"fmt"

	imageingest "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// ImagesDriveReader is the narrow Drive port needed by image ingestion.
type ImagesDriveReader interface {
	ListFiles(context.Context, string) ([]drive.DriveFileInfo, error)
}

type ImagesDriveReaderAdapter struct {
	reader ImagesDriveReader
}

var _ imageingest.DriveReader = (*ImagesDriveReaderAdapter)(nil)

func NewImagesDriveReaderAdapter(reader ImagesDriveReader) imageingest.DriveReader {
	if reader == nil {
		return nil
	}
	return &ImagesDriveReaderAdapter{reader: reader}
}

func (a *ImagesDriveReaderAdapter) ListFiles(ctx context.Context, parentID string) ([]imageingest.DriveFile, error) {
	if a == nil || a.reader == nil {
		return nil, fmt.Errorf("images DriveReader is not configured")
	}
	files, err := a.reader.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]imageingest.DriveFile, len(files))
	for i, file := range files {
		out[i] = imageingest.DriveFile{
			ID:          file.ID,
			Name:        file.Name,
			MimeType:    file.MimeType,
			WebViewLink: file.WebViewLink,
		}
	}
	return out, nil
}

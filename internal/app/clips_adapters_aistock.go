package app

import (
	"context"
	"io"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clips/aistock"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// aistockDriveReaderAdapter adapts drive.Reader to aistock.DriveReaderPort.
type aistockDriveReaderAdapter struct {
	reader drive.Reader
}

// Compile-time assertion.
var _ aistock.DriveReaderPort = (*aistockDriveReaderAdapter)(nil)

func newAistockDriveReaderAdapter(reader drive.Reader) aistock.DriveReaderPort {
	if reader == nil {
		return nil
	}
	return &aistockDriveReaderAdapter{reader: reader}
}

func (a *aistockDriveReaderAdapter) DownloadFile(ctx context.Context, fileID string) (body io.ReadCloser, contentType string, err error) {
	return a.reader.DownloadFile(ctx, fileID)
}

func (a *aistockDriveReaderAdapter) GetFileMeta(ctx context.Context, fileID string) (*aistock.DriveFileMeta, error) {
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil
	}
	return &aistock.DriveFileMeta{Name: meta.Name}, nil
}

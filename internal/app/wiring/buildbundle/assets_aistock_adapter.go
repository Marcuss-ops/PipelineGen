package buildbundle

import (
	"context"
	"io"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips/aistock"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type aistockDriveReaderAdapter struct{ reader drive.Reader }

var _ aistock.DriveReaderPort = (*aistockDriveReaderAdapter)(nil)

func newAistockDriveReaderAdapter(reader drive.Reader) aistock.DriveReaderPort {
	if reader == nil {
		return nil
	}
	return &aistockDriveReaderAdapter{reader: reader}
}

func (a *aistockDriveReaderAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
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

package drive

import (
	"context"
	"io"

	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
)

// AdminMediaReader adapts the canonical Drive Reader to the narrow
// application port used by operator media workflows.
type AdminMediaReader struct {
	Reader Reader
}

func (a AdminMediaReader) ListFiles(ctx context.Context, parentID string) ([]adminmedia.DriveAudioFile, error) {
	files, err := a.Reader.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}
	result := make([]adminmedia.DriveAudioFile, 0, len(files))
	for _, file := range files {
		result = append(result, adminmedia.DriveAudioFile{ID: file.ID, Name: file.Name, MimeType: file.MimeType})
	}
	return result, nil
}

func (a AdminMediaReader) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, error) {
	body, _, err := a.Reader.DownloadFile(ctx, fileID)
	return body, err
}

var _ adminmedia.DriveAudioReader = AdminMediaReader{}

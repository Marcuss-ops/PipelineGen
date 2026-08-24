package capabilities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

type catalogSyncDriveReaderStub struct{}

func (catalogSyncDriveReaderStub) GetFileMeta(context.Context, string) (*drive.FileMeta, error) {
	return &drive.FileMeta{
		ID:          "root-id",
		Name:        "Root",
		MimeType:    "application/vnd.google-apps.folder",
		WebViewLink: "https://drive.example/root",
	}, nil
}

func (catalogSyncDriveReaderStub) ListFiles(context.Context, string) ([]drive.DriveFileInfo, error) {
	return []drive.DriveFileInfo{{
		ID:             "file-id",
		Name:           "clip.mp4",
		MimeType:       "video/mp4",
		Size:           42,
		MD5Checksum:    "md5-value",
		WebViewLink:    "https://drive.example/file",
		WebContentLink: "https://drive.example/download",
		Parents:        []string{"root-id"},
	}}, nil
}

func TestCatalogSyncSourceReaderMapsDriveDTOs(t *testing.T) {
	reader := catalogSyncSourceReader{reader: catalogSyncDriveReaderStub{}}

	meta, err := reader.GetFileMeta(context.Background(), "root-id")
	require.NoError(t, err)
	require.Equal(t, "root-id", meta.ID)
	require.Equal(t, "Root", meta.Name)
	require.Equal(t, "application/vnd.google-apps.folder", meta.MimeType)
	require.Equal(t, "https://drive.example/root", meta.WebViewLink)

	files, err := reader.ListFiles(context.Background(), "root-id")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, RemoteFileExpectation{
		ID:             "file-id",
		Name:           "clip.mp4",
		MimeType:       "video/mp4",
		Size:           42,
		MD5Checksum:    "md5-value",
		WebViewLink:    "https://drive.example/file",
		WebContentLink: "https://drive.example/download",
		Parents:        []string{"root-id"},
	}, RemoteFileExpectation{
		ID:             files[0].ID,
		Name:           files[0].Name,
		MimeType:       files[0].MimeType,
		Size:           files[0].Size,
		MD5Checksum:    files[0].MD5Checksum,
		WebViewLink:    files[0].WebViewLink,
		WebContentLink: files[0].WebContentLink,
		Parents:        files[0].Parents,
	})
}

type RemoteFileExpectation struct {
	ID             string
	Name           string
	MimeType       string
	Size           int64
	MD5Checksum    string
	WebViewLink    string
	WebContentLink string
	Parents        []string
}

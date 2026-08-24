package app

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// catalogSyncSourceReader adapts the broad infrastructure Drive reader to the
// narrow application-owned SourceReader contract.
type catalogSyncDriveReader interface {
	GetFileMeta(ctx context.Context, fileID string) (*drive.FileMeta, error)
	ListFiles(ctx context.Context, parentID string) ([]drive.DriveFileInfo, error)
}

type catalogSyncSourceReader struct {
	reader catalogSyncDriveReader
}

func (a catalogSyncSourceReader) GetFileMeta(ctx context.Context, fileID string) (*catalogsync.RemoteFileMeta, error) {
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil || meta == nil {
		return nil, err
	}
	return &catalogsync.RemoteFileMeta{
		ID:          meta.ID,
		Name:        meta.Name,
		MimeType:    meta.MimeType,
		WebViewLink: meta.WebViewLink,
	}, nil
}

func (a catalogSyncSourceReader) ListFiles(ctx context.Context, parentID string) ([]catalogsync.RemoteFile, error) {
	files, err := a.reader.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}
	result := make([]catalogsync.RemoteFile, 0, len(files))
	for _, file := range files {
		result = append(result, catalogsync.RemoteFile{
			ID:             file.ID,
			Name:           file.Name,
			MimeType:       file.MimeType,
			Size:           file.Size,
			MD5Checksum:    file.MD5Checksum,
			WebViewLink:    file.WebViewLink,
			WebContentLink: file.WebContentLink,
			Parents:        file.Parents,
		})
	}
	return result, nil
}

var (
	_ catalogsync.SourceReader = catalogSyncSourceReader{}
	_ catalogSyncDriveReader   = (*drive.Uploader)(nil)
)

// The concrete SQLite repository implements all three catalog storage ports.
var (
	_ catalogsync.CatalogRepository    = (*assets.ClipsRepository)(nil)
	_ catalogsync.AssetIndexer         = (*assets.ClipsRepository)(nil)
	_ catalogsync.ProjectionDispatcher = (*outbox.Dispatcher)(nil)
)

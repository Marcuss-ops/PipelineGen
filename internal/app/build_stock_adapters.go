// build_stock_adapters.go — import-boundary adapter shims for the
// stock pipeline bundle. Each adapter converts a concrete
// infrastructure type into the application-layer port the
// stockpipeline.Service consumes, keeping internal/app free of
// direct internal/infrastructure imports (godlike/06 import-boundary
// discipline).
package app

import (
	"context"
	"io"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	assetindex "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
)

// chooseDriveReader returns the canonical DriveReaderPort to wire
// into the stock pipeline. It prefers the explicit DriveReader field;
// if nil, it falls back to DriveDownloader for backward compatibility.
// Both are adapted through stockDriveReaderAdapter which converts the
// concrete drive.DriveFileInfo to the application-layer
// stockpipeline.DriveFileInfo (godlike/06 import-boundary discipline).
func chooseDriveReader(acq StockAcquisitionDeps) stockpipeline.DriveReaderPort {
	raw := acq.DriveReader
	if raw == nil {
		raw = acq.DriveDownloader
	}
	if raw == nil {
		return nil
	}
	return &stockDriveReaderAdapter{inner: raw}
}

// stockDriveReaderAdapter wraps a stockConcreteDriveReader and adapts
// its ListFiles return type from []drive.DriveFileInfo to
// []stockpipeline.DriveFileInfo, keeping the application layer free
// of internal/infrastructure/drive imports.
type stockDriveReaderAdapter struct {
	inner stockConcreteDriveReader
}

var _ stockpipeline.DriveReaderPort = (*stockDriveReaderAdapter)(nil)

func (a *stockDriveReaderAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	return a.inner.DownloadFile(ctx, fileID)
}

func (a *stockDriveReaderAdapter) ListFiles(ctx context.Context, parentID string) ([]stockpipeline.DriveFileInfo, error) {
	raw, err := a.inner.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]stockpipeline.DriveFileInfo, len(raw))
	for i, f := range raw {
		out[i] = stockpipeline.DriveFileInfo{
			ID:       f.ID,
			MimeType: f.MimeType,
		}
	}
	return out, nil
}

// stockAssetIndexAdapter wraps *assetindex.Service and adapts its Upsert
// method from *assetindex.AssetRecord to *stockpipeline.StockAssetUpsertRecord,
// keeping the application layer free of internal/infrastructure/database/assetindex
// imports (godlike/06 import-boundary discipline).
type stockAssetIndexAdapter struct {
	inner *assetindex.Service
}

func (a *stockAssetIndexAdapter) Upsert(ctx context.Context, rec *stockpipeline.StockAssetUpsertRecord) error {
	return a.inner.Upsert(ctx, &assetindex.AssetRecord{AssetID: rec.AssetID})
}

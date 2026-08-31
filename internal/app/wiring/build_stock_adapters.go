// build_stock_adapters.go — import-boundary adapter shims for the
// stock pipeline bundle. Each adapter converts a concrete
// infrastructure type into the application-layer port the
// stockpipeline.Service consumes, keeping internal/app free of
// direct internal/infrastructure imports (godlike/06 import-boundary
// discipline).
package wiring

import (
	"context"
	"fmt"
	"io"
	"reflect"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	assetindex "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
)

// chooseDriveReader adapts the canonical DriveReader field to the
// application-layer stockpipeline.DriveReaderPort.
func chooseDriveReader(acq StockAcquisitionDeps) stockpipeline.DriveReaderPort {
	if stockDependencyNil(acq.DriveReader) {
		return nil
	}
	return &stockDriveReaderAdapter{inner: acq.DriveReader}
}

// stockDriveReaderAdapter wraps a stockConcreteDriveReader and adapts
// its ListFiles return type from []drive.DriveFileInfo to
// []stockpipeline.DriveFileInfo, keeping the application layer free
// of internal/platform/drive imports.
type stockDriveReaderAdapter struct {
	inner stockConcreteDriveReader
}

var _ stockpipeline.DriveReaderPort = (*stockDriveReaderAdapter)(nil)

func (a *stockDriveReaderAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if a == nil || stockDependencyNil(a.inner) {
		return nil, "", fmt.Errorf("stock drive reader: adapter is not wired")
	}
	return a.inner.DownloadFile(ctx, fileID)
}

func (a *stockDriveReaderAdapter) ListFiles(ctx context.Context, parentID string) ([]stockpipeline.DriveFileInfo, error) {
	if a == nil || stockDependencyNil(a.inner) {
		return nil, fmt.Errorf("stock drive reader: adapter is not wired")
	}
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
// keeping the application layer free of internal/platform/sqlite/assetindex
// imports (godlike/06 import-boundary discipline).
type stockAssetIndexAdapter struct {
	inner *assetindex.Service
}

func (a *stockAssetIndexAdapter) Upsert(ctx context.Context, rec *stockpipeline.StockAssetUpsertRecord) error {
	if a == nil || a.inner == nil {
		return fmt.Errorf("stock asset index: adapter is not wired")
	}
	if rec == nil {
		return fmt.Errorf("stock asset index: record is nil")
	}
	return a.inner.Upsert(ctx, &assetindex.AssetRecord{AssetID: rec.AssetID})
}

// stockAsyncProjectionAdapter represents the durable asynchronous
// projection handoff. The publish writer has already committed each
// media_assets row together with its asset.index.requested outbox event;
// the outbox worker owns the only Qdrant call. This boundary therefore
// validates that the manifest has a real video artifact and acknowledges
// the durable handoff without invoking Qdrant a second time.
type stockAsyncProjectionAdapter struct{}

var _ stockpipeline.ProjectionPort = (*stockAsyncProjectionAdapter)(nil)

func (*stockAsyncProjectionAdapter) Project(_ context.Context, manifest *job.ArtifactManifest) error {
	if manifest == nil {
		return fmt.Errorf("stock projection: manifest is nil")
	}
	videoCount := 0
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind != string(finalization.KindVideo) {
			continue
		}
		videoCount++
		// Artifact.ID is only the logical manifest identity. A durable
		// Drive handoff is proven by the provider identity populated by
		// the canonical publisher and finalization gates.
		if artifact.RemoteFileID == "" {
			return fmt.Errorf("stock projection: video artifact %q has no published remote file ID", artifact.ID)
		}
	}
	if videoCount == 0 {
		return fmt.Errorf("stock projection: manifest has no video artifacts")
	}
	return nil
}

func newStockProjection() stockpipeline.ProjectionPort {
	return &stockAsyncProjectionAdapter{}
}

// stockDependencyNil detects both a nil interface and an interface carrying
// a typed nil pointer before the composition root wraps it in another adapter.
// This is intentionally local to the app composition boundary: production
// constructors must never make an unavailable capability look available.
func stockDependencyNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

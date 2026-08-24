package assets

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type portConformanceRepository struct{}

func (portConformanceRepository) GetClip(context.Context, string) (*asset.Asset, error) {
	return nil, nil
}
func (portConformanceRepository) ListFolders(context.Context, string) ([]*asset.ClipFolder, error) {
	return nil, nil
}
func (portConformanceRepository) DeleteFolder(context.Context, string) error { return nil }
func (portConformanceRepository) GetIndexState(context.Context, string) (asset.IndexState, error) {
	return asset.StateDiscovered, nil
}
func (portConformanceRepository) UpsertClipNoIndex(context.Context, *asset.Asset) error { return nil }
func (portConformanceRepository) EnqueueAndIndex(context.Context, *asset.Asset, string) error {
	return nil
}

func (portConformanceRepository) GetFileMeta(context.Context, string) (*RemoteFileMeta, error) {
	return nil, nil
}
func (portConformanceRepository) ListFiles(context.Context, string) ([]RemoteFile, error) {
	return nil, nil
}

var (
	_ CatalogRepository    = portConformanceRepository{}
	_ AssetIndexer         = portConformanceRepository{}
	_ ProjectionDispatcher = portConformanceRepository{}
	_ SourceReader         = portConformanceRepository{}
)

func TestApplicationPortsHaveIndependentContracts(t *testing.T) {
	var _ CatalogRepository = portConformanceRepository{}
	var _ AssetIndexer = portConformanceRepository{}
	var _ ProjectionDispatcher = portConformanceRepository{}
	var _ SourceReader = portConformanceRepository{}
}

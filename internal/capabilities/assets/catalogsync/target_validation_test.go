package catalogsync

import (
	"context"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func TestSyncAllRejectsConfiguredTargetWithoutPorts(t *testing.T) {
	svc, err := NewService(Deps{
		Reader:     testSourceReader{},
		Targets:    []Target{{Source: "youtube", RootFolderID: "folder-1"}},
		Dispatcher: testDispatcher{},
		Log:        zap.NewNop(),
	})
	require.ErrorIs(t, err, ErrCatalogSyncInvalidTarget)
	require.Nil(t, svc)
}

func TestSyncSourceRejectsEmptyRootFolder(t *testing.T) {
	svc, err := NewService(Deps{
		Reader:     testSourceReader{},
		Targets:    nil,
		Dispatcher: testDispatcher{},
		Log:        zap.NewNop(),
	})
	require.NoError(t, err)
	svc.targets = []Target{{Source: "youtube", Repo: testRepository{}, Indexer: testIndexer{}}}

	_, err = svc.SyncSource(context.Background(), "youtube")
	require.ErrorIs(t, err, ErrCatalogSyncInvalidTarget)
}

type testDispatcher struct{}

func (testDispatcher) UpsertClipNoIndex(context.Context, *asset.Asset) error { return nil }
func (testDispatcher) EnqueueAndIndex(context.Context, *asset.Asset, string) error {
	return nil
}

type testRepository struct{}

func (testRepository) GetClip(context.Context, string) (*asset.Asset, error) { return nil, nil }
func (testRepository) ListFolders(context.Context, string) ([]*detail.ClipFolder, error) {
	return nil, nil
}
func (testRepository) DeleteFolder(context.Context, string) error { return nil }

type testIndexer struct{}

func (testIndexer) GetIndexState(context.Context, string) (asset.IndexState, error) {
	return asset.StateDiscovered, nil
}

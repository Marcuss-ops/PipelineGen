package artifacts

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type processingTestDispatcher struct{}

func (processingTestDispatcher) EnqueueAndIndex(context.Context, *asset.Asset, string) error {
	return nil
}
func (processingTestDispatcher) EnqueueAndRestore(context.Context, string) error { return nil }
func (processingTestDispatcher) EnqueueAndDelete(context.Context, string) error  { return nil }

var _ mutations.AssetMutationDispatcher = processingTestDispatcher{}

type registryProcessingErrorRepo struct {
	completeErr error
}

func (r *registryProcessingErrorRepo) Start(context.Context, string, string) error { return nil }
func (r *registryProcessingErrorRepo) Complete(context.Context, string, string) error {
	return r.completeErr
}
func (r *registryProcessingErrorRepo) Fail(context.Context, string, string, string) error {
	return nil
}
func (r *registryProcessingErrorRepo) Transition(context.Context, string, string, asset.ProcessingStatus, asset.ProcessingStatus) error {
	return nil
}
func (r *registryProcessingErrorRepo) Get(context.Context, string, string) (*asset.ProcessingRecord, error) {
	return nil, nil
}
func (r *registryProcessingErrorRepo) GetByAssetID(context.Context, string) ([]asset.ProcessingRecord, error) {
	return nil, nil
}
func (r *registryProcessingErrorRepo) GetFailed(context.Context) ([]asset.ProcessingRecord, error) {
	return nil, nil
}
func (r *registryProcessingErrorRepo) Delete(context.Context, string, string) error { return nil }
func (r *registryProcessingErrorRepo) DeleteAll(context.Context, string) error      { return nil }

var _ asset.ProcessingRepository = (*registryProcessingErrorRepo)(nil)

func TestClipsRegistryUpsertMediaPropagatesProcessingError(t *testing.T) {
	cause := errors.New("registry complete failed")
	processing := &registryProcessingErrorRepo{completeErr: cause}
	registry := NewClipsRegistry(nil, nil, nil, nil, processing, processingTestDispatcher{})

	err := registry.UpsertMedia(context.Background(), &MediaRecord{ID: "clip-registry", Status: "ACTIVE"})

	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want processing cause", err)
	}
}

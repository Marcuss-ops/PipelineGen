package artifacts

import (
	"context"
	"errors"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type processingTestCommitter struct{}

func (processingTestCommitter) CommitTx(context.Context, persistence.Transaction, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}
func (processingTestCommitter) CommitAndIndex(context.Context, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}
func (processingTestCommitter) CommitAsset(context.Context, persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return persistence.CommittedAsset{}, nil
}

var _ persistence.AssetCommitter = processingTestCommitter{}

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

var _ detail.ProcessingRepository = (*registryProcessingErrorRepo)(nil)

// P1-5 (Sept 2026): asset_processing is operational best-effort SQLite and
// MUST NOT make a durable PG media commit fail. The registry logs the
// processing error but returns nil so the caller sees the durable media row.
func TestClipsRegistryUpsertMediaPropagatesProcessingError(t *testing.T) {
	cause := errors.New("registry complete failed")
	processing := &registryProcessingErrorRepo{completeErr: cause}
	registry := NewClipsRegistry(nil, nil, nil, processing, processingTestCommitter{})

	err := registry.UpsertMedia(context.Background(), &MediaRecord{ID: "clip-registry", Status: "ACTIVE"})

	if err != nil {
		t.Fatalf("error = %v, want nil (processing is best-effort, media commit already durable)", err)
	}
}

func TestClipsRegistryUpsertMediaProcessingBestEffortDoesNotBlockPGCommit(t *testing.T) {
	cause := errors.New("processing write failed")
	processing := &registryProcessingErrorRepo{completeErr: cause}
	registry := NewClipsRegistry(nil, nil, nil, processing, processingTestCommitter{})

	err := registry.UpsertMedia(context.Background(), &MediaRecord{ID: "clip-best-effort", Status: "ACTIVE", MediaType: "audio"})
	if err != nil {
		t.Fatalf("best-effort processing must not fail UpsertMedia: %v", err)
	}
}

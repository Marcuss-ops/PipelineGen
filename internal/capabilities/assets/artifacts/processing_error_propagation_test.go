package artifacts

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
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

func (r *registryProcessingErrorRepo) StartAssetProcessing(context.Context, string, string) error {
	return nil
}
func (r *registryProcessingErrorRepo) CompleteAssetProcessing(context.Context, string, string) error {
	return r.completeErr
}
func (r *registryProcessingErrorRepo) FailAssetProcessing(context.Context, string, string, string) error {
	return nil
}

var _ persistence.AssetProcessingWriter = (*registryProcessingErrorRepo)(nil)

// P1-5 (Sept 2026): the registry's asset_processing write is best-effort and
// MUST NOT make a durable PG media commit fail. MEDIA-SSOT write-bridge
// (Sept 2026): the write now targets the media SSOT through
// persistence.AssetProcessingWriter, so best-effort no longer means
// second-engine — the registry logs the processing error but returns nil so
// the caller sees the durable media row.
func TestClipsRegistryUpsertMediaPropagatesProcessingError(t *testing.T) {
	cause := errors.New("registry complete failed")
	processing := &registryProcessingErrorRepo{completeErr: cause}
	registry := NewClipsRegistry(nil, nil, processing, processingTestCommitter{})

	err := registry.UpsertMedia(context.Background(), &MediaRecord{ID: "clip-registry", Status: "ACTIVE"})

	if err != nil {
		t.Fatalf("error = %v, want nil (processing is best-effort, media commit already durable)", err)
	}
}

func TestClipsRegistryUpsertMediaProcessingBestEffortDoesNotBlockPGCommit(t *testing.T) {
	cause := errors.New("processing write failed")
	processing := &registryProcessingErrorRepo{completeErr: cause}
	registry := NewClipsRegistry(nil, nil, processing, processingTestCommitter{})

	err := registry.UpsertMedia(context.Background(), &MediaRecord{ID: "clip-best-effort", Status: "ACTIVE", MediaType: "audio"})
	if err != nil {
		t.Fatalf("best-effort processing must not fail UpsertMedia: %v", err)
	}
}

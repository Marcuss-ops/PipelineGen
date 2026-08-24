package assets

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type processingErrorRepo struct {
	startErr    error
	completeErr error
	failErr     error
	starts      int
	completes   int
	fails       int
}

func (r *processingErrorRepo) Start(context.Context, string, string) error {
	r.starts++
	return r.startErr
}
func (r *processingErrorRepo) Complete(context.Context, string, string) error {
	r.completes++
	return r.completeErr
}
func (r *processingErrorRepo) Fail(context.Context, string, string, string) error {
	r.fails++
	return r.failErr
}
func (r *processingErrorRepo) Transition(context.Context, string, string, asset.ProcessingStatus, asset.ProcessingStatus) error {
	return nil
}
func (r *processingErrorRepo) Get(context.Context, string, string) (*asset.ProcessingRecord, error) {
	return nil, nil
}
func (r *processingErrorRepo) GetByAssetID(context.Context, string) ([]asset.ProcessingRecord, error) {
	return nil, nil
}
func (r *processingErrorRepo) GetFailed(context.Context) ([]asset.ProcessingRecord, error) {
	return nil, nil
}
func (r *processingErrorRepo) Delete(context.Context, string, string) error { return nil }
func (r *processingErrorRepo) DeleteAll(context.Context, string) error      { return nil }

var _ asset.ProcessingRepository = (*processingErrorRepo)(nil)

func TestPersistProcessingStatePropagatesStartError(t *testing.T) {
	cause := errors.New("start failed")
	repo := &processingErrorRepo{startErr: cause}
	rec := &artifacts.MediaRecord{ID: "clip-1", Status: "ACTIVE"}

	err := persistProcessingState(context.Background(), repo, rec, string(asset.StageUpload))

	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want start cause", err)
	}
	if repo.completes != 0 {
		t.Fatalf("Complete calls = %d, want 0 after Start failure", repo.completes)
	}
}

func TestPersistProcessingStatePropagatesCompleteError(t *testing.T) {
	cause := errors.New("complete failed")
	repo := &processingErrorRepo{completeErr: cause}
	rec := &artifacts.MediaRecord{ID: "clip-2", Status: "completed"}

	err := persistProcessingState(context.Background(), repo, rec, string(asset.StageUpload))

	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want complete cause", err)
	}
	if repo.starts != 1 || repo.completes != 1 {
		t.Fatalf("Start/Complete calls = %d/%d, want 1/1", repo.starts, repo.completes)
	}
}

func TestPersistProcessingStatePropagatesFailError(t *testing.T) {
	cause := errors.New("fail transition failed")
	repo := &processingErrorRepo{failErr: cause}
	rec := &artifacts.MediaRecord{ID: "clip-3", Status: "failed", Error: "source unavailable"}

	err := persistProcessingState(context.Background(), repo, rec, string(asset.StageUpload))

	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want fail cause", err)
	}
	if repo.starts != 1 || repo.fails != 1 {
		t.Fatalf("Start/Fail calls = %d/%d, want 1/1", repo.starts, repo.fails)
	}
}

type processingTestDispatcher struct{}

func (processingTestDispatcher) EnqueueAndIndex(context.Context, *asset.Asset, string) error {
	return nil
}
func (processingTestDispatcher) EnqueueAndRestore(context.Context, string) error { return nil }
func (processingTestDispatcher) EnqueueAndDelete(context.Context, string) error  { return nil }

var _ mutations.AssetMutationDispatcher = processingTestDispatcher{}

func TestClipStoreAdapterUpsertPropagatesProcessingError(t *testing.T) {
	cause := errors.New("adapter complete failed")
	processing := &processingErrorRepo{completeErr: cause}
	adapter := NewClipStoreAdapter(nil, nil, nil, nil, processing, processingTestDispatcher{}).(*clipStoreAdapter)

	err := adapter.Upsert(context.Background(), &artifacts.MediaRecord{ID: "clip-adapter", Status: "completed"})

	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want processing cause", err)
	}
}

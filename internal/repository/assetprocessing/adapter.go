// Package assetprocessing implements the canonical assets.ProcessingRepository
// interface backed by SQLite. The Adapter wraps the concrete *Repository and
// delegates directly — since the Repository already imports domain types,
// no conversion is needed.
package assetprocessing

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// Adapter implements assets.ProcessingRepository by delegating to the
// concrete SQLite *Repository. No type conversion is required because
// the Repository already uses domain types.
type Adapter struct {
	inner *Repository
}

// NewAdapter wraps a concrete *Repository as an assets.ProcessingRepository.
func NewAdapter(inner *Repository) *Adapter {
	return &Adapter{inner: inner}
}

func (a *Adapter) Start(ctx context.Context, assetID, step string) error {
	return a.inner.Start(ctx, assetID, step)
}

func (a *Adapter) Complete(ctx context.Context, assetID, step string) error {
	return a.inner.Complete(ctx, assetID, step)
}

func (a *Adapter) Fail(ctx context.Context, assetID, step, errMsg string) error {
	return a.inner.Fail(ctx, assetID, step, errMsg)
}

func (a *Adapter) Transition(ctx context.Context, assetID, step string, from, to assets.ProcessingStatus) error {
	return a.inner.Transition(ctx, assetID, step, from, to)
}

func (a *Adapter) Get(ctx context.Context, assetID, step string) (*assets.ProcessingRecord, error) {
	return a.inner.Get(ctx, assetID, step)
}

func (a *Adapter) GetByAssetID(ctx context.Context, assetID string) ([]assets.ProcessingRecord, error) {
	return a.inner.GetByAssetID(ctx, assetID)
}

func (a *Adapter) GetFailed(ctx context.Context) ([]assets.ProcessingRecord, error) {
	return a.inner.GetFailed(ctx)
}

func (a *Adapter) Delete(ctx context.Context, assetID, step string) error {
	return a.inner.Delete(ctx, assetID, step)
}

func (a *Adapter) DeleteAll(ctx context.Context, assetID string) error {
	return a.inner.DeleteAll(ctx, assetID)
}

// Compile-time check.
var _ assets.ProcessingRepository = (*Adapter)(nil)

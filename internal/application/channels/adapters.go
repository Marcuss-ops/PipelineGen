// Package channels — adapters.go: narrow infrastructure adapter
// satisfying the Repository port declared in contract.go.
//
// PG-002 (June 2026) renewed for the Capability Standard migration:
// RepositoryAdapter wraps the SQLite-backed *assets.ChannelsRepository
// so the application package can consume it through the narrow
// channels.Repository interface without importing internal/infrastructure/*
// directly. The adapter is intentionally thin — every method is a
// one-line delegate — because the channel surface today is pure CRUD;
// if/when application orchestration lands in service.go, the adapter
// stays the boundary and Service sits above it.
package channels

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// Compile-time assertion: *RepositoryAdapter satisfies
// channels.Repository. Drift in either side fails the build.
var _ Repository = (*RepositoryAdapter)(nil)

// RepositoryAdapter wraps the SQLite repository as the canonical
// port. The infrastructure type is unexported on the consumer side —
// only the Adapter is reachable from the composition root.
type RepositoryAdapter struct {
	repo *assets.ChannelsRepository
}

// NewRepositoryAdapter is the canonical constructor. The concrete
// ChannelsRepository comes from the assets package which is the
// single owner of the SQLite schema; this package does not re-export it.
func NewRepositoryAdapter(repo *assets.ChannelsRepository) *RepositoryAdapter {
	return &RepositoryAdapter{repo: repo}
}

func (a *RepositoryAdapter) ListAll(ctx context.Context) ([]*asset.CategoryChannel, error) {
	return a.repo.ListAll(ctx)
}

func (a *RepositoryAdapter) ListEnabled(ctx context.Context) ([]*asset.CategoryChannel, error) {
	return a.repo.ListEnabled(ctx)
}

func (a *RepositoryAdapter) ListCategories(ctx context.Context) ([]string, error) {
	return a.repo.ListCategories(ctx)
}

func (a *RepositoryAdapter) GetByID(ctx context.Context, id string) (*asset.CategoryChannel, error) {
	return a.repo.GetByID(ctx, id)
}

func (a *RepositoryAdapter) Upsert(ctx context.Context, ch *asset.CategoryChannel) error {
	return a.repo.Upsert(ctx, ch)
}

func (a *RepositoryAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

func (a *RepositoryAdapter) MarkChecked(ctx context.Context, cmd MarkCheckedCommand) error {
	return a.repo.MarkChecked(ctx, cmd.ID, cmd.NextCheckAt, cmd.LastError, cmd.Success)
}

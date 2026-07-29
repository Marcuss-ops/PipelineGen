// Package channels — adapters.go: narrow infrastructure adapter
// satisfying the Repository port declared in contract.go.
//
// PG-002 (June 2026) renewed for the Capability Standard migration:
// RepositoryAdapter wraps the SQLite-backed *channels.ChannelsRepository
// so the application package can consume it through the narrow
// channels.Repository interface without importing internal/infrastructure/*
// directly. The adapter is intentionally thin — every method is a
// one-line delegate — because the channel surface today is pure CRUD;
// if/when application orchestration lands in service.go, the adapter
// stays the boundary and Service sits above it.
package channels

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Compile-time assertion: *RepositoryAdapter satisfies
// channels.Repository. Drift in either side fails the build.
var _ Repository = (*RepositoryAdapter)(nil)

// RepositoryAdapter wraps the SQLite repository as the canonical
// port. The infrastructure type is unexported on the consumer side —
// only the Adapter is reachable from the composition root.
type RepositoryAdapter struct {
	repo *channels.ChannelsRepository
}

// NewRepositoryAdapter is the canonical constructor. The concrete
// ChannelsRepository comes from the assets package which is the
// single owner of the SQLite schema; this package does not re-export it.
func NewRepositoryAdapter(repo *channels.ChannelsRepository) *RepositoryAdapter {
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

// MarkChecked delegates to the SQLite repository. Commit A
// (June 2026, P1 #8): forwards cmd.LeaseToken so the leaf UPDATE can
// fence on lease_owner and surface ErrLeaseLost via errors.Is when
// RowsAffected==0. Empty LeaseToken means no fence (back-compat).
func (a *RepositoryAdapter) MarkChecked(ctx context.Context, cmd MarkCheckedCommand) error {
	return a.repo.MarkChecked(ctx, cmd.ID, cmd.LeaseToken, cmd.NextCheckAt, cmd.LastError, cmd.Success)
}

func (a *RepositoryAdapter) ClaimDue(ctx context.Context, cmd ClaimDueCommand) ([]*asset.CategoryChannel, error) {
	return a.repo.ClaimDue(ctx, cmd.Now, cmd.WorkerID, cmd.LeaseUntil, cmd.Limit)
}

func (a *RepositoryAdapter) UpdateCursor(ctx context.Context, cmd UpdateCursorCommand) error {
	return a.repo.UpdateCursor(ctx, cmd.ID, cmd.Cursor)
}

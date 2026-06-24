// Package app — adapters satisfying the typed ports declared in
// internal/application/<feature>/ports.go.
//
// PG-002 (June 2026): channelRepositoryAdapter wraps the SQLite-backed
// *assets.ChannelsRepository so the API layer can reach it through the
// channels.Repository interface (declared in
// internal/application/channels/ports.go) without importing
// internal/infrastructure/* directly. The adapter is intentionally
// thin — every method is a one-line delegate — because the channel
// surface today is pure CRUD; if/when application logic lands in this
// package, the adapter stays the boundary and orchestration lives in a
// ChannelService above it.
package app

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// Compile-time assertion: *channelRepositoryAdapter satisfies
// channels.Repository. Drift in either side fails the build.
var _ channels.Repository = (*channelRepositoryAdapter)(nil)

// channelRepositoryAdapter wraps the SQLite repository as the canonical
// port. The infrastructure type is unexported on the consumer side —
// only the Adapter is reachable from the composition root.
type channelRepositoryAdapter struct {
	repo *assets.ChannelsRepository
}

// newChannelRepositoryAdapter is a tiny factory for the composition
// root. The concrete ChannelsRepository comes from the assets package
// which is the single owner of the SQLite schema; this package does
// not re-export it.
func newChannelRepositoryAdapter(repo *assets.ChannelsRepository) *channelRepositoryAdapter {
	return &channelRepositoryAdapter{repo: repo}
}

func (a *channelRepositoryAdapter) ListAll(ctx context.Context) ([]*asset.CategoryChannel, error) {
	return a.repo.ListAll(ctx)
}

func (a *channelRepositoryAdapter) ListCategories(ctx context.Context) ([]string, error) {
	return a.repo.ListCategories(ctx)
}

func (a *channelRepositoryAdapter) GetByID(ctx context.Context, id string) (*asset.CategoryChannel, error) {
	return a.repo.GetByID(ctx, id)
}

func (a *channelRepositoryAdapter) Upsert(ctx context.Context, ch *asset.CategoryChannel) error {
	return a.repo.Upsert(ctx, ch)
}

func (a *channelRepositoryAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

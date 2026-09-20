// Package app — YouTube clip + monitor store adapters
// split from youtube_adapters.go (PR-GODOBJ-Azione-4, July 2026).
//
// 2 adapters: ClipStoreAdapter, MonitorsStoreAdapter.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): the third adapter,
// SourcingClipStoreAdapter (the SQLite dedupe lookup for the YouTube sourcing
// registrar), was DELETED. Dedupe now reads the PostgreSQL media SSOT through
// SourcingClipStorePGAdapter; see youtube_sourcing_pg_adapter.go.
package adapters

import (
	"context"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/monitors"
)

// ── ClipStoreAdapter ──────────────────────────────────────────────────

type ClipStoreAdapter struct {
	inner *assetsrepo.ClipsRepository
}

func NewClipStoreAdapter(r *assetsrepo.ClipsRepository) youtubeports.ClipStorePort {
	if r == nil {
		return nil
	}
	return &ClipStoreAdapter{inner: r}
}

var _ youtubeports.ClipStorePort = (*ClipStoreAdapter)(nil)

func (a *ClipStoreAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.Get(ctx, id)
}
func (a *ClipStoreAdapter) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.GetClip(ctx, id)
}
func (a *ClipStoreAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.inner.Upsert(ctx, clip)
}
func (a *ClipStoreAdapter) UpsertFolder(ctx context.Context, f *detail.ClipFolder) error {
	return a.inner.UpsertFolder(ctx, f)
}
func (a *ClipStoreAdapter) DeleteClip(ctx context.Context, id string) error {
	return a.inner.DeleteClip(ctx, id)
}
func (a *ClipStoreAdapter) GetFolder(ctx context.Context, folderID string) (*detail.ClipFolder, error) {
	return a.inner.GetFolder(ctx, folderID)
}
func (a *ClipStoreAdapter) SearchClipsAdvanced(ctx context.Context, req detail.AdvancedSearchRequest) (*detail.AdvancedSearchResult, error) {
	return a.inner.SearchClipsAdvanced(ctx, req)
}
func (a *ClipStoreAdapter) CountClips(ctx context.Context) (int, error) {
	return a.inner.CountClips(ctx)
}

// ── MonitorsStoreAdapter ──────────────────────────────────────────────

type MonitorsStoreAdapter struct {
	inner *monitors.MonitorsRepository
}

func NewMonitorsStoreAdapter(r *monitors.MonitorsRepository) youtubeports.MonitorsStorePort {
	if r == nil {
		return nil
	}
	return &MonitorsStoreAdapter{inner: r}
}

func (a *MonitorsStoreAdapter) UpsertSource(ctx context.Context, ms *asset.MonitoredSource) error {
	return a.inner.UpsertSource(ctx, ms)
}
func (a *MonitorsStoreAdapter) IncrementProcessed(ctx context.Context, id string) error {
	return a.inner.IncrementProcessed(ctx, id)
}

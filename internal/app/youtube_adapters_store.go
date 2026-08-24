// Package app — YouTube clip + monitor store adapters
// split from youtube_adapters.go (PR-GODOBJ-Azione-4, July 2026).
//
// 3 adapters: clipStoreAdapter, monitorsStoreAdapter, sourcingClipStoreAdapter.
package app

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/monitors"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── clipStoreAdapter ──────────────────────────────────────────────────

type clipStoreAdapter struct {
	inner *assetsrepo.ClipsRepository
}

func newClipStoreAdapter(r *assetsrepo.ClipsRepository) youtubeports.ClipStorePort {
	if r == nil {
		return nil
	}
	return &clipStoreAdapter{inner: r}
}

var _ youtubeports.ClipStorePort = (*clipStoreAdapter)(nil)

func (a *clipStoreAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.Get(ctx, id)
}
func (a *clipStoreAdapter) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.GetClip(ctx, id)
}
func (a *clipStoreAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.inner.Upsert(ctx, clip)
}
func (a *clipStoreAdapter) UpsertFolder(ctx context.Context, f *asset.ClipFolder) error {
	return a.inner.UpsertFolder(ctx, f)
}
func (a *clipStoreAdapter) DeleteClip(ctx context.Context, id string) error {
	return a.inner.DeleteClip(ctx, id)
}
func (a *clipStoreAdapter) GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error) {
	return a.inner.GetFolder(ctx, folderID)
}
func (a *clipStoreAdapter) SearchClipsAdvanced(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error) {
	return a.inner.SearchClipsAdvanced(ctx, req)
}
func (a *clipStoreAdapter) CountClips(ctx context.Context) (int, error) {
	return a.inner.CountClips(ctx)
}
func (a *clipStoreAdapter) ListYouTubeClipIDsForSearchText(ctx context.Context, limit, offset int) ([]string, error) {
	query := `SELECT id FROM media_assets WHERE source = 'youtube' AND json_extract(metadata_json, '$.youtube_title') != '' ORDER BY id`
	args := []any{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	if offset > 0 {
		if limit <= 0 {
			query += " LIMIT -1"
		}
		query += " OFFSET ?"
		args = append(args, offset)
	}
	rows, err := a.inner.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ── monitorsStoreAdapter ──────────────────────────────────────────────

type monitorsStoreAdapter struct {
	inner *monitors.MonitorsRepository
}

func newMonitorsStoreAdapter(r *monitors.MonitorsRepository) youtubeports.MonitorsStorePort {
	if r == nil {
		return nil
	}
	return &monitorsStoreAdapter{inner: r}
}

func (a *monitorsStoreAdapter) UpsertSource(ctx context.Context, ms *asset.MonitoredSource) error {
	return a.inner.UpsertSource(ctx, ms)
}
func (a *monitorsStoreAdapter) IncrementProcessed(ctx context.Context, id string) error {
	return a.inner.IncrementProcessed(ctx, id)
}

// ── sourcingClipStoreAdapter ──────────────────────────────────────────
// Merged from youtube_drive_legacy_adapter.go (PR-GODOBJ-Azione-4, July 2026).

type sourcingClipStoreAdapter struct {
	repo *assetsrepo.ClipsRepository
}

func (a *sourcingClipStoreAdapter) FindByName(ctx context.Context, name string) (string, error) {
	if a.repo == nil {
		return "", nil
	}
	return a.repo.FindByName(ctx, name)
}

func (a *sourcingClipStoreAdapter) FindExisting(ctx context.Context, videoID, url string, startSec, endSec float64) (string, error) {
	if a.repo == nil {
		return "", nil
	}
	hasSegment := endSec > startSec
	if videoID != "" {
		if id, err := a.repo.FindByYouTubeVideoID(ctx, videoID, hasSegment, startSec, endSec); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	if url != "" && !hasSegment {
		if id, err := a.repo.FindBySourceURL(ctx, url); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", nil
}

func (a *sourcingClipStoreAdapter) GetClip(ctx context.Context, id string) (*sourcing.ExistingClip, error) {
	if a.repo == nil {
		return nil, nil
	}
	clip, err := a.repo.GetClip(ctx, id)
	if err != nil || clip == nil {
		return nil, err
	}
	return toExistingClip(clip), nil
}

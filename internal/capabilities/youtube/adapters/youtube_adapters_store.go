// Package app — YouTube clip + monitor store adapters
// split from youtube_adapters.go (PR-GODOBJ-Azione-4, July 2026).
//
// 3 adapters: ClipStoreAdapter, MonitorsStoreAdapter, SourcingClipStoreAdapter.
package adapters

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
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
func (a *ClipStoreAdapter) ListYouTubeClipIDsForSearchText(ctx context.Context, limit, offset int) ([]string, error) {
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

// ── SourcingClipStoreAdapter ──────────────────────────────────────────
// Merged from youtube_drive_legacy_adapter.go (PR-GODOBJ-Azione-4, July 2026).

type SourcingClipStoreAdapter struct {
	repo *assetsrepo.ClipsRepository
}

func (a *SourcingClipStoreAdapter) FindByName(ctx context.Context, name string) (string, error) {
	if a.repo == nil {
		return "", nil
	}
	return a.repo.FindByName(ctx, name)
}

func (a *SourcingClipStoreAdapter) FindExisting(ctx context.Context, videoID, url string, startSec, endSec float64) (string, error) {
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

func (a *SourcingClipStoreAdapter) GetClip(ctx context.Context, id string) (*sourcing.ExistingClip, error) {
	if a.repo == nil {
		return nil, nil
	}
	clip, err := a.repo.GetClip(ctx, id)
	if err != nil || clip == nil {
		return nil, err
	}
	return toExistingClip(clip), nil
}

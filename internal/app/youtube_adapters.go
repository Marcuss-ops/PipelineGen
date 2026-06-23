package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
)

// ── ClipStoreAdapter wraps *assets.ClipsRepository to satisfy youtube.ClipStorePort ──

type clipStoreAdapter struct {
	inner *assets.ClipsRepository
}

func newClipStoreAdapter(r *assets.ClipsRepository) youtube.ClipStorePort {
	if r == nil {
		return nil
	}
	return &clipStoreAdapter{inner: r}
}

func (a *clipStoreAdapter) DB() *sql.DB { return a.inner.DB() }
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

// ── MonitorsStoreAdapter wraps *assets.MonitorsRepository to satisfy youtube.MonitorsStorePort ──

type monitorsStoreAdapter struct {
	inner *assets.MonitorsRepository
}

func newMonitorsStoreAdapter(r *assets.MonitorsRepository) youtube.MonitorsStorePort {
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

// ── CacheStoreAdapter wraps *assets.ClipsRepository.DB() to satisfy youtube.YouTubeCacheStorePort ──

type cacheStoreAdapter struct {
	clips *assets.ClipsRepository
}

func newCacheStoreAdapter(r *assets.ClipsRepository) youtube.YouTubeCacheStorePort {
	if r == nil {
		return nil
	}
	return &cacheStoreAdapter{clips: r}
}

func (a *cacheStoreAdapter) db() *assets.ClipsRepository { return a.clips }

func (a *cacheStoreAdapter) GetSearchCache(ctx context.Context, cacheKey string) (string, error) {
	var resultsJSON, cachedAt string
	err := a.db().DB().QueryRowContext(ctx,
		"SELECT results_json, cached_at FROM youtube_search_cache WHERE cache_key = ?", cacheKey,
	).Scan(&resultsJSON, &cachedAt)
	return resultsJSON, err
}

func (a *cacheStoreAdapter) UpsertSearchCache(ctx context.Context, cacheKey string, resultsJSON string) error {
	_, err := a.db().DB().ExecContext(ctx,
		"INSERT OR REPLACE INTO youtube_search_cache (cache_key, results_json, cached_at) VALUES (?, ?, datetime('now'))",
		cacheKey, resultsJSON,
	)
	return err
}

func (a *cacheStoreAdapter) GetMetadataCache(ctx context.Context, videoID string) (string, error) {
	var metadataJSON string
	err := a.db().DB().QueryRowContext(ctx,
		"SELECT metadata_json FROM youtube_video_metadata_cache WHERE video_id = ?", videoID,
	).Scan(&metadataJSON)
	return metadataJSON, err
}

func (a *cacheStoreAdapter) UpsertMetadataCache(ctx context.Context, videoID string, metadataJSON string) error {
	_, err := a.db().DB().ExecContext(ctx, `
		INSERT INTO youtube_video_metadata_cache (video_id, metadata_json, cached_at, last_used, hit_count)
		VALUES (?, ?, datetime('now'), datetime('now'), 0)
		ON CONFLICT(video_id) DO UPDATE SET
			metadata_json = excluded.metadata_json,
			cached_at = excluded.cached_at,
			last_used = datetime('now')
	`, videoID, metadataJSON)
	return err
}

func (a *cacheStoreAdapter) IncrementMetadataHits(ctx context.Context, videoID string) error {
	_, err := a.db().DB().ExecContext(ctx,
		`UPDATE youtube_video_metadata_cache SET hit_count = hit_count + 1, last_used = datetime('now') WHERE video_id = ?`,
		videoID,
	)
	return err
}

func (a *cacheStoreAdapter) ListHotMetadata(ctx context.Context, limit int) ([]assets.YouTubeCacheEntry, error) {
	rows, err := a.db().DB().QueryContext(ctx,
		`SELECT video_id, metadata_json FROM youtube_video_metadata_cache ORDER BY hit_count DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []assets.YouTubeCacheEntry
	for rows.Next() {
		var e assets.YouTubeCacheEntry
		if err := rows.Scan(&e.VideoID, &e.MetadataJSON); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (a *cacheStoreAdapter) PurgeStaleMetadata(ctx context.Context, staleIDs []string) (int64, error) {
	if len(staleIDs) == 0 {
		return 0, nil
	}
	query := "DELETE FROM youtube_video_metadata_cache WHERE video_id IN (?"
	for i := 1; i < len(staleIDs); i++ {
		query += ",?"
	}
	query += ")"
	args := make([]any, len(staleIDs))
	for i, id := range staleIDs {
		args[i] = id
	}
	result, err := a.db().DB().ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (a *cacheStoreAdapter) ListAllVideoIDs(ctx context.Context) ([]string, error) {
	rows, err := a.db().DB().QueryContext(ctx,
		`SELECT video_id FROM youtube_video_metadata_cache`,
	)
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
	return ids, nil
}

func (a *cacheStoreAdapter) GetSegmentsCache(ctx context.Context, videoID string) (string, error) {
	var segmentsJSON string
	err := a.db().DB().QueryRowContext(ctx,
		"SELECT segments_json FROM youtube_segments_cache WHERE video_id = ?", videoID,
	).Scan(&segmentsJSON)
	return segmentsJSON, err
}

func (a *cacheStoreAdapter) UpsertSegmentsCache(ctx context.Context, videoID string, segmentsJSON string) error {
	_, err := a.db().DB().ExecContext(ctx,
		"INSERT OR REPLACE INTO youtube_segments_cache (video_id, segments_json, cached_at) VALUES (?, ?, datetime('now'))",
		videoID, segmentsJSON,
	)
	return err
}

func (a *cacheStoreAdapter) GetCategoryCache(ctx context.Context, videoTitle string) (string, error) {
	var category string
	err := a.db().DB().QueryRowContext(ctx,
		"SELECT category FROM youtube_category_cache WHERE video_title = ?", videoTitle,
	).Scan(&category)
	return category, err
}

func (a *cacheStoreAdapter) UpsertCategoryCache(ctx context.Context, videoTitle string, category string) error {
	_, err := a.db().DB().ExecContext(ctx,
		"INSERT OR REPLACE INTO youtube_category_cache (video_title, category, cached_at) VALUES (?, ?, datetime('now'))",
		videoTitle, category,
	)
	return err
}

// ── ClipIndexerAdapter wraps *clipindexer.Service to satisfy youtube.ClipIndexerPort ──
//
// youtube.ClipIndexerPort is an empty-marker port — the struct conformance to
// the interface is at the *clipIndexerAdapter assignment site in
// composition.go. Methods below are present because the application code
// may call .IsEnabled() / .IndexClip() on the port.

type clipIndexerAdapter struct {
	inner *clipindexer.Service
}

func (a *clipIndexerAdapter) IsEnabled() bool { return a.inner.IsEnabled() }
func (a *clipIndexerAdapter) IndexClip(ctx context.Context, id string) error {
	return a.inner.IndexClip(ctx, id)
}

// ── OllamaClientAdapter wraps *client.Client (composition injects the real
//    *client.Client directly; this adapter is reserved for tests/mocks that
//    predate the structural port semantics). ──

type ollamaClientAdapter struct {
	inner interface {
		SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error)
	}
}

func (a *ollamaClientAdapter) SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error) {
	return a.inner.SimpleGenerate(ctx, model, prompt, timeout, opts)
}

// ── DriveFolderMgrAdapter wraps *drive.Uploader to satisfy youtube.DriveFolderManagerPort ──
//
// Per PR2 followup (June 2026): the port mandates a *UploadResultDTO return
// type (defined in ports.go) so the application layer never imports
// internal/infrastructure/drive. This adapter converts infra UploadResult →
// DTO at the seam.

type driveFolderMgrAdapter struct {
	uploader *drive.Uploader
	log      *zap.Logger
}

func newDriveFolderMgrAdapter(uploader *drive.Uploader, log *zap.Logger) youtube.DriveFolderManagerPort {
	if uploader == nil {
		return nil
	}
	return &driveFolderMgrAdapter{uploader: uploader, log: log}
}

func (a *driveFolderMgrAdapter) GetOrCreateFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if a.uploader == nil {
		return parentFolderID, fmt.Errorf("driveFolderMgr: uploader not wired")
	}
	return a.uploader.GetOrCreateFolder(ctx, channelName, parentFolderID)
}

func (a *driveFolderMgrAdapter) UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*youtube.UploadResultDTO, bool, error) {
	if a.uploader == nil {
		return nil, false, fmt.Errorf("driveFolderMgr: uploader not wired")
	}
	res, skipped, err := a.uploader.UploadFileIfChanged(ctx, localPath, folderID, filename)
	if err != nil {
		return nil, skipped, err
	}
	if res == nil {
		return nil, skipped, nil
	}
	return &youtube.UploadResultDTO{FileID: res.FileID, WebViewLink: res.WebViewLink}, skipped, nil
}

// ── FolderMemoryAdapter wraps *foldermemory.Service to satisfy youtube.FolderMemoryPort ──
//
// *foldermemory.Service satisfies the port structurally (LoadManifest,
// SaveManifest, UpdateManifestTXT, ComputeManifestStats). This adapter is
// retained as a stable composition seam so future cache logic can layer
// without changing the port signature.

type folderMemoryAdapter struct {
	inner *foldermemory.Service
}

func newFolderMemoryAdapter(svc *foldermemory.Service) youtube.FolderMemoryPort {
	if svc == nil {
		return nil
	}
	return &folderMemoryAdapter{inner: svc}
}

func (a *folderMemoryAdapter) LoadManifest(manifestPath string) (*asset.ClipManifest, error) {
	return a.inner.LoadManifest(manifestPath)
}

func (a *folderMemoryAdapter) SaveManifest(manifestPath string, manifest *asset.ClipManifest) error {
	return a.inner.SaveManifest(manifestPath, manifest)
}

func (a *folderMemoryAdapter) UpdateManifestTXT(folder *asset.ClipFolder, manifest *asset.ClipManifest) error {
	return a.inner.UpdateManifestTXT(folder, manifest)
}

func (a *folderMemoryAdapter) ComputeManifestStats(manifest *asset.ClipManifest) asset.ClipFolderStats {
	return a.inner.ComputeManifestStats(manifest)
}

// ── PR2 (June 2026): SearchRunnerStub removed.
//
// The previous `searchRunnerStub` was a silent-empty fallback that returned
// `[]youtube.SearchLiveResult{}, nil` and `&youtube.DownloaderMetadata{}, nil`
// when the underlying infrastructure was unavailable. That behaviour was
// indistinguishable from a successful empty search and is the failure mode
// explicitly killed by PR2.
//
// The production wiring is now `ytinfra.NewSearchRunnerAdapter(cfg, log)`
// (see internal/infrastructure/youtube/search_runner_adapter.go), which:
//
//   - returns nil at construction time when cfg or log is nil (composition
//     root detects this and returns an error from BuildDomainBundle);
//   - wraps subprocess errors with `ports.ErrSearchRunnerUnavailable`
//     (search) or `ports.ErrSearchRunnerVideoInfoUnavailable` (info) so
//     callers can branch on the cause via `errors.Is`;
//   - propagates ctx.Err() unwrapped so cancellation is detected faithfully.
//
// The previous `newSearchRunnerStub(log)` constructor and its type are
// intentionally NOT retained here. The test file `youtube_adapters_test.go`
// has been migrated to `internal/infrastructure/youtube/search_runner_adapter_test.go`.
// ──

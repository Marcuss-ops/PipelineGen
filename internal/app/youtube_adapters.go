package app

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/classifier"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
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

func (a *clipStoreAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.Get(ctx, id)
}

func (a *clipStoreAdapter) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.GetClip(ctx, id)
}

func (a *clipStoreAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.inner.Upsert(ctx, clip)
}

func (a *clipStoreAdapter) DeleteClip(ctx context.Context, id string) error {
	return a.inner.DeleteClip(ctx, id)
}

func (a *clipStoreAdapter) UpdateSearchTerms(ctx context.Context, id, source, title string, tags []string, searchText string) error {
	return a.inner.UpdateSearchTerms(ctx, id, source, title, tags, searchText)
}

func (a *clipStoreAdapter) GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error) {
	return a.inner.GetFolder(ctx, folderID)
}

func (a *clipStoreAdapter) ListYouTubeClipIDs(ctx context.Context, limit, offset int) ([]string, error) {
	db := a.inner.DB()
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM media_assets WHERE source = 'youtube' ORDER BY id LIMIT ? OFFSET ?`,
		limit, offset,
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

func (a *clipStoreAdapter) ListEnrichedYouTubeClipIDs(ctx context.Context, limit, offset int) ([]string, error) {
	db := a.inner.DB()
	query := `SELECT id FROM media_assets WHERE source = 'youtube' AND json_extract(metadata_json, '$.youtube_title') != '' ORDER BY id`
	if limit > 0 {
		query += " LIMIT ?"
	}
	if offset > 0 {
		query += " OFFSET ?"
	}
	var args []any
	if limit > 0 {
		args = append(args, limit)
	}
	if offset > 0 {
		args = append(args, offset)
	}
	rows, err := db.QueryContext(ctx, query, args...)
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

func (a *cacheStoreAdapter) ListHotMetadata(ctx context.Context, limit int) ([]youtube.YouTubeCacheEntry, error) {
	rows, err := a.db().DB().QueryContext(ctx,
		`SELECT video_id, metadata_json FROM youtube_video_metadata_cache ORDER BY hit_count DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []youtube.YouTubeCacheEntry
	for rows.Next() {
		var e youtube.YouTubeCacheEntry
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

type clipIndexerAdapter struct {
	inner *clipindexer.Service
}

func (a *clipIndexerAdapter) IsEnabled() bool { return a.inner.IsEnabled() }

func (a *clipIndexerAdapter) IndexClip(ctx context.Context, id string) error {
	return a.inner.IndexClip(ctx, id)
}

// ── OllamaClientAdapter wraps *client.Client to satisfy youtube.OllamaClientPort ──

type ollamaClientAdapter struct {
	inner interface {
		SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error)
	}
}

func (a *ollamaClientAdapter) SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error) {
	return a.inner.SimpleGenerate(ctx, model, prompt, timeout, opts)
}

// ── DriveClientAdapter wraps *driveapi.Service to satisfy youtube.DriveClientPort ──

type driveClientAdapter struct {
	uploader *drive.Uploader
}

func newDriveClientAdapter(svc *driveapi.Service, log *zap.Logger) youtube.DriveClientPort {
	if svc == nil {
		return nil
	}
	return &driveClientAdapter{uploader: &drive.Uploader{Service: svc, Log: log}}
}

func (a *driveClientAdapter) GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	return a.uploader.GetOrCreateFolder(ctx, channelName, parentFolderID)
}

// ── ClassifierAdapter wraps classifier.CachedClassify to satisfy youtube.CategoryClassifierPort ──

type classifierAdapter struct {
	cfg      *config.Config
	log      *zap.Logger
	clip     *assets.ClipsRepository
	ollama   youtube.OllamaClientPort
}

func newClassifierAdapter(cfg *config.Config, log *zap.Logger, clips *assets.ClipsRepository, ollama youtube.OllamaClientPort) youtube.CategoryClassifierPort {
	if clips == nil || ollama == nil {
		return nil
	}
	return &classifierAdapter{cfg: cfg, log: log, clip: clips, ollama: ollama}
}

func (a *classifierAdapter) Classify(ctx context.Context, title string) string {
	cache := &classifierCacheAdapter{clips: a.clip}
	llm := &classifierLLMAdapter{inner: a.ollama}
	return classifier.CachedClassify(ctx, a.log, llm, title, classifier.Options{
		DataDir:          a.cfg.Storage.DataDir,
		Model:            a.cfg.External.OllamaModel,
		FallbackCategory: "general",
		ExcludeCategories: []string{
			"interviews", "general", "other", "clips", "youtube", "videos",
		},
		EnsureCategories:  []string{"rap", "music"},
		DefaultCategories: []string{"boxe", "crime", "discovery", "rap", "music"},
		Cache:             cache,
	})
}

// classifierLLMAdapter wraps OllamaClientPort for classifier.LLMClient interface.
type classifierLLMAdapter struct {
	inner youtube.OllamaClientPort
}

func (a *classifierLLMAdapter) SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error) {
	return a.inner.SimpleGenerate(ctx, model, prompt, timeout, opts)
}

// classifierCacheAdapter wraps ClipsRepository DB for classifier.CategoryCache interface.
type classifierCacheAdapter struct {
	clips *assets.ClipsRepository
}

func (c *classifierCacheAdapter) Get(ctx context.Context, title string) (string, bool) {
	if c.clips == nil || c.clips.DB() == nil {
		return "", false
	}
	var category string
	err := c.clips.DB().QueryRowContext(ctx, "SELECT category FROM youtube_category_cache WHERE video_title = ?", title).Scan(&category)
	if err == nil {
		return category, true
	}
	return "", false
}

func (c *classifierCacheAdapter) Set(ctx context.Context, title, category string) error {
	if c.clips == nil || c.clips.DB() == nil {
		return fmt.Errorf("clips repo not available")
	}
	_, err := c.clips.DB().ExecContext(ctx, "INSERT OR REPLACE INTO youtube_category_cache (video_title, category, cached_at) VALUES (?, ?, datetime('now'))", title, category)
	return err
}

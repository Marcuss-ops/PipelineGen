// Package adapters — youtube_clip_cache_pg.go: PostgreSQL-backed
// ClipCachePort fallback for the canonical YouTube extraction path.
//
// The historical ClipCacheAdapter reads the operational SQLite
// ClipsRepository. After the media cutover the authoritative clip row lives
// in PostgreSQL media_assets, so a SQLite-only lookup reports a false miss
// for every clip committed by the canonical pipeline and the segment is
// re-cut/re-downloaded on every re-run.
//
// PostgresClipCache reads the SAME media SSOT the writer commits to. It keeps
// the cache contract of the SQLite adapter:
//   - hit  → an existing, non-deleted asset with a usable local file OR a
//     Drive reference;
//   - miss → no row, deleted row, or no usable file reference;
//   - error → the underlying read failed.
//
// FallbackClipCache composes the two: SQLite first (legacy/operational rows),
// PostgreSQL second (current canonical rows). A miss on both is a miss; a
// backend error is logged and the next backend is still tried.
package adapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ClipReader is the narrow read surface PostgresClipCache needs. The
// production implementation is pgmedia.MediaSearcher; a narrow interface
// keeps the adapter testable without a database.
type ClipReader interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
}

// PostgresClipCache adapts the PostgreSQL media read surface to ClipCachePort.
type PostgresClipCache struct {
	reader ClipReader
	log    *zap.Logger
}

// NewPostgresClipCache wires the adapter. A nil reader returns nil so the
// composition can omit the fallback cleanly.
func NewPostgresClipCache(reader ClipReader, log *zap.Logger) *PostgresClipCache {
	if reader == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &PostgresClipCache{reader: reader, log: log}
}

var _ youtubeports.ClipCachePort = (*PostgresClipCache)(nil)

// GetExisting returns the cached ExtractItem for clipID from PostgreSQL.
func (c *PostgresClipCache) GetExisting(ctx context.Context, clipID string) (*youtubetypes.ExtractItem, bool, error) {
	if c == nil || c.reader == nil {
		return nil, false, fmt.Errorf("PostgresClipCache: not wired (composition must inject a media clip reader)")
	}
	if clipID == "" {
		return nil, false, fmt.Errorf("PostgresClipCache.GetExisting: clipID is required")
	}
	clip, err := c.reader.GetClip(ctx, clipID)
	if err != nil {
		return nil, false, fmt.Errorf("PostgresClipCache.GetExisting: %w", err)
	}
	if clip == nil {
		return nil, false, nil
	}
	if clip.LifecycleState == asset.StateDeleted {
		return nil, false, nil
	}

	localPath := clip.LocalPath()
	if localPath != "" {
		stat, statErr := os.Stat(localPath)
		if statErr != nil || stat.Size() == 0 {
			c.log.Info("PostgresClipCache: cache miss — local file missing or empty, falling through to re-download",
				zap.String("clip_id", clipID),
				zap.String("local_path", localPath),
				zap.Any("stat_err", statErr))
			return nil, false, nil
		}
	} else if clip.DriveFileID() == "" {
		c.log.Info("PostgresClipCache: cache miss — no local file and no Drive reference, falling through to re-download",
			zap.String("clip_id", clipID))
		return nil, false, nil
	}

	return postgresClipToExtractItem(clip), true, nil
}

// FallbackClipCache tries the operational SQLite cache first and the
// PostgreSQL media cache second. It guarantees the ClipCachePort contract:
// the first hit wins, both misses return (nil, false, nil), and backend
// errors are logged rather than turning a healthy miss into a pipeline
// failure (the use case already falls through on cache errors).
type FallbackClipCache struct {
	primary   youtubeports.ClipCachePort
	secondary youtubeports.ClipCachePort
	log       *zap.Logger
}

// NewFallbackClipCache composes the two cache surfaces.
func NewFallbackClipCache(primary, secondary youtubeports.ClipCachePort, log *zap.Logger) *FallbackClipCache {
	if log == nil {
		log = zap.NewNop()
	}
	return &FallbackClipCache{primary: primary, secondary: secondary, log: log}
}

var _ youtubeports.ClipCachePort = (*FallbackClipCache)(nil)

// GetExisting implements ClipCachePort.
func (c *FallbackClipCache) GetExisting(ctx context.Context, clipID string) (*youtubetypes.ExtractItem, bool, error) {
	if c == nil {
		return nil, false, nil
	}
	for _, cache := range []youtubeports.ClipCachePort{c.primary, c.secondary} {
		if cache == nil {
			continue
		}
		item, ok, err := cache.GetExisting(ctx, clipID)
		if err != nil {
			c.log.Warn("clip cache backend failed; trying next backend",
				zap.String("clip_id", clipID), zap.Error(err))
			continue
		}
		if ok && item != nil {
			return item, true, nil
		}
	}
	return nil, false, nil
}

// postgresClipToExtractItem maps the canonical asset shape to the cache DTO.
func postgresClipToExtractItem(a *asset.Asset) *youtubetypes.ExtractItem {
	if a == nil {
		return nil
	}
	name := a.Name
	if name == "" {
		name = a.ID
	}
	filename := a.Filename
	if filename == "" && a.LocalPath() != "" {
		filename = filepath.Base(a.LocalPath())
	}
	item := &youtubetypes.ExtractItem{
		ID:              a.ID,
		Name:            name,
		Filename:        filename,
		ContentSHA256:   a.ContentHash(),
		LocalPath:       a.LocalPath(),
		DriveFileID:     a.DriveFileID(),
		DriveLink:       a.DriveLink(),
		DownloadLink:    a.DownloadLink(),
		DriveFolderID:   a.FolderID(),
		DriveFolderPath: a.FolderPath(),
		Status:          "skipped",
	}
	if a.Duration > 0 {
		item.Duration = int(a.Duration.Seconds())
	}
	return item
}

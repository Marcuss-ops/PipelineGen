// Package app — helper functions extracted from registry.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// standalone helpers used by WireRegistry: asset service initialisation,
// media processor construction, sync target building, Drive folder definition,
// and style-folder pre-creation.
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// ── Asset service initialisation ────────────────────────────────────────────

func initAssetServices(dbs *databases, log *zap.Logger) (*assetindex.Service, *assettree.Service, error) {
	assetIndexRepo := assetindex.NewRepository(dbs.main.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	assetTreeRepo, err := assets.NewAssetTreeRepository(dbs.main.DB, log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	return assetIndexService, assetTreeService, nil
}

// ── Drive destinations ──────────────────────────────────────────────────────

// DriveDestinations groups the canonical Drive folder IDs for media assets.
type DriveDestinations struct {
	MediaRoot, SoundEffectsRoot, imagesFolder string
}

func (d *DriveDestinations) RootFolder() string   { return d.MediaRoot }
func (d *DriveDestinations) ImagesFolder() string { return d.imagesFolder }

// ── Media processor initialisation ──────────────────────────────────────────

// initMediaProcessor wires the media processor. PG-011: db is now
// *storage.SQLiteDB (the typed canonical handle) instead of raw *sql.DB;
// the artifacts.NewClipsRegistry constructor still takes *sql.DB so we
// deref via db.DB at the call site — this keeps the upstream contract
// unchanged while letting the composition layer stop holding a raw
// sqlite handle in signatures.
func initMediaProcessor(cfg *config.Config, db *storage.SQLiteDB, assetsRepo asset.Repository, querySvc *asset.Service, locations asset.LocationRepository, processing asset.ProcessingRepository, log *zap.Logger, driveUploader *driveup.Uploader) asset.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.NewFromConfig(cfg)
	clipsRegistry := artifacts.NewClipsRegistry(db.DB, assetsRepo, querySvc, locations, processing)
	return processor.NewProcessor(ytDLPDownloader, httpDL, ffmpegProc, log, processor.ProcessorConfig{DataDir: cfg.Storage.DataDir, TempDir: cfg.Storage.TempDir, VideoCfg: ffmpeg.DefaultNormalizeOptions(cfg), ScraperServerURL: cfg.External.ArtlistScraperServerURL, EmbeddingServerURL: cfg.ClipIndexer.ServerURL}, clipsRegistry, driveUploader)
}

// ── Sync target building ────────────────────────────────────────────────────

func buildSyncTargets(cfg *config.Config, clipsOnlyRepo *assets.ClipsRepository, clipsRepo *assets.ClipsRepository, artlistRepo *assets.ClipsRepository) []catalogsync.Target {
	return []catalogsync.Target{
		{Name: "stock", RootFolderID: cfg.Drive.StockFolder(), Source: "stock", MediaType: "stock", Repo: clipsRepo},
		{Name: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Source: "youtube", MediaType: "clip", Repo: clipsOnlyRepo},
		{Name: "artlist", RootFolderID: cfg.Drive.ArtlistFolder(), Source: "artlist", MediaType: "artlist", Repo: artlistRepo},
	}
}

// ── Style Drive folder pre-creation ─────────────────────────────────────────

func ensureStyleDriveFolders(ctx context.Context, uploader *driveup.Uploader, rootID string, styleRegistry *generation.StyleRegistry, log *zap.Logger) {
	if uploader == nil || strings.TrimSpace(rootID) == "" || styleRegistry == nil {
		return
	}
	for _, st := range styleRegistry.List() {
		name := strings.TrimSpace(st.Name)
		if name == "" {
			continue
		}
		if _, err := uploader.GetOrCreateFolder(ctx, name, rootID); err != nil && log != nil {
			log.Warn("failed to pre-create style folder", zap.String("style", name), zap.String("root_id", rootID), zap.Error(err))
		}
	}
}

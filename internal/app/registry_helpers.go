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
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	ffmpegtypes "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg/types"
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
//
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed): mutationsDisp
// is the 8th positional arg so the embedded artifacts.NewClipsRegistry
// routes its media_assets UPSERT through the canonical outbox+tx
// writer. The PR-7 deferred-hydration strategy (hydrateMediaProcessor +
// MediaProcessor=nil) is gone; BuildProcessBundle now consumes
// outbox.OutboxBundle inline and constructs MediaProcessor directly
// (see composition.go::buildQdrantDeps + BuildProcessBundle for the
// strict-DAG shape: qdrantDeps -> outbox -> process).
//
// Fail-closed at the composition root: BuildProcessBundle returns
// MediaProcessor=nil if outbox.Dispatcher is nil so worker / reprocess
// / ingest paths surface the missing dep rather than silently defaulting
// to the legacy path.
// F2.8 (June 2026): the trailing arg swaps from `*driveup.Uploader`
// to `delivery.Publisher`. processor.NewProcessor now panics on nil
// publisher (composition-time fail-fast), so a wiring gap becomes
// loud at boot rather than silent on first upload. The Publisher
// canonically routes every Drive write through the DestinationRegistry
// + RequireSubpath + ConflictPolicy belt; the legacy direct-uploader
// bypass is closed. driveup is still imported because
// ensureStyleDriveFolders (below) takes the concrete Uploader for
// Style folder pre-creation — that surface is OUT OF SCOPE for F2.8.
func initMediaProcessor(cfg *config.Config, db *storage.SQLiteDB, assetsRepo asset.Repository, querySvc *asset.Service, locations asset.LocationRepository, processing asset.ProcessingRepository, mutationsDisp mutations.AssetMutationDispatcher, log *zap.Logger, publisher delivery.Publisher) asset.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.NewFromConfig(cfg)
	clipsRegistry := artifacts.NewClipsRegistry(db.DB, assetsRepo, querySvc, locations, processing, mutationsDisp)
	return processor.NewProcessor(ytDLPDownloader, httpDL, ffmpegProc, log, processor.ProcessorConfig{DataDir: cfg.Storage.DataDir, TempDir: cfg.Storage.TempDir, VideoCfg: ffmpegtypes.DefaultNormalizeOptions(cfg), ScraperServerURL: cfg.External.ArtlistScraperServerURL, EmbeddingServerURL: cfg.ClipIndexer.ServerURL}, clipsRegistry, publisher)
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

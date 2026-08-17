// Package app — wire_stock_pipeline.go (PR-STOCK-ATLASTORCH-DISPATCH commit-4, July 2026).
//
// WireStockPipeline is the canonical composition-root entry point that
// constructs every stock pipeline dependency and routes them through
// BuildStockBundle. It was extracted from the inline Step 8 block in
// registry_internal_modules.go per the audit recommendation (AGENTS.md
// Pattern 5 — single-purpose capability file).
//
// godlike/06 SSOT (one canonical owner per fact):
//
//   - This file is the SOLE owner of the stock-pipeline dep construction
//     sequence (DB → Cutter → Renderer → yt-dlp → Fetch → SourceStager →
//     Finalizer → BuildStockBundle).
//   - BuildStockBundle in build_bundles_stock.go owns the bundle assembly.
//   - registry_internal_modules.go owns the module registration.
//
// godlike/07 fail-closed: every nil dep surfaces a typed sentinel or
// logged Warn so operators see exactly which gate fired instead of the
// legacy silent nil → 404.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	jobsfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/stockbatches"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/stocksourcecache"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/filesystem"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

// WireStockPipeline constructs every stock pipeline dependency from the
// wiring.ComposeRoot and routes them through BuildStockBundle. Returns a fully
// populated *wiring.StockPipelineWiring on success, or (nil, error) when a
// required dep is missing (godlike/07 fail-closed).
//
// Dep construction sequence:
//   - DB: root.DB.DB (embedded *sql.DB from *storage.SQLiteDB)
//   - Cutter: render.NewConfiguredCutter(...)
//   - Renderer: rustexec.NewStockRenderer(cfg.External.RustMusclesPath, ...)
//   - ChannelLister + SourceStager Fetch: downloader.NewYTDLP(cfg)
//   - SourceStager: WireAcquisitionStager with real yt-dlp Fetch closure
//   - Finalizer: jobsfinalizer.New (Publisher+Finalizer paired → gate passes)
//
// Returns (nil, nil) when StockPipelineEnabled is false — the caller
// treats nil wiring as "route not mounted" (no error, no registration).
func WireStockPipeline(cfg *config.Config, log *zap.Logger, root *wiring.ComposeRoot) (*wiring.StockPipelineWiring, error) {
	if cfg == nil {
		return nil, fmt.Errorf("wire stock pipeline: cfg is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("wire stock pipeline: log is nil")
	}
	if root == nil {
		return nil, fmt.Errorf("wire stock pipeline: root is nil")
	}

	if !cfg.Features.StockPipelineEnabled {
		log.Info("WireStockPipeline: stock pipeline disabled by cfg flag; returning nil wiring")
		return nil, nil
	}

	// ── Construct real deps ──────────────────────────────────
	if root.Drive == nil {
		return nil, fmt.Errorf("wire stock pipeline: Drive bundle is required")
	}
	if root.Jobs == nil {
		return nil, fmt.Errorf("wire stock pipeline: Jobs bundle is required")
	}
	if root.Repos == nil {
		return nil, fmt.Errorf("wire stock pipeline: repository bundle is required")
	}
	if root.Search == nil {
		return nil, fmt.Errorf("wire stock pipeline: search bundle is required")
	}
	if root.Outbox == nil {
		return nil, fmt.Errorf("wire stock pipeline: outbox bundle is required")
	}
	ffmpegPath := cfg.External.FfmpegPath
	mediaConfig := root.MediaExec
	if mediaConfig == (mediaexec.ExecutionConfig{}) {
		return nil, fmt.Errorf("wire stock pipeline: resolved media execution config is required")
	}

	// DB: extract *sql.DB from typed *storage.SQLiteDB handle.
	stockDB := (*sql.DB)(nil)
	if root.DB != nil {
		stockDB = root.DB.DB
	}
	if stockDB == nil {
		return nil, fmt.Errorf("wire stock pipeline: SQLite DB handle is required")
	}
	if root.Jobs.Service == nil || root.Jobs.Repo == nil {
		return nil, fmt.Errorf("wire stock pipeline: Jobs service and repository are required")
	}
	if root.Repos.ClipsRepo == nil {
		return nil, fmt.Errorf("wire stock pipeline: Clips repository is required")
	}
	if root.Search.AssetIndexService == nil {
		return nil, fmt.Errorf("wire stock pipeline: asset index service is required")
	}
	if root.Outbox.Dispatcher == nil || root.Outbox.EventsRepo == nil {
		return nil, fmt.Errorf("wire stock pipeline: outbox dispatcher and events repository are required")
	}
	if root.Drive.Admin == nil || root.Drive.Reader == nil || root.Drive.Publisher == nil {
		return nil, fmt.Errorf("wire stock pipeline: Drive admin, reader, and publisher are required")
	}

	// Cutter, renderer, and probe share one Executor so resource limits,
	// process-group cancellation, bounded diagnostics, and .part cleanup are
	// owned by this composition root rather than each adapter separately.
	rustExecutor := rustexec.NewExecutor(cfg.External.RustMusclesPath, ffmpegPath, log)
	stockCutter, cutterErr := render.NewConfiguredCutterWithExecutor(
		cfg.External.RustMusclesPath,
		ffmpegPath,
		mediaConfig.Policy,
		mediaConfig.Profile,
		log,
		rustExecutor,
	)
	if cutterErr != nil {
		return nil, fmt.Errorf("wire stock pipeline: configure cutter: %w", cutterErr)
	}
	stockRenderer := rustexec.NewStockRendererWithExecutor(rustExecutor, mediaConfig.Policy, mediaConfig.Profile, log)

	// ChannelLister + SourceStager: share the same yt-dlp downloader.
	// StockDownloaderAdapter bridges the concrete YTDLPDownloader to the
	// application-layer ports (SourceDownloader + ChannelLister).
	ytdlp := downloader.NewYTDLP(cfg)
	stockAdapter := downloader.NewStockAdapter(ytdlp)

	// SourceStager: wire a real yt-dlp Fetch closure so Prepare
	// downloads source videos via yt-dlp subprocess. The closure
	// maps appacq.PrepareRequest → downloader.DownloadRequest,
	// then resolves the yt-dlp output file and renames it to the
	// canonical dstPath the FilesystemStager expects.
	ytdlpFetch := func(ctx context.Context, req appacq.PrepareRequest, dstPath string, onWireSHA256 func(string)) error {
		dlReq := &downloader.DownloadRequest{
			URL:        req.Source.URL,
			OutputPath: dstPath + ".%(ext)s",
			Timeout:    req.Timeout,
			UseCookies: true, // July 2026: android_creator client supports cookies without n-challenge
		}
		if req.Source.DownloadSection != "" {
			dlReq.DownloadSections = []string{req.Source.DownloadSection}
			dlReq.ForceKeyframes = req.Source.ForceKeyframes
		}
		if req.Source.MergeFormat != "" {
			dlReq.MergeFormat = req.Source.MergeFormat
		}
		if err := ytdlp.Download(ctx, dlReq); err != nil {
			return err
		}
		// yt-dlp writes to OutputPath.%(ext)s; resolve the actual file
		// and rename it to the canonical dstPath.
		outputTemplate := dstPath + ".%(ext)s"
		resolved, resolveErr := downloader.ResolveDownloadedSegmentPath(outputTemplate)
		if resolveErr != nil {
			return resolveErr
		}
		if resolved != dstPath {
			if err := os.Rename(resolved, dstPath); err != nil {
				return err
			}
		}
		return nil
	}
	stockSourceStager, stagerErr := WireAcquisitionStager(cfg, log, ytdlpFetch)
	if stagerErr != nil {
		log.Warn("WireStockPipeline: WireAcquisitionStager failed (godlike/07 fail-closed: source staging will return typed error)",
			zap.Error(stagerErr))
		stockSourceStager = nil
	}

	// Finalizer: single-TX spine for SUCCEEDED state + artifact writes.
	var stockFinalizer finalization.JobFinalizer
	if stockDB != nil && root.Outbox != nil && root.Outbox.EventsRepo != nil {
		assetCommitter := newCanonicalAssetCommitter(stockDB, root.Outbox.EventsRepo, log)
		assetTx := assetfinalizer.NewAssetTxFinalizer(log, assetCommitter)
		if root.TextTracks != nil {
			assetTx.WithFanOut(root.TextTracks.FanOut)
		}
		stockFinalizer = jobsfinalizer.New(stockDB, root.Outbox.EventsRepo, assetTx, log)
	} else {
		log.Warn("WireStockPipeline: Finalizer not constructed (godlike/07: one or more required deps nil — stockDB, root.Outbox, or root.Outbox.EventsRepo). If Publisher is also non-nil, the symmetric gate will fire ErrStockProductionJobFinalizerMissing.",
			zap.Bool("stockDB_nil", stockDB == nil),
			zap.Bool("root_Outbox_nil", root.Outbox == nil),
			zap.Bool("EventsRepo_nil", root.Outbox == nil || root.Outbox.EventsRepo == nil),
		)
	}

	// ── Diagnostic: wiring status summary ────────────────────
	// Emitted at Info so operators see it even without --debug.
	// Uses zap.Bool (same pattern as the Finalizer Warn above)
	// so every field is a simple true/false the operator can
	// scan in one line: "are Publisher and Finalizer wired?"
	log.Info("WireStockPipeline: wiring summary",
		zap.Bool("publisher_wired", root.Drive != nil && root.Drive.Publisher != nil),
		zap.Bool("finalizer_wired", stockFinalizer != nil),
		zap.Bool("source_stager_wired", stockSourceStager != nil),
		zap.String("ffmpeg_path", ffmpegPath),
	)

	// ── Local FS port (Pattern 0 typed port for the cache) ──
	// PR-REFACTOR-P0-IO-BINDER: the application layer cannot call
	// os.* directly. Always inject the LocalAdapter so the cache
	// copy + validate paths have a port to route through.
	stockLocalFS := filesystem.NewLocal()

	// ── Source cache (cross-run dedup) ──────────────────────
	// Construct the SQLite-backed source cache repository when DB
	// is available. The cache is optional — nil reader/writer means
	// every download is fresh (no cross-run dedup).
	var stockCacheReader stockpipeline.SourceCacheReader
	var stockCacheWriter stockpipeline.SourceCacheWriter
	if stockDB != nil {
		cacheRepo := stocksourcecache.NewRepository(stockDB)
		stockCacheReader = cacheRepo
		stockCacheWriter = cacheRepo
		log.Info("WireStockPipeline: source cache wired",
			zap.Bool("cache_enabled", true))
	} else {
		log.Info("WireStockPipeline: source cache disabled (no DB)")
	}

	// Batch repository (Fase 2 durable state) — wired when DB is available.
	var stockBatchRepo stockpipeline.StockBatchRepository
	if stockDB != nil {
		stockBatchRepo = stockbatches.NewRepository(stockDB)
		log.Info("WireStockPipeline: batch repository wired")
	}

	var stockMetadataUpdater stockenrich.AssetMetadataUpdater
	if stockDB != nil && root.Outbox != nil && root.Outbox.EventsRepo != nil {
		stockMetadataUpdater = newCanonicalAssetCommitter(stockDB, root.Outbox.EventsRepo, log)
	}

	// PublisherPort: construct the canonical drive.NewArtifactPublisherAdapter
	// so the application layer stays free of internal/infrastructure/drive imports.
	var stockPublisherPort finalization.PublisherPort
	if root.Drive != nil && root.Drive.Publisher != nil {
		stockPublisherPort = drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log)
	}

	// Source duration validation and manifest projection are production
	// capabilities, not optional test conveniences. Both are built from
	// concrete infrastructure already owned by the composition root.
	stockProbe := render.NewFFProbeSourceDurationProbe(rustexec.NewConfiguredVideoProcessorWithExecutor(rustExecutor, mediaConfig.Policy, mediaConfig.Profile, log))
	stockProjection := newStockProjection()

	return BuildStockBundle(StockBundleDeps{
		Runtime: StockRuntimeDeps{
			Cfg: cfg,
			Log: log,
			DB:  stockDB,
			JobCreator: func() stockpipeline.JobCreator {
				if root.Jobs == nil {
					return nil
				}
				return root.Jobs.Repo
			}(),
			StepStore: func() steps.Store {
				if stockDB == nil {
					return nil
				}
				return steps.NewSQLiteStore(stockDB)
			}(),
		},
		Delivery: StockDeliveryDeps{
			Publisher:     root.Drive.Publisher,
			PublisherPort: stockPublisherPort,
			Finalizer:     stockFinalizer,
			Projection:    stockProjection,
		},
		Acquisition: StockAcquisitionDeps{
			SourceStager:    stockSourceStager,
			SourceProbe:     stockProbe,
			ClipsRepo:       root.Repos.ClipsRepo,
			AssetIndex:      root.Search.AssetIndexService,
			Dispatcher:      root.Outbox.Dispatcher,
			BatchRepository: stockBatchRepo,
			DriveReader:     root.Drive.Reader,
		},
		Media: StockMediaDeps{
			Cutter:   stockCutter,
			Renderer: stockRenderer,
		},
		Orchestration: StockOrchestrationDeps{
			Jobs:          root.Jobs.Service,
			ChannelLister: stockAdapter,
			FolderCreator: root.Drive.Admin,
		},
		Feature: StockFeatureGate{
			StockPipelineEnabled: func() bool { return cfg.Features.StockPipelineEnabled },
		},
		Enrichment: StockEnrichmentDeps{
			AssetMetadataUpdater: stockMetadataUpdater,
		},
		SourceCache: StockSourceCacheDeps{
			Reader:  stockCacheReader,
			Writer:  stockCacheWriter,
			LocalFS: stockLocalFS,
		},
	})
}

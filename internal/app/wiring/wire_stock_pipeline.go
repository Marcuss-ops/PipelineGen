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
//
// BON-03 (2026-11-23): the entry point is an assembly sequence, not a
// 300-line procedure. Each dependency group has its own constructor, so the
// order of construction and the order of the gates can be read (and changed)
// one unit at a time, and the gate errors keep the message the operators
// already know.
package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"go.uber.org/zap"

	stockwiring "github.com/Marcuss-ops/PipelineGen/internal/app/modules/stock"
	appacq "github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	jobsfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	infacq "github.com/Marcuss-ops/PipelineGen/internal/platform/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/executionsteps"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/stockbatches"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/stocksourcecache"
)

// stockPipelineRoot is the ComposeRoot surface the stock pipeline consumes,
// already gated. Resolving it once keeps the entry point to three readable
// phases: gate → construct → assemble.
type stockPipelineRoot struct {
	DB       *sql.DB
	FFmpeg   string
	MediaCfg mediaexec.ExecutionConfig
}

// WireStockPipeline constructs every stock pipeline dependency from the
// ComposeRoot and routes them through BuildStockBundle. Returns a fully
// populated *StockPipelineWiring on success, or (nil, error) when a
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
func WireStockPipeline(cfg *config.Config, log *zap.Logger, root *ComposeRoot) (*stockwiring.StockPipelineWiring, error) {
	resolved, enabled, err := resolveStockPipelineRoot(cfg, log, root)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}

	// Cutter and probe share one Executor so resource limits, process-group
	// cancellation, bounded diagnostics, and .part cleanup are owned by this
	// composition root rather than each adapter separately.
	rustExecutor := rustexec.NewExecutor(cfg.External.RustMusclesPath, resolved.FFmpeg, log)

	stockCutter, err := newStockCutter(cfg, resolved, log, rustExecutor)
	if err != nil {
		return nil, err
	}

	stockDownloader := downloader.NewYTDLP(cfg)
	stockChannelLister := downloader.NewStockAdapter(stockDownloader)
	stockSourceStager := newStockSourceStager(cfg, log, stockDownloader)

	stockFinalizer, err := newStockFinalizer(root, log)
	if err != nil {
		return nil, err
	}

	// ── Diagnostic: wiring status summary ────────────────────
	// Emitted at Info so operators see it even without --debug.
	// Uses zap.Bool (same pattern as the Finalizer Warn) so every field is a
	// simple true/false the operator can scan in one line: "are Publisher and
	// Finalizer wired?"
	log.Info("WireStockPipeline: wiring summary",
		zap.Bool("publisher_wired", root.Drive != nil && root.Drive.Publisher != nil),
		zap.Bool("finalizer_wired", stockFinalizer != nil),
		zap.Bool("source_stager_wired", stockSourceStager != nil),
		zap.String("ffmpeg_path", resolved.FFmpeg),
	)

	stockCacheReader, stockCacheWriter := newStockSourceCache(root, log)
	stockBatchRepo := newStockBatchRepository(resolved.DB, log)

	stockMetadataUpdater, err := newStockMetadataUpdater(root, log)
	if err != nil {
		return nil, err
	}

	return BuildStockBundle(StockBundleDeps{
		Runtime: newStockRuntimeDeps(cfg, log, root, resolved.DB),
		Delivery: StockDeliveryDeps{
			Publisher:     root.Drive.Publisher,
			PublisherPort: newStockPublisherPort(root, log),
			Finalizer:     stockFinalizer,
			Projection:    stockwiring.NewProjection(),
		},
		Acquisition: StockAcquisitionDeps{
			SourceStager:    stockSourceStager,
			SourceProbe:     newStockSourceProbe(resolved, log, rustExecutor),
			ClipsRepo:       root.Repos.ClipsRepo,
			AssetIndex:      root.Search.AssetIndexService,
			Dispatcher:      root.Outbox.Dispatcher,
			BatchRepository: stockBatchRepo,
			// PR-STOCK-DESTINATION-RECONCILE: the stock acquisition Drive port
			// needs the ownership-aware listing (ListFilesWithAppProperties)
			// and TrashFile, which live on the concrete *drive.Uploader and
			// not on the canonical drive.Reader port. Passing the concrete
			// uploader here lets the adapter stamp PipelineOwned and drive the
			// destination GC without widening drive.Reader for every consumer.
			DriveReader: root.Drive.DriveUploader,
		},
		Media: StockMediaDeps{
			Cutter: stockCutter,
		},
		Orchestration: StockOrchestrationDeps{
			Jobs:          root.Jobs.Service,
			ChannelLister: stockChannelLister,
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
			LocalFS: filesystem.NewLocal(),
		},
	})
}

// newStockRuntimeDeps builds the runtime half of the bundle: the config, the
// logging surface, the operational DB, and the two lazy adapters (job creator
// and step store) that must stay nil when their plane is absent.
func newStockRuntimeDeps(cfg *config.Config, log *zap.Logger, root *ComposeRoot, db *sql.DB) StockRuntimeDeps {
	return StockRuntimeDeps{
		Cfg: cfg,
		Log: log,
		DB:  db,
		// The enrichment read plane is pinned to the PostgreSQL media SSOT
		// (never the operational SQLite store) — see StockRuntimeDeps.
		MediaDB: root.MediaPostgres,
		JobCreator: func() stockpipeline.JobCreator {
			if root.Jobs == nil {
				return nil
			}
			return root.Jobs.Repo
		}(),
		StepStore: func() steps.Store {
			if db == nil {
				return nil
			}
			return executionsteps.NewSQLiteStore(db)
		}(),
	}
}

// resolveStockPipelineRoot runs every fail-closed gate in the order operators
// have learned to expect, and returns (zero, false, nil) when the feature flag
// is off — a disabled pipeline is not an error, it is an unmounted route.
func resolveStockPipelineRoot(cfg *config.Config, log *zap.Logger, root *ComposeRoot) (stockPipelineRoot, bool, error) {
	var resolved stockPipelineRoot
	if cfg == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: cfg is nil")
	}
	if log == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: log is nil")
	}
	if root == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: root is nil")
	}

	if !cfg.Features.StockPipelineEnabled {
		log.Info("WireStockPipeline: stock pipeline disabled by cfg flag; returning nil wiring")
		return resolved, false, nil
	}

	// ── Construct real deps ──────────────────────────────────
	if root.Drive == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: Drive bundle is required")
	}
	if root.Jobs == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: Jobs bundle is required")
	}
	if root.Repos == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: repository bundle is required")
	}
	if root.Search == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: search bundle is required")
	}
	if root.Outbox == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: outbox bundle is required")
	}
	resolved.FFmpeg = cfg.External.FfmpegPath
	resolved.MediaCfg = root.MediaExec
	if resolved.MediaCfg == (mediaexec.ExecutionConfig{}) {
		return resolved, false, fmt.Errorf("wire stock pipeline: resolved media execution config is required")
	}

	// DB: extract *sql.DB from typed *storage.SQLiteDB handle.
	if root.DB != nil {
		resolved.DB = root.DB.DB
	}
	if resolved.DB == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: SQLite DB handle is required")
	}
	if root.Jobs.Service == nil || root.Jobs.Repo == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: Jobs service and repository are required")
	}
	if root.Repos.ClipsRepo == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: Clips repository is required")
	}
	if root.Search.AssetIndexService == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: asset index service is required")
	}
	if root.Outbox.Dispatcher == nil || root.Outbox.EventsRepo == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: outbox dispatcher and events repository are required")
	}
	if root.Drive.Admin == nil || root.Drive.Reader == nil || root.Drive.Publisher == nil {
		return resolved, false, fmt.Errorf("wire stock pipeline: Drive admin, reader, and publisher are required")
	}
	return resolved, true, nil
}

// newStockCutter builds the packet-copy cutter. Stock clips are assembly
// inputs whose canonical certificate is produced by the CPU libx264/veryfast
// lane, so this pipeline must not inherit the user-facing render encoder
// (which may be NVENC when GPU rendering is required).
func newStockCutter(cfg *config.Config, resolved stockPipelineRoot, log *zap.Logger, executor *rustexec.Executor) (stockpipeline.VideoCutter, error) {
	stockMediaConfig := resolved.MediaCfg
	stockMediaConfig.Policy.Codec = config.CanonicalStockEncoderCodec
	stockMediaConfig.Policy.Preset = config.CanonicalStockEncoderPreset
	cutter, err := render.NewConfiguredCutterWithExecutionConfig(
		cfg.External.RustMusclesPath,
		resolved.FFmpeg,
		stockMediaConfig,
		log,
		executor,
	)
	if err != nil {
		return nil, fmt.Errorf("wire stock pipeline: configure cutter: %w", err)
	}
	return cutter, nil
}

// newStockSourceStager wires the real yt-dlp Fetch closure so Prepare
// downloads source videos through the yt-dlp subprocess. A failure is not a
// crash: godlike/07 fail-closed logs a Warn and the staging step returns its
// typed error at run time instead of the legacy silent nil.
func newStockSourceStager(cfg *config.Config, log *zap.Logger, ytdlp *downloader.YTDLPDownloader) appacq.SourceStager {
	stager, err := WireAcquisitionStager(cfg, log, ytdlpStockFetch(ytdlp))
	if err != nil {
		log.Warn("WireStockPipeline: WireAcquisitionStager failed (godlike/07 fail-closed: source staging will return typed error)",
			zap.Error(err))
		return nil
	}
	return stager
}

// ytdlpStockFetch maps an appacq.PrepareRequest onto the downloader request,
// then resolves the yt-dlp output template back to the canonical dstPath the
// FilesystemStager expects.
func ytdlpStockFetch(ytdlp *downloader.YTDLPDownloader) infacq.FetchFn {
	return func(ctx context.Context, req appacq.PrepareRequest, dstPath string, onFetchResult func(infacq.FetchResult)) error {
		dlReq := &downloader.DownloadRequest{
			URL:        req.Source.URL,
			OutputPath: dstPath + ".%(ext)s",
			Timeout:    req.Timeout,
			UseCookies: true, // July 2026: android_creator client supports cookies without n-challenge
		}
		if req.Source.DownloadSection != "" {
			dlReq.DownloadSections = []string{req.Source.DownloadSection}
			dlReq.ForceKeyframes = req.Source.ForceKeyframes
			dlReq.ReturnSourceWindows = true
		}
		if req.Source.MergeFormat != "" {
			dlReq.MergeFormat = req.Source.MergeFormat
		}
		sourceWindow, err := ytdlp.DownloadWithSourceWindow(ctx, dlReq)
		if err != nil {
			return err
		}
		// yt-dlp writes to OutputPath.%(ext)s; resolve the actual file and
		// rename it to the canonical dstPath.
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
		if sourceWindow != nil && onFetchResult != nil {
			onFetchResult(infacq.FetchResult{SHA256: sourceWindow.AssetSHA256, SourceWindow: sourceWindow})
		}
		return nil
	}
}

// newStockFinalizer builds the single-TX spine for SUCCEEDED state and artifact
// writes. With the outbox closed it returns (nil, nil) and logs the symmetric
// gate that will fire if a Publisher is present.
func newStockFinalizer(root *ComposeRoot, log *zap.Logger) (finalization.JobFinalizer, error) {
	if root.Outbox == nil || root.Outbox.EventsRepo == nil {
		log.Warn("WireStockPipeline: Finalizer not constructed (godlike/07: one or more required deps nil — root.Outbox or root.Outbox.EventsRepo). If Publisher is also non-nil, the symmetric gate will fire ErrStockProductionJobFinalizerMissing.",
			zap.Bool("root_Outbox_nil", root.Outbox == nil),
			zap.Bool("EventsRepo_nil", root.Outbox == nil || root.Outbox.EventsRepo == nil),
		)
		return nil, nil
	}

	assetCommitter, err := newCanonicalAssetCommitter(root.MediaPostgres, log)
	if err != nil {
		return nil, fmt.Errorf("WireStockPipeline: canonical media writer: %w", err)
	}
	if assetCommitter == nil {
		return nil, nil
	}

	assetTx := assetfinalizer.NewAssetTxFinalizer(log, assetCommitter)
	if root.TextTracks != nil {
		assetTx.WithFanOut(root.TextTracks.FanOut)
	}
	// Mirror the canonical broker finalizer wiring
	// (wire_services_composition.go): the job terminal state lives on the JOBS
	// plane, not on the media SQLite file. Wiring the media DB here made the
	// spine write fail with `select job: no such table: jobs`.
	jobDB := root.DB.DB
	if root.Jobs != nil && root.Jobs.DB != nil && root.Jobs.DB.DB != nil {
		jobDB = root.Jobs.DB.DB
	}
	jobOutbox := outboxevents.NewRepository(jobDB)
	jobsFinalizer := jobsfinalizer.New(jobDB, jobOutbox, nil, log)
	// MEDIA-SSOT P0-2 (September 2026): the canonical media writer is ALWAYS
	// PostgreSQL (newCanonicalAssetCommitter returns non-nil only for a media
	// PostgreSQL handle), while the job terminal state lives on the SQLite job
	// plane. The two engines cannot share a transaction, so the two-phase
	// split-plane boundary is unconditional here. Passing assetTx (a PostgreSQL
	// committer) into jobsfinalizer.New alongside a SQLite job DB was the
	// cross-engine branch: the job tx would have carried PostgreSQL SQL and
	// failed with `unrecognized token: ":"`.
	return &splitPlaneFinalizer{
		mediaDB:       root.MediaPostgres,
		mediaOutbox:   pgmedia.NewOutboxRepository(root.MediaPostgres),
		assetTx:       assetTx,
		jobsFinalizer: jobsFinalizer,
	}, nil
}

// newStockSourceCache builds the cross-run source cache. It is optional: nil
// reader/writer means every download is fresh (no dedup), which is honest at
// the bundle boundary rather than a silently empty cache.
func newStockSourceCache(root *ComposeRoot, log *zap.Logger) (stockpipeline.SourceCacheReader, stockpipeline.SourceCacheWriter) {
	if root.CacheDB == nil || root.CacheDB.DB == nil {
		log.Info("WireStockPipeline: source cache disabled (cache plane unavailable)")
		return nil, nil
	}
	cacheRepo := stocksourcecache.NewRepository(root.CacheDB.DB)
	log.Info("WireStockPipeline: source cache wired", zap.Bool("cache_enabled", true))
	return cacheRepo, cacheRepo
}

// newStockBatchRepository builds the durable batch state (Fase 2).
func newStockBatchRepository(db *sql.DB, log *zap.Logger) stockpipeline.StockBatchRepository {
	if db == nil {
		return nil
	}
	log.Info("WireStockPipeline: batch repository wired")
	return stockbatches.NewRepository(db)
}

// newStockMetadataUpdater resolves the enrichment write plane through the
// canonical media committer. The typed assertion keeps the narrow-port
// discipline (godlike/06): enrichment may not reach into the committer's
// concrete surface, only the one method it needs.
func newStockMetadataUpdater(root *ComposeRoot, log *zap.Logger) (stockenrich.AssetMetadataUpdater, error) {
	if root.Outbox == nil || root.Outbox.EventsRepo == nil {
		return nil, nil
	}
	type metadataUpdater interface {
		UpdateAssetMetadata(ctx context.Context, assetID, metadataJSON string) error
	}
	w, err := newCanonicalAssetCommitter(root.MediaPostgres, log)
	if err != nil {
		return nil, fmt.Errorf("WireStockPipeline: canonical media writer: %w", err)
	}
	if w == nil {
		return nil, nil
	}
	if mu, ok := w.(metadataUpdater); ok {
		return mu, nil
	}
	return nil, nil
}

// newStockPublisherPort wraps the concrete Drive publisher so the application
// layer stays free of internal/platform/drive imports.
func newStockPublisherPort(root *ComposeRoot, log *zap.Logger) finalization.PublisherPort {
	if root.Drive == nil || root.Drive.Publisher == nil {
		return nil
	}
	return drive.NewArtifactPublisherAdapter(root.Drive.Publisher, log)
}

// newStockSourceProbe builds the identity-cached duration probe. Retries and
// multi-step fan-outs re-probe the same staged source within a job, and the
// timing snapshot charges stock.duration_probe 4.1h/week at a 25.7s average
// per ffprobe on multi-GB sources. Only successes are cached; failures stay
// transient at the underlying ffprobe.
func newStockSourceProbe(resolved stockPipelineRoot, log *zap.Logger, executor *rustexec.Executor) *render.CachedSourceDurationProbe {
	return render.NewCachedSourceDurationProbe(
		render.NewFFProbeSourceDurationProbe(rustexec.NewConfiguredVideoProcessorWithExecutor(
			executor,
			resolved.MediaCfg.Policy,
			resolved.MediaCfg.Profile,
			log,
		)),
	)
}

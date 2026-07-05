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

	appacq "github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	jobsfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"go.uber.org/zap"
)

// WireStockPipeline constructs every stock pipeline dependency from the
// ComposeRoot and routes them through BuildStockBundle. Returns a fully
// populated *StockPipelineWiring on success, or (nil, error) when a
// required dep is missing (godlike/07 fail-closed).
//
// Dep construction sequence:
//   - DB: root.DB.DB (embedded *sql.DB from *storage.SQLiteDB)
//   - Cutter: render.NewFFmpegCutter(cfg.External.FfmpegPath)
//   - Renderer: render.NewFFmpegRenderer(cfg.External.FfmpegPath, nil)
//   - ChannelLister + SourceStager Fetch: downloader.NewYTDLP(cfg)
//   - SourceStager: WireAcquisitionStager with real yt-dlp Fetch closure
//   - Finalizer: jobsfinalizer.New (Publisher+Finalizer paired → gate passes)
//
// Returns (nil, nil) when StockPipelineEnabled is false — the caller
// treats nil wiring as "route not mounted" (no error, no registration).
func WireStockPipeline(cfg *config.Config, log *zap.Logger, root *ComposeRoot) (*StockPipelineWiring, error) {
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
	ffmpegPath := cfg.External.FfmpegPath

	// DB: extract *sql.DB from typed *storage.SQLiteDB handle.
	stockDB := (*sql.DB)(nil)
	if root.DB != nil {
		stockDB = root.DB.DB
	}

	// Cutter + Renderer (nil-safe: empty string → "ffmpeg").
	stockCutter := render.NewFFmpegCutter(ffmpegPath, log)
	stockRenderer := render.NewFFmpegRenderer(ffmpegPath, nil, log)

	// ChannelLister + SourceStager: share the same yt-dlp downloader.
	// *YTDLPDownloader satisfies ChannelLister (compile-time pin in ports.go).
	ytdlp := downloader.NewYTDLP(cfg)
	stockChannelLister := ytdlp

	// SourceStager: wire a real yt-dlp Fetch closure so Prepare
	// downloads source videos via yt-dlp subprocess. The closure
	// maps appacq.PrepareRequest → downloader.DownloadRequest,
	// then resolves the yt-dlp output file and renames it to the
	// canonical dstPath the FilesystemStager expects.
	ytdlpFetch := func(ctx context.Context, req appacq.PrepareRequest, dstPath string, onWireSHA256 func(string)) error {
		dlReq := &downloader.DownloadRequest{
			URL:        req.Source.URL,
			OutputPath: dstPath,
			Timeout:    req.Timeout,
			UseCookies: true,
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
		assetTx := assetfinalizer.NewAssetTxFinalizer(log)
		stockFinalizer = jobsfinalizer.New(stockDB, root.Outbox.EventsRepo, assetTx, log)
	} else {
		log.Warn("WireStockPipeline: Finalizer not constructed (godlike/07: one or more required deps nil — stockDB, root.Outbox, or root.Outbox.EventsRepo). If Publisher is also non-nil, the symmetric gate will fire ErrStockProductionJobFinalizerMissing.",
			zap.Bool("stockDB_nil", stockDB == nil),
			zap.Bool("root_Outbox_nil", root.Outbox == nil),
			zap.Bool("EventsRepo_nil", root.Outbox == nil || root.Outbox.EventsRepo == nil),
		)
	}

	return BuildStockBundle(StockBundleDeps{
		Cfg:                  cfg,
		Log:                  log,
		DB:                   stockDB,
		Publisher:            root.Drive.Publisher,
		Finalizer:            stockFinalizer,
		SourceStager:         stockSourceStager,
		ClipsRepo:            root.Repos.ClipsRepo,
		AssetIndex:           root.Search.AssetIndexService,
		Dispatcher:           root.Outbox.Dispatcher,
		Cutter:               stockCutter,
		Renderer:             stockRenderer,
		Jobs:                 root.Jobs.Service,
		ChannelLister:        stockChannelLister,
		StockPipelineEnabled: func() bool { return cfg.Features.StockPipelineEnabled },
	})
}

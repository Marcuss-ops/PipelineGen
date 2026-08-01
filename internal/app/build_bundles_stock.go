// Package app — stock pipeline composition root
// (PR-STOCK-ATLASTORCH-DISPATCH commit-2, July 2026).
//
// godlike/07 fail-fast-at-composition: the asymmetric gate
// (publisher≠nil + finalizer≠nil = production; publisher=nil +
// finalizer=nil = backcompat/test; asymmetric = ErrStockProduction*)
// runs BEFORE stockpipeline.NewService so wiring gaps surface at
// startup, not at first /run (orchestrator.go:478/480 mirrors this
// gate but with harder-to-diagnose late-binding context).
//
// godlike/06 SSOT (one canonical owner per fact):
//
//   - The 2 typed sentinels live ONLY in stockpipeline/upload_orchestration.go
//     (ErrStockProductionJobFinalizerMissing + ErrStockProductionArtifactPrepMissing).
//   - The wiring.StockPipelineWiring struct lives in bundle_types.go
//     (Module api.Module + Service *stockpipeline.Service).
//   - The canonical StockUseCase constructor lives in
//     stockpipeline/usecase.go::NewStockUseCase (returns *stockpipeline.StockUseCase,
//     which is what api/assets/stock/module.go::Build expects).
//   - The API Descriptor lives in api/assets/stock/module.go::StockDescriptor.
//
// The HTTP projection is part of this bundle. It is constructed from the
// same use case as the worker registration so the route cannot advertise a
// capability backed by a different service graph.
//
// This file hosts the BuildStockBundle orchestrator. The per-sub-domain
// wiring lives in sibling files of this package:
//
//   - build_stock_deps.go      — StockBundleDeps + the 8 sub-bundle Deps
//     structs + validateStockSymmetricGate.
//   - build_stock_adapters.go  — chooseDriveReader + stockDriveReaderAdapter
//   - stockAssetIndexAdapter (import-boundary shims).
//   - build_stock_enrichment.go — wireStockEnrichment (Gate 3b, PR-011A/B/C
//     RLM/LLM enrichment pass).
//   - build_stock_batches.go   — buildStockBatchModule (Gate 5, stock batch
//     coordinator + /stock-batches module).
//
// The BuildStockBundle body must keep zero goroutine spawns (freeze test
// TestComposition_NoGoroutinesSpawned_FrozenSiteCount) — helpers in the
// sibling files are deliberately lowercase (not Build\w+Bundle) so they
// stay out of the freeze table.
package app

import (
	"fmt"

	stockapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/stock"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	appmetrics "github.com/Marcuss-ops/PipelineGen/internal/application/processmetrics"
	sqliteprocessmetrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/processmetrics"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildStockBundle assembles the stock video pipeline composition root:
//
//  1. validateStockSymmetricGate (godlike/07 fail-fast at composition time).
//     1b. SQLite mandatory in production (Fase 2).
//  2. stockpipeline.NewService (Deps{Publisher, Finalizer} both threaded).
//  3. stockpipeline.NewStockUseCase (ServiceRunner + narrowed jobsEnqueuer).
//  4. stockapi.Build (API Descriptor with EnabledFunc closure).
//
// Returns a fully populated *wiring.StockPipelineWiring (Module + Service).
// The HTTP Handler stays internal to stockapi.Build per Block
// C1-Step 6 — no caller reads it outside the API module's
// RegisterRoutes closure.
//
// godlike/06 SSOT (cross-package structural conformance):
//
//   - *stockpipeline.Service satisfies ServiceRunner structurally
//     (assert: `var _ ServiceRunner = (*Service)(nil)` at the
//     bottom of stockpipeline/usecase.go).
//   - *appjobs.Service satisfies stockpipeline.jobsEnqueuer structurally
//     (the narrowed interface requires only Enqueue — *appjobs.Service
//     has Enqueue per `var _ job.Service = (*Service)(nil)` at
//     internal/application/jobs/service.go). No adapter shim required —
//     mirrors voiceoverjobs.FanoutDeps in build_bundles_voiceover.go's
//     `voiceoverjobs.NewFanoutVoiceoversUseCase(voiceoverjobs.FanoutDeps{Enqueuer: jobs.Service})`
//     precedent.
//
// godlike/07 minimum-blast-radius: BuildStockBundle is the SINGLE
// canonical site that builds a *stockpipeline.Service — composition
// roots that previously called stockpipeline.NewService inline MUST
// refunnel through this entry point so the symmetric gate cannot be
// bypassed by a future un-wired caller. The pre-§-extraction Step 8
// stub at registry_internal_modules.go:215 references this function
// as the canonical builder.
//
// Error wrapping (godlike/07 typed-error contract): every typed sentinel
// returned by the constitutive functions carries the bundle preamble
// (`stock.BuildStockBundle: <surface>: %w` style) so the caller's
// errors.Is walk recovers the canonical typed error AND the diagnostic
// preamble in one probe.
//
// Returns:
//   - *wiring.StockPipelineWiring (nil, non-nil) on success — caller registers
//     the Module via tryRegisterModuleStrict.
//   - (nil, ErrStockProduction*) on asymmetric wiring (gate fires
//     before NewService).
//   - (nil, *typed sentinel from upload_orchestration.go) on missing
//     required dep (NewService rejects Cfg/Log/SourceStager/ClipsRepo/
//     AssetIndex/Dispatcher/Cutter/Renderer/Jobs).
//   - (nil, stockapi.Build error) on missing UseCase / EnabledFunc.
func BuildStockBundle(deps StockBundleDeps) (*wiring.StockPipelineWiring, error) {
	// ── Gate 1: godlike/07 symmetric production pairing ────────
	if err := validateStockSymmetricGate(deps.Delivery.Publisher, deps.Delivery.Finalizer); err != nil {
		return nil, err
	}

	// ── Gate 1b: Fase 2 — SQLite + BatchRepository mandatory in production ───────
	// Production mode is defined by the presence of either Publisher or
	// Finalizer (the pair must be symmetric after Gate 1). Without a DB,
	// batch/group/artifact persistence is impossible — fail closed.
	isProduction := deps.Delivery.Publisher != nil || deps.Delivery.Finalizer != nil
	if isProduction && deps.Runtime.DB == nil {
		return nil, stockpipeline.ErrStockProductionDBMissing
	}
	if isProduction && deps.Acquisition.BatchRepository == nil {
		return nil, stockpipeline.ErrStockProductionBatchRepositoryMissing
	}

	// ── Gate 2: construct the canonical *stockpipeline.Service ───
	metricsRecorder := deps.Runtime.Metrics
	if metricsRecorder == nil && deps.Runtime.DB != nil {
		metricsRepo := sqliteprocessmetrics.NewSQLiteRepository(deps.Runtime.DB)
		metricsRecorder = appmetrics.NewRecorder(sqliteprocessmetrics.NewApplicationRepository(metricsRepo))
	}
	svc, err := stockpipeline.NewService(stockpipeline.Deps{
		Runtime: stockpipeline.RuntimeDeps{
			Cfg:        stockRuntimeConfig(deps.Runtime.Cfg),
			Log:        deps.Runtime.Log,
			JobCreator: deps.Runtime.JobCreator,
			StepStore:  deps.Runtime.StepStore,
			Metrics:    metricsRecorder,
		},
		Storage: stockpipeline.StorageDeps{
			ClipsRepo:       deps.Acquisition.ClipsRepo,
			AssetIndex:      &stockAssetIndexAdapter{inner: deps.Acquisition.AssetIndex},
			Dispatcher:      deps.Acquisition.Dispatcher,
			BatchRepository: deps.Acquisition.BatchRepository,
		},
		Media: stockpipeline.MediaDeps{
			Cutter:   deps.Media.Cutter,
			Renderer: deps.Media.Renderer,
		},
		Execution: stockpipeline.ExecutionDeps{
			Jobs:          deps.Orchestration.Jobs,
			SourceStager:  deps.Acquisition.SourceStager,
			ChannelLister: deps.Orchestration.ChannelLister,
		},
		SourceCache: stockpipeline.SourceCacheDeps{
			Reader:  deps.SourceCache.Reader,
			Writer:  deps.SourceCache.Writer,
			LocalFS: deps.SourceCache.LocalFS,
		},
		Delivery: stockpipeline.DeliveryDeps{
			Publisher:     deps.Delivery.Publisher,
			PublisherPort: deps.Delivery.PublisherPort,
			FolderCreator: deps.Orchestration.FolderCreator,
			DriveReader:   chooseDriveReader(deps.Acquisition),
			Finalizer:     deps.Delivery.Finalizer,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockpipeline.NewService: %w", err)
	}

	// ── Gate 3: construct the canonical stockpipeline.StockUseCase ─
	// godlike/06 structural-conformance: *stockpipeline.Service
	// satisfies ServiceRunner; *appjobs.Service satisfies jobsEnqueuer.
	// No adapter shim required — mirrors voiceoverjobs.FanoutDeps.
	useCase := stockpipeline.NewStockUseCase(svc, deps.Orchestration.Jobs, deps.Runtime.Log)

	// ── Gate 3b: PR-011A + PR-011B — wire the stock RLM/LLM enrichment handler ─
	if err := wireStockEnrichment(deps); err != nil {
		return nil, err
	}

	// ── Gate 4: compose the canonical API Descriptor ─────────────
	sd, err := stockapi.Build(stockapi.Dependencies{
		UseCase:     useCase,
		EnabledFunc: deps.Feature.StockPipelineEnabled,
		Logger:      deps.Runtime.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build: %w", err)
	}
	typed, ok := sd.(*stockapi.StockDescriptor)
	if !ok || typed == nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build returned unexpected descriptor type %T (want *stockapi.StockDescriptor)", sd)
	}

	// ── Gate 5: construct the stock batch coordinator + /stock-batches module ─
	batchModule, err := buildStockBatchModule(deps, svc)
	if err != nil {
		return nil, err
	}

	return &wiring.StockPipelineWiring{
		Module:      typed.Module,
		BatchModule: batchModule,
		Service:     svc,
	}, nil
}

func stockRuntimeConfig(cfg *config.Config) *stockpipeline.RuntimeConfig {
	if cfg == nil {
		return nil
	}
	v := cfg.Video.WithDefaults()
	return &stockpipeline.RuntimeConfig{
		WorkDir: cfg.Storage.TempPath(), ClipDurationSec: v.ClipDuration,
		ChunkDurationSec: v.ChunkDuration, MaxResults: v.MaxClipsPerSource,
		PolicyVersion: "v1",
	}
}

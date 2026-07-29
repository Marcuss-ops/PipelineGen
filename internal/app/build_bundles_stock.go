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
package app

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"go.uber.org/zap"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	stockapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/stock"
	stockbatches "github.com/Marcuss-ops/PipelineGen/internal/api/assets/stockbatches"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/enrichment"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockplan"
	stocksteps "github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	assetindex "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// StockBundleDeps is the typed narrow input to BuildStockBundle (PR-2 of
// PR-STOCK-ATLASTORCH-DISPATCH).
//
// godlike/06 SSOT: the Deps struct mirrors stockpipeline.Deps
// field-for-field where the fields live canonically (Publisher + Finalizer
// are top-level fields at service_types.go). The 3 flat-typed
// sub-Deps (`SourceStager`, `Cutter`, `Renderer`) collapse StorageDeps +
// MediaDeps into the bundle deps shape so the caller's literal is
// flat (calls adding a typed field never need to wrap into the
// stockpipeline.Deps sub-struct). ChannelLister is optional per
// §F.1 governance — when nil, query.go's resolveQuery fails-closed at
// first search.
//
// PR-NEST-FLAT-DEPS-STOCK (July 2026): the previous flat shape had
// 14 mandatory fields, tripping the `max_struct_deps=8` archcheck
// gate (warn-severity struct_deps violation). The struct now nests
// the 14 fields into 7 purpose-grouped sub-bundles (each ≤4 fields,
// all ≤8):
//
//   - Runtime         (3): Cfg, Log, DB — runtime environment.
//   - Delivery        (2): Publisher, Finalizer — the production pair.
//   - Acquisition     (4): SourceStager, ClipsRepo, AssetIndex,
//     Dispatcher — the storage + dispatch layer.
//   - Media           (2): Cutter, Renderer — the ffmpeg-mediated
//     media processing layer.
//   - Orchestration   (2): Jobs, ChannelLister — the dispatcher-side
//     control layer.
//   - Feature         (1): StockPipelineEnabled — the capability
//     gate closure.
//   - Enrichment      (3): EnrichmentLLMClient, EnrichmentEnabled,
//     EnrichmentEmitter — the PR-011A/B/C RLM/LLM
//     enrichment pass surface.
//
// StockBundleDeps itself carries 7 sub-bundle fields → 7 fields, well
// below the 8-field cap. The nesting follows the canonical godlike/06
// SSOT pattern established by PR-NEST-FLAT-DEPS-ARLIST
// (build_bundles_artlist.go::ServiceDeps{ServicePorts + ServiceDependencies}).
//
// Mandatory fields return an error when BuildStockBundle is called with
// nil; optional fields fall through to the existing type's nil-tolerance
// (Publisher + Finalizer + DB + ChannelLister are optional per
// stockpipeline.NewService's lenient gate — the symmetric gate above
// adds the load-bearing pairing check on Publisher/DepositFinalizer).
type StockBundleDeps struct {
	Runtime       StockRuntimeDeps
	Delivery      StockDeliveryDeps
	Acquisition   StockAcquisitionDeps
	Media         StockMediaDeps
	Orchestration StockOrchestrationDeps
	Feature       StockFeatureGate
	Enrichment    StockEnrichmentDeps
	SourceCache   StockSourceCacheDeps
}

// StockSourceCacheDeps groups the cross-run source download cache
// ports. Reader and Writer are OPTIONAL — nil means no cache
// (every download is fresh). LocalFS is the Pattern 0 typed port
// (PR-REFACTOR-P0-IO-BINDER) the application layer uses to read,
// write, and stat cached files; it is injected at composition time
// so the application layer never calls os.* directly.
// Field count: 3.
type StockSourceCacheDeps struct {
	Reader  stockpipeline.SourceCacheReader
	Writer  stockpipeline.SourceCacheWriter
	LocalFS stockpipeline.LocalFSPort
}

// StockRuntimeDeps groups the runtime environment the stock bundle
// needs (Cfg, Log, DB). Field count: 3.
type StockRuntimeDeps struct {
	Cfg        *config.Config
	Log        *zap.Logger
	DB         *sql.DB // optional (nil → in-memory)
	JobCreator stockpipeline.JobCreator
	StepStore  stocksteps.Store
}

// StockDeliveryDeps groups the asymmetric production-pair surface
// (Publisher, PublisherPort, Finalizer). The StockSymmetricGate validates
// this pair is both-nil or both-non-nil. PublisherPort is the pre-constructed
// finalization.PublisherPort adapter (drive.NewArtifactPublisherAdapter)
// created at the composition root so the application layer stays free of
// internal/infrastructure/drive imports. Field count: 3.
type StockDeliveryDeps struct {
	Publisher     delivery.Publisher         // optional (nil → backcompat; finalizer nil → OK)
	PublisherPort finalization.PublisherPort // optional (nil → backcompat; constructed from Publisher at composition root)
	Finalizer     finalization.JobFinalizer  // optional (nil → backcompat OR asymmetric gate fires when Publisher non-nil)
}

// stockConcreteDriveReader is the raw interface matched by the concrete
// drive types (*drive.Uploader, drive.Reader). Defined in the composition
// root so the application layer's DriveReaderPort can stay free of
// internal/infrastructure/drive imports. chooseDriveReader wraps the
// concrete with a stockDriveReaderAdapter that converts
// drive.DriveFileInfo → stockpipeline.DriveFileInfo.
type stockConcreteDriveReader interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	ListFiles(ctx context.Context, parentID string) ([]drive.DriveFileInfo, error)
}

// StockAcquisitionDeps groups the storage + dispatch layer the stock
// pipeline reads from (SourceStager, ClipsRepo, AssetIndex,
// Dispatcher, BatchRepository, DriveDownloader). Field count: 6.
type StockAcquisitionDeps struct {
	SourceStager    acquisition.SourceStager           // required
	ClipsRepo       *sqassets.ClipsRepository          // required
	AssetIndex      *assetindex.Service                // required
	Dispatcher      *outbox.Dispatcher                 // required
	BatchRepository stockpipeline.StockBatchRepository // optional; required in production via DB gate
	// DriveDownloader enables staging of Google Drive source URLs.
	// Optional — nil means Drive URLs fail with a typed error (no
	// silent fallback to yt-dlp). Wraps a concrete drive type.
	//
	// Deprecated: DriveReader is the canonical field going forward.
	// DriveDownloader is still accepted for backward compatibility.
	DriveDownloader stockConcreteDriveReader
	// DriveReader enables staging of Google Drive source URLs,
	// including folder expansion. Optional — nil means Drive URLs
	// fail with a typed error.
	DriveReader stockConcreteDriveReader
}

// StockMediaDeps groups the ffmpeg-mediated media processing layer
// (Cutter, Renderer). Field count: 2.
type StockMediaDeps struct {
	Cutter   stockpipeline.VideoCutter   // required
	Renderer stockpipeline.StockRenderer // required
}

// StockOrchestrationDeps groups the dispatcher-side control layer
// (Jobs, ChannelLister). ChannelLister is optional — when nil,
// query.go's resolveQuery fails-closed at first search. Field
// count: 2.
type StockOrchestrationDeps struct {
	Jobs          *appjobs.Service                 // required
	ChannelLister stockpipeline.ChannelLister      // optional
	FolderCreator stockpipeline.StockFolderCreator // optional
}

// StockFeatureGate is the canonical closure that decides whether
// /api/stock-pipeline/* routes are mounted.
// Field count: 1.
type StockFeatureGate struct {
	StockPipelineEnabled func() bool
}

// StockEnrichmentDeps groups the PR-011A/B/C RLM/LLM enrichment pass
// surface (LLMClient, Enabled closure, Emitter). All fields are
// OPTIONAL (nil = enrichment disabled, godlike/07 fail-closed).
// When EnrichmentEnabled() returns true AND the LLMClient is
// resolved (override > real adapter > stub), BuildStockBundle wires
// the canonical EnrichmentHandler
// (stockenrich.EnrichmentHandler) and registers it on the jobs
// dispatcher for appjobs.TypeMediaStockRLMEnrich
// ("media.stock_rlm_enrich"). Field count: 3.
type StockEnrichmentDeps struct {
	// EnrichmentLLMClient is the canonical Pattern-0 typed port.
	// PR-011A passes the stockenrich.StubEnrichmentLLMClient
	// (returns ErrEnrichmentLLMUnavailable, drives the worker
	// retry path end-to-end). PR-011B replaces the stub with a
	// real ollama-backed adapter.
	EnrichmentLLMClient stockenrich.EnrichmentLLMClient

	// EnrichmentEnabled is the canonical cfg-gated closure
	// (mirrors StockPipelineEnabled). When nil or returning
	// false, no handler is registered.
	EnrichmentEnabled func() bool

	// EnrichmentEmitter is the canonical Pattern-0 typed port for
	// the asset.published v1 outbox event (PR-011C). OPTIONAL
	// (nil = disabled-mode wiring; the handler's godlike/07
	// nil-tolerance logs a Warn and skips the emit step).
	EnrichmentEmitter stockenrich.AssetPublishedEmitter
}

// validateStockSymmetricGate enforces the godlike/07 production pairing
// of Publisher + JobFinalizer. The 4 states:
//
//	publisher=nil + finalizer=nil → nil (test/backcompat mode)
//	publisher≠nil + finalizer≠nil → nil (production mode)
//	publisher≠nil + finalizer=nil → ErrStockProductionJobFinalizerMissing
//	publisher=nil + finalizer≠nil → ErrStockProductionArtifactPrepMissing
//
// Pre-gate (before NewService): composition-time typed error surfaces
// loudly instead of silently passing through the orchestrator's
// RunResilient gate at orchestrator.go:478/480 (which fires AFTER
// source staging + cut dispatch — much later in the pipeline, harder
// to diagnose from incident reports).
//
// Exposed at package scope as unexported helper so build_bundles_stock_test.go
// can drive TDD coverage without standing up all 14 StockBundleDeps fields.
func validateStockSymmetricGate(publisher delivery.Publisher, finalizer finalization.JobFinalizer) error {
	if publisher != nil && finalizer == nil {
		return stockpipeline.ErrStockProductionJobFinalizerMissing
	}
	if publisher == nil && finalizer != nil {
		return stockpipeline.ErrStockProductionArtifactPrepMissing
	}
	return nil
}

// chooseDriveReader returns the canonical DriveReaderPort to wire
// into the stock pipeline. It prefers the explicit DriveReader field;
// if nil, it falls back to DriveDownloader for backward compatibility.
// Both are adapted through stockDriveReaderAdapter which converts the
// concrete drive.DriveFileInfo to the application-layer
// stockpipeline.DriveFileInfo (godlike/06 import-boundary discipline).
func chooseDriveReader(acq StockAcquisitionDeps) stockpipeline.DriveReaderPort {
	raw := acq.DriveReader
	if raw == nil {
		raw = acq.DriveDownloader
	}
	if raw == nil {
		return nil
	}
	return &stockDriveReaderAdapter{inner: raw}
}

// stockDriveReaderAdapter wraps a stockConcreteDriveReader and adapts
// its ListFiles return type from []drive.DriveFileInfo to
// []stockpipeline.DriveFileInfo, keeping the application layer free
// of internal/infrastructure/drive imports.
type stockDriveReaderAdapter struct {
	inner stockConcreteDriveReader
}

var _ stockpipeline.DriveReaderPort = (*stockDriveReaderAdapter)(nil)

func (a *stockDriveReaderAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	return a.inner.DownloadFile(ctx, fileID)
}

func (a *stockDriveReaderAdapter) ListFiles(ctx context.Context, parentID string) ([]stockpipeline.DriveFileInfo, error) {
	raw, err := a.inner.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]stockpipeline.DriveFileInfo, len(raw))
	for i, f := range raw {
		out[i] = stockpipeline.DriveFileInfo{
			ID:       f.ID,
			MimeType: f.MimeType,
		}
	}
	return out, nil
}

// stockAssetIndexAdapter wraps *assetindex.Service and adapts its Upsert
// method from *assetindex.AssetRecord to *stockpipeline.StockAssetUpsertRecord,
// keeping the application layer free of internal/infrastructure/database/assetindex
// imports (godlike/06 import-boundary discipline).
type stockAssetIndexAdapter struct {
	inner *assetindex.Service
}

func (a *stockAssetIndexAdapter) Upsert(ctx context.Context, rec *stockpipeline.StockAssetUpsertRecord) error {
	return a.inner.Upsert(ctx, &assetindex.AssetRecord{AssetID: rec.AssetID})
}

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
	svc, err := stockpipeline.NewService(stockpipeline.Deps{
		Runtime: stockpipeline.RuntimeDeps{
			Cfg:        stockRuntimeConfig(deps.Runtime.Cfg),
			Log:        deps.Runtime.Log,
			JobCreator: deps.Runtime.JobCreator,
			StepStore:  deps.Runtime.StepStore,
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
	// godlike/07 fail-closed composition: enrichment is OPTIONAL
	// (nil-LLMClient OR nil-EnabledFunc OR EnabledFunc()==false = no
	// handler registered; the worker pool cannot dequeue
	// media.stock_rlm_enrich jobs and the registry entry sits unused).
	// No silent-success path: a misconfigured production deployment
	// (EnabledFunc()==true but LLMClient==nil AND cfg has no
	// fallback model) surfaces as a typed error at composition time.
	//
	// PR-011B (July 2026): the LLM client resolution order is:
	//   1. deps.EnrichmentLLMClient (test override / future dev override)
	//   2. real ollama adapter when cfg.External.ParseArenaLLM is non-empty
	//   3. real ollama adapter fallback to cfg.External.OllamaModel
	//   4. StubEnrichmentLLMClient (PR-011A default) when BOTH empty
	//
	// The model precedence (ParseArenaLLM > OllamaModel) is canonical
	// for the stock RLM/LLM enrichment pass per AGENTS.md Pattern 0
	// + godlike/06 SSOT (one canonical owner per fact: the
	// resolution order lives ONLY here in the composition root).
	if deps.Enrichment.EnrichmentEnabled != nil && deps.Enrichment.EnrichmentEnabled() {
		// Step 1: resolve the LLM client (override > real adapter > stub).
		llmClient := deps.Enrichment.EnrichmentLLMClient
		if llmClient == nil && deps.Runtime.Cfg != nil {
			modelName := strings.TrimSpace(deps.Runtime.Cfg.External.ParseArenaLLM)
			if modelName == "" {
				modelName = strings.TrimSpace(deps.Runtime.Cfg.External.OllamaModel)
			}
			if modelName != "" {
				// Construct the real ollama-backed adapter. The
				// ollama client's default model is OllamaModel
				// (canonical cfg-default); the per-capability
				// modelName override (typically ParseArenaLLM)
				// is passed to the adapter so Enrich() threads
				// it via options["model"] on every Chat call.
				ollamaCli := ollamaclient.NewClient(
					deps.Runtime.Cfg.External.OllamaURL,
					deps.Runtime.Cfg.External.OllamaModel,
					deps.Runtime.Cfg.External.OllamaTimeoutSeconds,
				)
				realAdapter, realErr := stockenrich.NewOllamaEnrichmentLLMClient(ollamaCli, modelName, deps.Runtime.Cfg.External.EnrichmentPromptVersion)
				if realErr != nil {
					return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.NewOllamaEnrichmentLLMClient: %w", realErr)
				}
				llmClient = realAdapter
				deps.Runtime.Log.Info("stock.BuildStockBundle: enrichment wired with real ollama adapter",
					zap.String("model", modelName),
					zap.String("ollama_url", deps.Runtime.Cfg.External.OllamaURL),
				)
			} else {
				// godlike/07 minimum-blast-radius: when neither
				// ParseArenaLLM nor OllamaModel is configured,
				// fall back to the stub so the worker retry path
				// is still exercised end-to-end (no silent
				// success — the stub returns
				// ErrEnrichmentLLMUnavailable verbatim).
				llmClient = stockenrich.NewStubEnrichmentLLMClient("stub:enrichment-unavailable")
				deps.Runtime.Log.Warn("stock.BuildStockBundle: enrichment using stub (no model configured; set ParseArenaLLM or OllamaModel to wire the real adapter)")
			}
		}

		if llmClient == nil {
			deps.Runtime.Log.Warn("stock.BuildStockBundle: enrichment enabled but no LLM client resolved (set EnrichmentLLMClient or configure ParseArenaLLM/OllamaModel)")
		} else {
			assetRepo, repoErr := stockenrich.NewSQLiteAssetRepository(deps.Runtime.DB)
			if repoErr != nil {
				return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.NewSQLiteAssetRepository: %w", repoErr)
			}

			// PR-011C follow-up (July 2026): wire the production
			// outbox-dispatcher-backed emitter. The emitter opens
			// a fresh SQL tx + calls outboxevents.Repository.Enqueue
			// per the canonical pattern. When deps.Runtime.DB is nil, fall
			// back to the nil-emitter (handler's godlike/07
			// nil-tolerance logs a Warn + skips the emit step)
			// — this preserves the PR-011C composition-root
			// disabled-mode wiring for tests / dev environments
			// where SQLite is not available.
			//
			// godlike/07 minimum-blast-radius: the emitter is
			// OPTIONAL (nil is allowed). Production deployments
			// that enable enrichment MUST wire a real DB +
			// real emitter (no silent-success on the emit path).
			var emitter stockenrich.AssetPublishedEmitter
			if deps.Enrichment.EnrichmentEmitter != nil {
				emitter = deps.Enrichment.EnrichmentEmitter
				deps.Runtime.Log.Info("stock.BuildStockBundle: enrichment using injected AssetPublishedEmitter")
			} else if deps.Runtime.DB != nil {
				emitter, repoErr = stockenrich.NewOutboxBackedAssetPublishedEmitter(deps.Runtime.DB, deps.Runtime.Log)
				if repoErr != nil {
					return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.NewOutboxBackedAssetPublishedEmitter: %w", repoErr)
				}
				deps.Runtime.Log.Info("stock.BuildStockBundle: enrichment wired with outbox-backed emitter (asset.published v1)")
			} else {
				deps.Runtime.Log.Warn("stock.BuildStockBundle: enrichment nil-emitter (no DB; the handler will skip asset.published v1 emit with a Warn log)")
			}

			enrichHandler, hErr := stockenrich.NewEnrichmentHandler(llmClient, assetRepo, emitter, deps.Runtime.Log)
			if hErr != nil {
				return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.NewEnrichmentHandler: %w", hErr)
			}
			if regErr := enrichHandler.RegisterHandler(deps.Orchestration.Jobs); regErr != nil {
				return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.RegisterHandler: %w", regErr)
			}
		}
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
	var batchModule api.Module
	if deps.Acquisition.BatchRepository != nil {
		coordinator := stockplan.NewCoordinator(stockplan.CoordinatorDeps{
			Repo:     deps.Acquisition.BatchRepository,
			Enqueuer: deps.Orchestration.Jobs,
			Resolver: nil,
			Stager:   svc,
			Log:      deps.Runtime.Log,
		})
		batchDescriptor, batchErr := stockbatches.Build(stockbatches.Dependencies{
			Coordinator: coordinator,
			EnabledFunc: deps.Feature.StockPipelineEnabled,
			Logger:      deps.Runtime.Log,
		})
		if batchErr != nil {
			return nil, fmt.Errorf("stock.BuildStockBundle: stockbatches.Build: %w", batchErr)
		}
		if d, ok := batchDescriptor.(*stockbatches.StockBatchesDescriptor); ok && d != nil {
			batchModule = d.Module
		}
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

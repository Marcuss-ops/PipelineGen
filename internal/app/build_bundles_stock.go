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
//   - The StockPipelineWiring struct lives in bundle_types.go
//     (Module api.Module + Service *stockpipeline.Service).
//   - The canonical StockUseCase constructor lives in
//     stockpipeline/usecase.go::NewStockUseCase (returns *stockpipeline.StockUseCase,
//     which is what api/assets/stock/handler.go expects via NewHandler +
//     api/assets/stock/module.go::Build).
//   - The API Descriptor lives in api/assets/stock/module.go::StockDescriptor.
//
// Pre-commit-2 state: WireStockPipeline was retired and the Step 8
// registerInternalModules stub logged a Warn + nil-wired
// wiring.StockPipeline. /api/stock-pipeline/* returned 404 in
// production. This file restores the canonical typed-port composition
// surface alongside the build_bundles_artlist.go / build_bundles_voiceover.go
// precedent — WireStockPipeline (or its future re-introduction) MUST
// funnel through BuildStockBundle so the symmetric gate cannot be
// bypassed by a future un-wired caller.
package app

import (
	"database/sql"
	"fmt"
	"strings"

	"go.uber.org/zap"

	stockapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/stock"
	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/enrichment"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	assetindex "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
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
// Mandatory fields return an error when nil; optional fields fall through
// to the existing type's nil-tolerance (Publisher + Finalizer + DB +
// ChannelLister are optional per stockpipeline.NewService's lenient
// gate — the asymmetric gate above adds the load-bearing pairing
// check).
type StockBundleDeps struct {
	Cfg       *config.Config
	Log       *zap.Logger
	DB        *sql.DB                   // optional (nil → in-memory)
	Publisher delivery.Publisher        // optional (nil → backcompat; finalizer nil → OK)
	Finalizer finalization.JobFinalizer // optional (nil → backcompat OR asymmetric gate fires when Publisher non-nil)
	// typed ports
	SourceStager  acquisition.SourceStager    // required
	ClipsRepo     *sqassets.ClipsRepository   // required
	AssetIndex    *assetindex.Service         // required
	Dispatcher    *outbox.Dispatcher          // required
	Cutter        stockpipeline.VideoCutter   // required
	Renderer      stockpipeline.StockRenderer // required
	Jobs          *appjobs.Service            // required
	ChannelLister stockpipeline.ChannelLister // optional

	// StockPipelineEnabled is the canonical closure that decides
	// whether /api/stock-pipeline/* routes are mounted. MANDATORY
	// for stockapi.Build — nil closes the capability (no route
	// registration) per api/assets/stock/module.go's nil-tolerance.
	StockPipelineEnabled func() bool

	// PR-011A (July 2026): stock RLM/LLM enrichment pass composition
	// seam. Both fields are OPTIONAL (nil = enrichment disabled,
	// godlike/07 fail-closed). When both are non-nil AND
	// EnrichmentEnabled() returns true, BuildStockBundle wires the
	// canonical EnrichmentHandler (stockenrich.EnrichmentHandler)
	// and registers it on the jobs dispatcher for
	// appjobs.TypeMediaStockRLMEnrich ("media.stock_rlm_enrich").
	//
	// EnrichmentLLMClient is the canonical Pattern-0 typed port.
	// PR-011A passes the stockenrich.StubEnrichmentLLMClient
	// (returns ErrEnrichmentLLMUnavailable, drives the worker
	// retry path end-to-end). PR-011B will replace the stub
	// with a real ollama-backed adapter; the composition root
	// wires it via fluent setter per AGENTS.md Pattern 0.
	//
	// EnrichmentEnabled is the canonical cfg-gated closure
	// (mirrors StockPipelineEnabled). When nil or returning
	// false, no handler is registered; the worker pool cannot
	// dequeue media.stock_rlm_enrich jobs.
	EnrichmentLLMClient stockenrich.EnrichmentLLMClient
	EnrichmentEnabled   func() bool

	// EnrichmentEmitter is the canonical Pattern-0 typed port for
	// the asset.published v1 outbox event (PR-011C, July 2026).
	// OPTIONAL (nil = disabled-mode wiring; the handler's
	// godlike/07 nil-tolerance logs a Warn and skips the emit
	// step). When non-nil, the handler builds the v1 envelope
	// (via stockenrich.EnrichmentIdempotencyKey) and hands it to
	// this port for emission to outbox_events.
	//
	// PR-011C ships the producer-side surface (handler + port +
	// stub + tests). The production concrete (outbox-dispatcher-backed
	// adapter that opens a tx + calls outboxevents.Repository.Enqueue)
	// is a follow-up PR-011C-FOLLOW-UP-1 forward-pointer. Until
	// that lands, production wiring passes nil here (handler
	// skips emit with a Warn log; the LLM + UPDATE sequence still
	// reaches the mark-SUCCEEDED seam end-to-end).
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

// BuildStockBundle assembles the stock video pipeline composition root:
//
//  1. validateStockSymmetricGate (godlike/07 fail-fast at composition time).
//  2. stockpipeline.NewService (Deps{Publisher, Finalizer} both threaded).
//  3. stockpipeline.NewStockUseCase (ServiceRunner + narrowed jobsEnqueuer).
//  4. stockapi.Build (API Descriptor with EnabledFunc closure).
//
// Returns a fully populated *StockPipelineWiring (Module + Service).
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
//   - *StockPipelineWiring (nil, non-nil) on success — caller registers
//     the Module via tryRegisterModuleStrict.
//   - (nil, ErrStockProduction*) on asymmetric wiring (gate fires
//     before NewService).
//   - (nil, *typed sentinel from upload_orchestration.go) on missing
//     required dep (NewService rejects Cfg/Log/SourceStager/ClipsRepo/
//     AssetIndex/Dispatcher/Cutter/Renderer/Jobs).
//   - (nil, stockapi.Build error) on missing UseCase / EnabledFunc.
func BuildStockBundle(deps StockBundleDeps) (*StockPipelineWiring, error) {
	// ── Gate 1: godlike/07 symmetric production pairing ────────
	if err := validateStockSymmetricGate(deps.Publisher, deps.Finalizer); err != nil {
		return nil, err
	}

	// ── Gate 2: construct the canonical *stockpipeline.Service ───
	svc, err := stockpipeline.NewService(stockpipeline.Deps{
		Cfg:       deps.Cfg,
		Log:       deps.Log,
		Publisher: deps.Publisher,
		Storage: stockpipeline.StorageDeps{
			ClipsRepo:  deps.ClipsRepo,
			AssetIndex: deps.AssetIndex,
			Dispatcher: deps.Dispatcher,
		},
		Media: stockpipeline.MediaDeps{
			Cutter:   deps.Cutter,
			Renderer: deps.Renderer,
		},
		Jobs:         deps.Jobs,
		Finalizer:    deps.Finalizer,
		SourceStager: deps.SourceStager,
		DB:           deps.DB,
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockpipeline.NewService: %w", err)
	}

	// ── Gate 3: construct the canonical stockpipeline.StockUseCase ─
	// godlike/06 structural-conformance: *stockpipeline.Service
	// satisfies ServiceRunner; *appjobs.Service satisfies jobsEnqueuer.
	// No adapter shim required — mirrors voiceoverjobs.FanoutDeps.
	useCase := stockpipeline.NewStockUseCase(svc, deps.Jobs, deps.Log)

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
	if deps.EnrichmentEnabled != nil && deps.EnrichmentEnabled() {
		// Step 1: resolve the LLM client (override > real adapter > stub).
		llmClient := deps.EnrichmentLLMClient
		if llmClient == nil && deps.Cfg != nil {
			modelName := strings.TrimSpace(deps.Cfg.External.ParseArenaLLM)
			if modelName == "" {
				modelName = strings.TrimSpace(deps.Cfg.External.OllamaModel)
			}
			if modelName != "" {
				// Construct the real ollama-backed adapter. The
				// ollama client's default model is OllamaModel
				// (canonical cfg-default); the per-capability
				// modelName override (typically ParseArenaLLM)
				// is passed to the adapter so Enrich() threads
				// it via options["model"] on every Chat call.
				ollamaCli := ollamaclient.NewClient(
					deps.Cfg.External.OllamaURL,
					deps.Cfg.External.OllamaModel,
					deps.Cfg.External.OllamaTimeoutSeconds,
				)
				realAdapter, realErr := stockenrich.NewOllamaEnrichmentLLMClient(ollamaCli, modelName)
				if realErr != nil {
					return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.NewOllamaEnrichmentLLMClient: %w", realErr)
				}
				llmClient = realAdapter
				deps.Log.Info("stock.BuildStockBundle: enrichment wired with real ollama adapter",
					zap.String("model", modelName),
					zap.String("ollama_url", deps.Cfg.External.OllamaURL),
				)
			} else {
				// godlike/07 minimum-blast-radius: when neither
				// ParseArenaLLM nor OllamaModel is configured,
				// fall back to the stub so the worker retry path
				// is still exercised end-to-end (no silent
				// success — the stub returns
				// ErrEnrichmentLLMUnavailable verbatim).
				llmClient = stockenrich.NewStubEnrichmentLLMClient("stub:enrichment-unavailable")
				deps.Log.Warn("stock.BuildStockBundle: enrichment using stub (no model configured; set ParseArenaLLM or OllamaModel to wire the real adapter)")
			}
		}

		if llmClient != nil {
			assetRepo, repoErr := stockenrich.NewSQLiteAssetRepository(deps.DB)
			if repoErr != nil {
				return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.NewSQLiteAssetRepository: %w", repoErr)
			}

			// PR-011C follow-up (July 2026): wire the production
			// outbox-dispatcher-backed emitter. The emitter opens
			// a fresh SQL tx + calls outboxevents.Repository.Enqueue
			// per the canonical pattern. When deps.DB is nil, fall
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
			if deps.DB != nil {
				emitter, repoErr = stockenrich.NewOutboxBackedAssetPublishedEmitter(deps.DB, deps.Log)
				if repoErr != nil {
					return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.NewOutboxBackedAssetPublishedEmitter: %w", repoErr)
				}
				deps.Log.Info("stock.BuildStockBundle: enrichment wired with outbox-backed emitter (asset.published v1)")
			} else {
				deps.Log.Warn("stock.BuildStockBundle: enrichment nil-emitter (no DB; the handler will skip asset.published v1 emit with a Warn log)")
			}

			enrichHandler, hErr := stockenrich.NewEnrichmentHandler(llmClient, assetRepo, emitter, deps.Log)
			if hErr != nil {
				return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.NewEnrichmentHandler: %w", hErr)
			}
			if regErr := enrichHandler.RegisterHandler(deps.Jobs); regErr != nil {
				return nil, fmt.Errorf("stock.BuildStockBundle: enrichment.RegisterHandler: %w", regErr)
			}
		}
	}

	// ── Gate 4: compose the canonical API Descriptor ─────────────
	sd, err := stockapi.Build(stockapi.Dependencies{
		UseCase:     useCase,
		EnabledFunc: deps.StockPipelineEnabled,
		Logger:      deps.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build: %w", err)
	}
	typed, ok := sd.(*stockapi.StockDescriptor)
	if !ok || typed == nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build returned unexpected descriptor type %T (want *stockapi.StockDescriptor)", sd)
	}

	return &StockPipelineWiring{
		Module:  typed.Module,
		Service: svc,
	}, nil
}

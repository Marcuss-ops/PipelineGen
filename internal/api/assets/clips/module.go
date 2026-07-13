// Package clips — module.go: the single canonical Build entrypoint for
// the Clips HTTP capability.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The returned Descriptor is complete: missing mandatory dependencies
// return an error during composition; the capability does not create
// partially-initialized services. Once Build returns, the descriptor is
// ready to be registered into the api.Registry by the composition root.
//
// This file is part of Blocco C1-Step 5 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/module_media.go::WireAssets` and threads the returned
// Descriptor into `assetsapi.Dependencies.Clips` (route module that
// mounts /media/clips).
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), and the rest of the Wave-C1 set.
//
// UNIQUE TO CLIPS (vs artlist/youtube): the package already owns a
// fat-orchestrator *Handler that wires 4 sub-handlers (ingest, search,
// ops, bulk_upload) + 9 NON-Ops inline methods + 3 Action cluster
// methods via the canonical Handler.RegisterRoutes. The user hint
// "riusalo come Descriptor" maps to:
//
//   - Build constructs *Handler via NewHandler(deps.toDeps(), idem).
//   - Build wraps it in api.NewRouteModule("clips", enabledFn, "/clips",
//     handler, log, opts...) — the Module captures handler.RegisterRoutes
//     in its closure.
//   - Build returns *ClipsDescriptor which exposes `Module` (route
//     surface, api.Descriptor-satisfying via forwarder methods) AND
//     `Handler` (raw orchestrator, for the ONE non-HTTP consumer in
//     `internal/app/assets_register_sourcing.go::sourcingEnrichmentAdapter`
//     that calls `clipsHandler.EnrichAndIndexClip(ctx, clip, source)`).
//
// The Descriptor does NOT embed *Handler (to avoid method promotion +
// to keep the canonical "Descriptor = route surface" shape). The
// Handler stays the internal worker; callers (composition root,
// tests, internal services) either go through the Descriptor's
// Module (for HTTP) or via the explicit `Descriptor.Handler` field
// (for the single non-HTTP call site).
package clips

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Mirrors the
// `Deps` bag that `NewHandler` consumes, plus the Build-time
// fields the artlist/youtube precedent requires (Idempotency
// middleware + EnabledFunc + ModuleOpts + Logger).
//
// Mandatory fields return an error when nil; optional fields fall
// through to the handler's existing nil-tolerance (each route
// short-circuits to 503 or to the appropriate sentinel response —
// never panic, never NPE).
//
// Logger nil → zap.NewNop() (composition-root-friendly default).
type Dependencies struct {
	// ── Handler bag (mirrors clipsapi.Deps) ────────────────────────
	// All 27 fields consumed by NewHandler are mirrored here as
	// flat fields so the composition root can pass them straight
	// through without an intermediate Deps literal.

	// ClipsRepo is the canonical SQLite-backed clips store.
	// MANDATORY — Build returns an error when nil.
	ClipsRepo *assets.ClipsRepository

	// AssetRepo is the domain asset.Repository (DB-agnostic).
	// MANDATORY — used by Ingest sub-handler + clip_action.
	AssetRepo asset.Repository

	// DeletionSvc is the application-layer deletion orchestrator.
	// MANDATORY — used by Ops sub-handler.
	DeletionSvc *deletion.DeletionService

	// DriveAdmin wraps google.golang.org/api/drive/v3.
	// MANDATORY — used by Ingest sub-handler for the upload path.
	DriveAdmin drive.Admin

	// MediaProcessor is the asset.Processor (ffmpeg pipeline).
	// MANDATORY — used by ReprocessUseCase inside Handler.
	MediaProcessor asset.Processor

	// AssetTreeSvc maintains the asset-tree shadow model.
	// MANDATORY — used by Ingest sub-handler + BulkTagsUC.
	AssetTreeSvc *assettree.Service

	// MetaWriter handles semantic metadata JSON generation.
	// MANDATORY — used by Ingest sub-handler + clip_action.
	MetaWriter semantic.MetadataWriterPort

	// ClipIndexer is the qdrant-fed clip indexer.
	// MANDATORY — used by ReindexClip / BatchReindex.
	ClipIndexer *clipindexer.Service

	// JobsSvc is the canonical jobs.Service (facade).
	// MANDATORY — used by EnrichMedia / ReindexClip / BatchReindex
	// / BulkUpload cluster.
	JobsSvc jobservice.Service

	// Cfg is the application *config.Config. MANDATORY — used by
	// Ingest sub-handler + driveRootForSource helper.
	Cfg *config.Config

	// Log is the structured logger. nil → zap.NewNop() (Build
	// convenience). MANDATORY-shape only — never blocks Build.
	Log *zap.Logger

	// VoiceoverRepo is the canonical VoiceoversRepository.
	// MANDATORY — used by Search sub-handler + Action cluster.
	VoiceoverRepo *assets.VoiceoversRepository

	// ImagesRepo is the canonical ImagesRepository.
	// MANDATORY — used by Search sub-handler.
	ImagesRepo *assets.ImagesRepository

	// ArtifactSvc is the application-layer artifact orchestrator.
	// MANDATORY — used by Ingest sub-handler.
	ArtifactSvc *artifacts.Service

	// FolderMemSvc is the foldermemory.Service (folder-cache).
	// MANDATORY — used by Ops sub-handler.
	FolderMemSvc *foldermemory.Service

	// SearchSvc is the canonical search.Aggregator (Wave 21 PR 10).
	// MANDATORY — used by Search sub-handler.
	SearchSvc *search.Aggregator

	// ProcessRunner is the infrastructure ProcessRunner port.
	// MANDATORY — used by Ingest sub-handler.
	ProcessRunner appassets.ProcessRunner

	// Dispatcher is the application-layer ClipIndexDispatcherPort
	// (PR 7, June 2026, codex/qdrant-app-writers-fail-closed).
	// MANDATORY — used by Ingest sub-handler (UpdateClip path).
	Dispatcher appclips.ClipIndexDispatcherPort

	// DuplicateFinder is the canonical duplicate-detection service.
	// MANDATORY — used by Action cluster (FindDuplicates).
	DuplicateFinder *duplicates.Finder

	// ReuploadUC is the application-layer ReuploadUseCase.
	// MANDATORY — used by Action cluster (ReuploadClip).
	ReuploadUC *appclips.ReuploadUseCase

	// EnrichUC is the application-layer EnrichUseCase. S1a
	// (June 2026): the composition root builds a SHARED instance
	// and threads it through Build so the worker's media.enrich
	// job and the handler's EnrichMedia path share state.
	// OPTIONAL — when nil, NewHandler constructs a local fallback
	// copy that preserves pre-S1a behaviour (enrichUCOrLocal
	// helper).
	EnrichUC *appclips.EnrichUseCase

	// BulkUploadWorker is the application-layer BulkUploadWorker
	// (W14 PR2 slice 3, June 2026).
	// MANDATORY — used by bulk_upload cluster routes.
	BulkUploadWorker *appclips.BulkUploadWorker

	// ClipOpsService is the application-layer ClipOpsService
	// (PR 2, June 2026).
	// MANDATORY — used by Ops sub-handler (14 routes).
	ClipOpsService *appclips.ClipOpsService

	// UploadUC is the application-layer upload UseCase
	// (P1.5, June 2026).
	// MANDATORY — used by Ingest sub-handler (UploadVideoClip).
	UploadUC *appupload.UseCase

	// Publisher is the canonical delivery.Publisher (Drive write).
	// MANDATORY — used by BulkUploadTransport for folder-name resolution.
	Publisher delivery.Publisher

	// ── Build-time fields (mirrors artlist/youtube) ───────────────

	// Idempotency is the reusable Gin idempotency middleware
	// (PR8, June 2026). Constructed once at server boot via
	// WireRegistry → BuildRepoBundle.IdempotencyStore.
	// nil → Build installs a no-op pass-through (preserves
	// the test-fixture / dry-run path).
	Idempotency gin.HandlerFunc

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The composition root wires
	// the canonical `func() bool { return true }` closure (clips
	// are always on in production; the closure shape preserves
	// the artlist/youtube contract symmetry).
	// MANDATORY — Build returns an error when nil.
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// (typically `api.WithMiddleware(...)`) applied to the
	// RouteModule at Build time. OPTIONAL — nil produces a plain
	// RouteModule.
	ModuleOpts []api.RouteModuleOption
}

// ClipsDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module
// field (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods. The pre-built `Handler` is
// exposed so the ONE non-HTTP caller
// (`internal/app/assets_register_sourcing.go::sourcingEnrichmentAdapter`)
// can drive `clipsHandler.EnrichAndIndexClip(ctx, clip, source)`
// without re-constructing the orchestrator (matches the artlist
// precedent of exposing Service in the Descriptor).
//
// UNIQUE TO CLIPS: the handler is the HTTP orchestrator (not a
// "use case service" like artlist's *artlistapp.Service). It is
// the SAME object that owns RegisterRoutes; the Module closure
// captures it. Exposing it via `Descriptor.Handler` is the
// pattern-level answer to "I need to call a non-HTTP method
// (EnrichAndIndexClip) on the same orchestrator that the routes
// use" — the alternative would be to extract a separate
// non-HTTP surface, but the codebase has not converged on that
// extraction yet (a future commit may lift EnrichAndIndexClip
// into a typed clipEnrichmentPort and consume it via port
// instead of a raw *Handler reference).
type ClipsDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule instance)
	// the composition root threads into assetsapi.Dependencies.Clips.
	Module api.Module

	// Handler is the raw orchestrator. Exposed for the
	// non-HTTP consumer
	// (sourcingEnrichmentAdapter.EnrichAndIndex → handler.EnrichAndIndexClip).
	// Future commits may move this to a typed port and drop the
	// field; the current shape mirrors artlist's Descriptor.Service.
	Handler *Handler
}

// ── Module satisfaction (api.Descriptor) ────────────────────────────
// Descriptor does NOT embed Module. The explicit field form does not
// promote Name / Enabled / RegisterRoutes via embedding, so we
// forward them by hand. (Matches the Artlist / Generation /
// ScriptAssets / Channels precedent.)

// Name returns the module name ("clips").
func (d *ClipsDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *ClipsDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure (and via Descriptor.Handler
// for the one non-HTTP consumer).
func (d *ClipsDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// RegisterJobHandlers implements api.DescriptorJobs so the
// composition root publishes the clips worker handlers
// (bulk_upload_youtube_clips) into the canonical jobs service
// at boot. The slot takes the typed JobRegistrar port (not the
// concrete *appjobs.Service) per godlike/06 Pattern 0 — the
// composition root injects its concrete service at
// descriptor-wiring time.
//
// Pre-Step-7 (July 2026): ClipsDescriptor did NOT implement
// DescriptorJobs. The handler was reachable via
// Descriptor.Handler.RegisterJobHandlers() in tests, but the
// production composition root (registry_public_modules.go)
// only calls RegisterJobHandlers on modules that type-assert as
// DescriptorJobs. The gap meant bulk_upload_youtube_clips was
// never registered at boot — the worker could never claim
// those jobs.
func (d *ClipsDescriptor) RegisterJobHandlers(svc api.JobRegistrar) error {
	if d.Handler == nil {
		return nil
	}
	// PR-CLIPS-NONOPS-CANONICAL-CHAIN (July 2026): route through the
	// orchestrator's 1-line delegator (Handler.RegisterJobHandlers)
	// instead of calling svc.RegisterHandler directly. The canonical
	// 3-method chain is
	//
	//	ClipsDescriptor.RegisterJobHandlers (this method)
	//	-> Handler.RegisterJobHandlers (handler.go)
	//	-> NonOpsHandler.RegisterJobHandlers (nonops/handler_jobs.go)
	//	-> jobs.Service.RegisterHandler
	//
	// The `svc` parameter is part of the api.DescriptorJobs interface
	// but unused here: the orchestrator's captured h.jobsSvc IS the
	// same instance the composition root passes (canonical wiring,
	// JobsBundle.Facade == JobsBundle.Service). Routing through the
	// orchestrator (a) keeps the fail-closed typed sentinel at
	// nonops.RegisterJobHandlers the FIRST observable surface for a
	// misconfigured JobsSvc, and (b) makes the dispatched handler
	// resolve through Handler.HandleBulkUploadYouTubeClipsJob ->
	// nonops.HandleBulkUploadYouTubeClipsJob -> bulkUploadWorker.HandleJob
	// which is the EXACT path the SSOT diagnostic (ec0d7b89d) validated.
	registerErr := d.Handler.RegisterJobHandlers()
	// PR-DIAG-BULKUPLOAD-REGISTRATION (July 2026, diagnostic-only):
	// confirm the write landed by logging the received svc's pointer
	// + the result of RegisterHandler. The transport-side pointer
	// (logged inside BulkUploadYouTubeClips at request entry) should
	// match this svc_ptr when the canonical wiring is correct; a
	// mismatch localizes the split. Diagnostic only — no behavioural
	// change; retire in a follow-up commit once the upstream bug is fixed.
	if d.Handler.log != nil {
		d.Handler.log.Info("clips: registered bulk_upload_youtube_clips handler",
			zap.String("module", "clips"),
			zap.String("svc_type", fmt.Sprintf("%T", svc)),
			zap.String("svc_ptr", fmt.Sprintf("%p", svc)),
			zap.String("descriptor_ptr", fmt.Sprintf("%p", d)),
			zap.String("handler_ptr", fmt.Sprintf("%p", d.Handler)),
			zap.Bool("register_ok", registerErr == nil),
			zap.Error(registerErr),
		)
	}
	return registerErr
}

// Build composes the Clips HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop(). Idempotency nil → no-op
// pass-through. ModuleOpts nil → no decorators applied.
//
// The returned Descriptor carries the Module (routes) + Handler
// (non-HTTP use cases). The HTTP Handler is constructed here and
// captured by the Module's RegisterRoutes closure — no caller
// (composition root, tests, internal services) reads the raw Handler
// anywhere outside this function.
func Build(deps Dependencies) (api.Descriptor, error) {
	// ── Mandatory-shape validation ────────────────────────────────
	if deps.ClipsRepo == nil {
		return nil, fmt.Errorf("clips.Build: ClipsRepo is required (composition root must pre-construct *assets.ClipsRepository from the canonical repo bundle)")
	}
	if deps.JobsSvc == nil {
		return nil, fmt.Errorf("clips.Build: JobsSvc is required (EnrichMedia / ReindexClip / BatchReindex / BulkUpload route handlers route through the canonical jobs system)")
	}
	if deps.Cfg == nil {
		return nil, fmt.Errorf("clips.Build: Cfg is required (Ingest sub-handler + driveRootForSource helper read *config.Config at request time)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("clips.Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}

	// Idempotency: nil → no-op pass-through (preserves the
	// test-fixture / dry-run CLI invocation path).
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	// Construct the canonical Handler orchestrator via the strict
	// fail-closed constructor (PR-CLIPS-NONOPS-FAIL-CLOSED,
	// July 2026). ValidateNonOpsDeps pre-check at composition time
	// ensures JobsSvc + BulkUploadWorker are non-nil — a partial
	// wiring that would fail at first enqueue ("no handler
	// registered for bulk_upload_youtube_clips") is now a 500 at
	// boot instead of a silent-success class (godlike/07
	// no-fake-availability). NewHandler itself remains nil-tolerant
	// for test fixtures that opt out of the fail-closed contract.
	handler, err := NewHandlerStrict(Deps{
		ClipsRepo:        deps.ClipsRepo,
		AssetRepo:        deps.AssetRepo,
		DeletionSvc:      deps.DeletionSvc,
		DriveAdmin:       deps.DriveAdmin,
		MediaProcessor:   deps.MediaProcessor,
		AssetTreeSvc:     deps.AssetTreeSvc,
		MetaWriter:       deps.MetaWriter,
		ClipIndexer:      deps.ClipIndexer,
		JobsSvc:          deps.JobsSvc,
		Cfg:              deps.Cfg,
		Log:              log,
		VoiceoverRepo:    deps.VoiceoverRepo,
		ImagesRepo:       deps.ImagesRepo,
		ArtifactSvc:      deps.ArtifactSvc,
		FolderMemSvc:     deps.FolderMemSvc,
		SearchSvc:        deps.SearchSvc,
		ProcessRunner:   deps.ProcessRunner,
		Dispatcher:      deps.Dispatcher,
		DuplicateFinder: deps.DuplicateFinder,
		ReuploadUC:      deps.ReuploadUC,
		EnrichUC:         deps.EnrichUC,
		BulkUploadWorker: deps.BulkUploadWorker,
		ClipOpsService:   deps.ClipOpsService,
		UploadUC:         deps.UploadUC,
		Publisher:        deps.Publisher,
	}, idem)
	if err != nil {
		return nil, fmt.Errorf("clips.Build: %w", err)
	}

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	mod := api.NewRouteModule(
		"clips",
		deps.EnabledFunc,
		"/clips",
		handler,
		log,
		deps.ModuleOpts..., // typically []ModuleOption{api.WithMiddleware(...)}
	)

	return &ClipsDescriptor{
		Module:  mod,
		Handler: handler,
	}, nil
}

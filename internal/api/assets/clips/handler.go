// Package clips hosts the unified HTTP handler that owns every clip-related
// endpoint. PR-A Phase 4 BULK consolidation: a single Handler struct carries
// the full dep surface and exposes every method previously scattered across
// handler_sources_clip_*.go in the flat sources package.
//
// Splits 1 + 2 + Step-5-Split-2 (June 2026, override ADR 0009): Search /
// Ingest / Ops sub-handlers own their idiomatic-route band via per-cluster
// RegisterRoutes. Each sub-handler receiver receives only the deps it
// consumes. The orchestrator *Handler keeps a public Deps bag, applies one
// idempotency middleware (PR8) for all writes, and calls RegisterRoutes on
// each sub-handler.
//
// NonOps methods (9): BulkAddTags / BulkRemoveTags / ReprocessClip /
// ReindexClip / BatchReindex / EnrichMedia / EnrichAndIndexClip /
// RegisterJobHandlers / HandleBulkUploadYouTubeClipsJob live in the
// nonops sub-package (PR-CLIPS-NONOPS-EXTRACT, July 2026, deadline
// 2026-08-01). The orchestrator *Handler keeps 3 one-line
// delegators (EnrichAndIndexClip / RegisterJobHandlers /
// HandleBulkUploadYouTubeClipsJob) for non-HTTP consumer stability
// (sourcingEnrichmentAdapter + ClipsDescriptor.RegisterJobHandlers +
// jobs-service dispatcher). The 6 HTTP routes are installed via
// h.nonops.RegisterRoutes(r, idem) in Handler.RegisterRoutes.
//
// Action cluster (DownloadClip / ReuploadClip / FindDuplicates) stays on
// *Handler via clip_action.go; Action its own sub-handler will land in a
// later commit.
package clips

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/nonops"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Deps is the constructor bag for Handler. Keeping deps in a struct
// rather than 14 positional arguments makes wiring sites readable and
// future dep additions non-breaking.
type Deps struct {
	ClipsRepo        *assets.ClipsRepository
	AssetRepo        asset.Repository
	DeletionSvc      *deletion.DeletionService
	DriveAdmin       drive.Admin
	MediaProcessor   asset.Processor
	AssetTreeSvc     *assettree.Service
	MetaWriter       semantic.MetadataWriterPort
	ClipIndexer      *clipindexer.Service
	JobsSvc          jobservice.Service
	Cfg              *config.Config
	Log              *zap.Logger
	VoiceoverRepo    *assets.VoiceoversRepository
	ImagesRepo       *assets.ImagesRepository
	ArtifactSvc      *artifacts.Service
	FolderMemSvc     *foldermemory.Service
	SearchSvc        *search.Aggregator
	ProcessRunner    appassets.ProcessRunner
	Dispatcher       appclips.ClipIndexDispatcherPort
	DuplicateFinder  *duplicates.Finder
	ReuploadUC       *appclips.ReuploadUseCase
	EnrichUC         *appclips.EnrichUseCase
	BulkUploadWorker *appclips.BulkUploadWorker
	ClipOpsService   *appclips.ClipOpsService
	UploadUC         *appupload.UseCase
	Publisher        delivery.Publisher
}

// Handler owns every clip-related HTTP method. One receiver per method;
// methods live on *Handler until their cluster lands its own sub-handler
// (Search/Ingest/Ops/NonOps already split; Action methods stay inline
// until their future split).
type Handler struct {
	// PR8 (June 2026): Idempotency is the reusable Gin idempotency
	// middleware (constructed once at server boot via NewHandler →
	// WireAssets → BuildRepoBundle.IdempotencyStore). Nil-tolerated so
	// test fixtures can opt out. Only WRITE routes install it — READ
	// routes fall through unchanged.
	Idempotency gin.HandlerFunc

	// Action cluster mirror fields (split 3 TBD).
	assetRepo       asset.Repository
	driveAdmin      drive.Admin
	duplicateFinder *duplicates.Finder
	downloadUC       *appclips.DownloadUseCase
	reuploadUC       *appclips.ReuploadUseCase
	publisher        delivery.Publisher
	log              *zap.Logger
	jobsSvc          jobservice.Service

	// Cfg used by driveRootForSource helper (Action cluster).
	cfg *config.Config

	// search (Split 1): Search sub-handler — 4 clip-search routes.
	search *SearchHandler
	// ingest (Split 2): Ingest sub-handler — 3 ingest routes.
	ingest *IngestHandler
	// ops (Step 5 Split 2): Ops sub-handler — 14 ops routes (5 read + 9 write+idem).
	ops *OpsHandler
	// bulk (DRIFT-CLIPS-BULK-SPLIT-5, July 2026): BulkUploadTransport —
	// 1 HTTP route POST /:source/clips/bulk-upload-youtube-clips. Reconnected
	// after PR-CLIPS-NONOPS-EXTRACT orphaned the receiver (July 2026).
	bulkTransport *BulkUploadTransport
	// nonops (PR-CLIPS-NONOPS-EXTRACT, July 2026): 9 NonOps methods
	// (6 HTTP routes + 3 non-HTTP delegators on the orchestrator)
	// extracted from handler.go + handler_delegators.go +
	// handler_reprocess.go + handler_index.go + handler_download.go +
	// clip_ops_handlers.go. Construction receives pre-built use case
	// instances per thinker verdict Q7 (don't re-construct in the
	// sub-package — that would leak repository/service deps to
	// nonops).
	nonops *nonops.NonOpsHandler
}

// NewHandler constructs the unified Handler. May be called before every
// dependency is wired — individual methods that need a missing dep will
// internal-error handle it (preserved legacy behavior).
//
// PR8: idempotencyMiddleware is the reusable Gin idempotency middleware
// instance; a nil value disables idempotency (test fixtures / dry-run
// CLI invocations). Production wiring passes the canonical *middleware.Idempotency
// value constructed from BuildRepoBundle.IdempotencyStore.
func NewHandler(d Deps, idempotencyMiddleware gin.HandlerFunc) *Handler {
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}	// S1a (June 2026): when the composition root supplies a shared
	// EnrichUC, reuse it; when nil (test fixture, partial deploy),
	// construct a local fallback copy that preserves pre-lift behaviour.
	enrichUC := enrichUCOrLocal(d.EnrichUC, d.AssetRepo, d.MetaWriter, d.Log)
	bulkTagsUC := appclips.NewBulkTagsUseCase(d.ClipsRepo, d.AssetTreeSvc)
	downloadUC := appclips.NewDownloadUseCase(d.AssetRepo, d.VoiceoverRepo)
	reprocessUC := appclips.NewReprocessUseCase(d.AssetRepo, d.MediaProcessor, nil)

	h := &Handler{
		Idempotency:     idem,
		assetRepo:       d.AssetRepo,
		duplicateFinder: d.DuplicateFinder,
		driveAdmin:      d.DriveAdmin,
		downloadUC:       downloadUC,
		reuploadUC:       d.ReuploadUC,
		publisher:        d.Publisher,
		log:              d.Log,
		jobsSvc:          d.JobsSvc,
		cfg:              d.Cfg,

		// Split 1 (June 2026, override ADR 0009): Search sub-handler.
		search: NewSearchHandler(SearchDeps{
			ClipsRepo:     d.ClipsRepo,
			AssetRepo:     d.AssetRepo,
			VoiceoverRepo: d.VoiceoverRepo,
			ImagesRepo:    d.ImagesRepo,
			SearchSvc:     d.SearchSvc,
		}),
		// Split 2 (June 2026, override ADR 0009): Ingest sub-handler.
		// 6 fields removed July 2026 (dead code — UploadVideoClip was
		// migrated to uploadUC.Execute).
		ingest: NewIngestHandler(IngestDeps{
			Dispatcher:   d.Dispatcher,
			AssetTreeSvc: d.AssetTreeSvc,
			JobsSvc:      d.JobsSvc,
			ClipsRepo:    d.ClipsRepo,
			EnrichUC:     enrichUC,
			UploadUC:     d.UploadUC,
			Log:          d.Log,
		}),
		// Step 5 Split 2 (June 2026, override ADR 0009): Ops sub-handler
		// owns 14 routes (5 read + 9 write+idem). The 7 OpsDeps fields
		// below are exactly what the 14 moved methods touch — no more,
		// no less (cluster × deps matrix §4). Non-Ops methods stay
		// inline on *Handler until their future sub-handlers land.
		ops: NewOpsHandler(OpsDeps{
			ClipOpsService: d.ClipOpsService,
			DeletionSvc:    d.DeletionSvc,
			FolderMemSvc:   d.FolderMemSvc,
			ClipsRepo:      d.ClipsRepo,
			DriveAdmin:     d.DriveAdmin,
			AssetTreeSvc:   d.AssetTreeSvc,
			Log:            d.Log,
		}),
		// BulkUploadTransport (DRIFT-CLIPS-BULK-SPLIT-5 + reconnector
		// patch, July 2026): the SINGLE HTTP route
		// POST /:source/clips/bulk-upload-youtube-clips. All 5 deps
		// come straight from the orchestrator Deps bag.
		bulkTransport: NewBulkUploadTransport(BulkTransportDeps{
			JobsSvc:          d.JobsSvc,
			DriveAdmin:       d.DriveAdmin,
			Cfg:              d.Cfg,
			BulkUploadWorker: d.BulkUploadWorker,
			Publisher:        d.Publisher,
			Log:              d.Log,
		}),
	}

	// PR-CLIPS-NONOPS-EXTRACT (July 2026): construct the NonOps
	// sub-handler AFTER the h struct is in place so we can bind
	// h.repoForSource as the canonical source-resolution callback
	// (method value, captured even though h is partially populated
	// at this point — when the method is invoked later via
	// h.nonops.ReindexClip, the receiver's h.search field is fully
	// constructed and the lookup chains correctly).
	h.nonops = nonops.NewNonOpsHandler(nonops.Deps{
		BulkTagsUC:       bulkTagsUC,
		ReprocessUC:      reprocessUC,
		EnrichUC:         enrichUC,
		ClipIndexer:      d.ClipIndexer,
		JobsSvc:          d.JobsSvc,
		BulkUploadWorker: d.BulkUploadWorker,
		RepoForSource:    h.repoForSource,
		Log:              d.Log,
	})

	return h
}

// NewHandlerStrict constructs the unified Handler with fail-closed
// validation of the canonical 3-method job-handler registration chain
// deps at construction time. Threaded through the composition root
// (clips/module.go::Build -> NewHandlerStrict) so a partial wiring
// that would fail at first enqueue crashes loudly at boot instead —
// godlike/07 no-fake-availability.
//
// godlike/06 SSOT: the canonical path is
//
//	clips.Build (module.go)
//	  -> NewHandlerStrict (this function)
//	      -> nonops.ValidateNonOpsDeps pre-check on the required deps
//	      -> NewHandler construction (with the validated locator deps)
//	  -> ClipsDescriptor.RegisterJobHandlers (module.go)
//	      -> Handler.RegisterJobHandlers (handler.go)
//	          -> NonOpsHandler.RegisterJobHandlers (nonops/handler_jobs.go)
//	              -> jobs.Service.RegisterHandler
//
// If the pre-check fails (JobsSvc or BulkUploadWorker is nil), the
// construction returns an error instead of constructing a Handler
// with a partially-wired nonops sub-handler. The legacy NewHandler
// (nil-tolerant at construction) remains for test fixtures that
// opt out of the fail-closed contract.
func NewHandlerStrict(d Deps, idempotencyMiddleware gin.HandlerFunc) (*Handler, error) {
	if err := nonops.ValidateNonOpsDeps(nonops.Deps{
		JobsSvc:          d.JobsSvc,
		BulkUploadWorker: d.BulkUploadWorker,
	}); err != nil {
		return nil, err
	}
	return NewHandler(d, idempotencyMiddleware), nil
}

// enrichUCOrLocal returns `shared` when non-nil, otherwise constructs a
// fresh EnrichUseCase with the supplied dependencies. Single-line
// helper that documents the share-or-construct decision inline.
//
// Wave 2 (Asset commit + Qdrant, July 2026): the local fallback has
// no dispatcher by design (the orchestrator only sees the narrower
// ClipIndexDispatcherPort). Enrichment will still run, but re-index
// enqueueing is skipped with a warning.
func enrichUCOrLocal(
	shared *appclips.EnrichUseCase,
	repo asset.Repository,
	mw semantic.MetadataWriterPort,
	log *zap.Logger,
) *appclips.EnrichUseCase {
	if shared != nil {
		return shared
	}
	return appclips.NewEnrichUseCase(repo, mw, nil, log)
}

// repoForSource resolves a clip source to its canonical repository
// by delegating to the Search sub-handler (Split 1, June 2026).
// Used as the method-value callback by the nonops sub-handler
// (RepoForSource: h.repoForSource in NewHandler) so the lookup
// chains into the Search sub-handler without coupling nonops to
// it directly.
func (h *Handler) repoForSource(source string) *assets.ClipsRepository {
	if h.search == nil {
		return nil
	}
	return h.search.repoForSource(source)
}

// ── One-line delegators to nonops (3 non-HTTP methods) ────────────────
//
// PR-CLIPS-NONOPS-EXTRACT (July 2026): these 3 methods stay on
// *Handler (the orchestrator) as one-line delegators to the nonops
// sub-handler so external non-HTTP consumers continue to work
// without breaking. The 6 HTTP routes are installed via
// h.nonops.RegisterRoutes(r, idem) in RegisterRoutes below.

// EnrichAndIndexClip is a 1-line delegator to nonops.EnrichAndIndexClip.
// External consumer: sourcingEnrichmentAdapter
// (internal/app/youtube_adapters_meta.go) calls this on the
// orchestrator *Handler to drive the bulk-enrich path that
// supplements the HTTP /enrich route. Returns immediately if the
// nonops sub-handler is nil (test fixture / pre-construction call).
func (h *Handler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	if h.nonops == nil {
		return
	}
	h.nonops.EnrichAndIndexClip(ctx, clip, source)
}

// RegisterJobHandlers is a 1-line delegator to nonops.RegisterJobHandlers.
// External consumers: ClipsDescriptor.RegisterJobHandlers
// (clips/module.go) + tests. Returns nil when the nonops sub-handler
// is nil (test fixture / pre-construction call).
func (h *Handler) RegisterJobHandlers() error {
	if h.nonops == nil {
		return nil
	}
	return h.nonops.RegisterJobHandlers()
}

// HandleBulkUploadYouTubeClipsJob is a 1-line delegator to
// nonops.HandleBulkUploadYouTubeClipsJob. External consumer: the
// jobs service dispatcher (wired via RegisterJobHandlers above).
// Returns a typed error when the nonops sub-handler is nil
// (test fixture / pre-construction call) so the dispatcher can
// fail-closed rather than silently succeeding on a no-op.
func (h *Handler) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *jobservice.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h.nonops == nil {
		return nil, fmt.Errorf("nonops sub-handler not wired (clips.Handler constructed without NewHandler)")
	}
	return h.nonops.HandleBulkUploadYouTubeClipsJob(ctx, j, tools)
}

// RegisterRoutes mounts the entire clip-route surface.
// Thin delegators + helpers moved to handler_delegators.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #7). The 6 NonOps
// HTTP routes (BulkAddTags + BulkRemoveTags + ReprocessClip +
// ReindexClip + EnrichMedia + BatchReindex) are installed via
// h.nonops.RegisterRoutes(r, idem) — see PR-CLIPS-NONOPS-EXTRACT
// (July 2026) for the sub-package extraction rationale.
//
// NOTE (Wave 4, July 2026): This method is retained as the
// canonical route surface for existing tests and for callers that
// construct the Handler directly. The clips.Build composition path
// registers routes through the sub-descriptors (catalog, ingest,
// processing, publication, indexing, operations, bulk) instead of
// calling this method, so production registration does not double-
// mount routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()

	h.ingest.RegisterRoutes(r, idem)
	h.search.RegisterRoutes(r, idem)
	h.ops.RegisterRoutes(r, idem)
	h.nonops.RegisterRoutes(r, idem)
	h.bulkTransport.RegisterRoutes(r, idem)

	r.POST("/:source/clips/:id/download", idem, h.DownloadClip)
	r.POST("/:source/clips/:id/duplicates", idem, h.FindDuplicates)
	r.POST("/:source/clips/:id/reupload", idem, h.ReuploadClip)
}

// ── Wave 4 sub-descriptor factory methods ─────────────────────────────
//
// These methods expose the existing sub-handlers as
// submodule.RouteRegistrar instances so that clips.Build can
// compose the clips capability from catalog/ingest/processing/
// publication/indexing/operations/bulk sub-descriptors without
// exposing the Handler's unexported fields. Each factory returns
// a wrapper that delegates to the corresponding sub-handler.

func (h *Handler) catalogRegistrar(idem gin.HandlerFunc) *catalogRegistrar {
	return &catalogRegistrar{search: h.search, ops: h.ops, h: h, idem: idem}
}

func (h *Handler) ingestRegistrar(idem gin.HandlerFunc) *ingestRegistrar {
	return &ingestRegistrar{ingest: h.ingest, idem: idem}
}

func (h *Handler) processingRegistrar(idem gin.HandlerFunc) *processingRegistrar {
	return &processingRegistrar{nonops: h.nonops, idem: idem}
}

func (h *Handler) publicationRegistrar(idem gin.HandlerFunc) *publicationRegistrar {
	return &publicationRegistrar{h: h, idem: idem}
}

func (h *Handler) indexingRegistrar(idem gin.HandlerFunc) *indexingRegistrar {
	return &indexingRegistrar{nonops: h.nonops, idem: idem}
}

func (h *Handler) operationsRegistrar(idem gin.HandlerFunc) *operationsRegistrar {
	return &operationsRegistrar{ops: h.ops, nonops: h.nonops, idem: idem}
}

func (h *Handler) bulkRegistrar(idem gin.HandlerFunc) *bulkRegistrar {
	return &bulkRegistrar{bulk: h.bulkTransport, idem: idem}
}

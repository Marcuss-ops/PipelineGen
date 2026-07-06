// Package clips hosts the unified HTTP handler that owns every clip-related
// endpoint. PR-A Phase 4 BULK consolidation: a single Handler struct carries
// the full 27-dep surface and exposes every method previously scattered
// across handler_sources_clip_*.go in the flat sources package.
//
// Splits 1 + 2 + Step-5-Split-2 (June 2026, override ADR 0009): Search /
// Ingest / Ops sub-handlers own their idiomatic-route band via per-cluster
// RegisterRoutes. Each sub-handler receiver receives only the deps it
// consumes. The orchestrator *Handler keeps a public Deps bag, applies one
// idempotency middleware (PR8) for all writes, and calls RegisterRoutes on
// each sub-handler.
//
// NON-Ops methods: BulkAddTags/BulkRemoveTags stay INLINE on *Handler.
// ReprocessClip → handler_reprocess.go, EnrichMedia+EnrichAndIndexClip
// → handler_download.go, ReindexClip+BatchReindex → handler_index.go
// (PG-028 capability split, July 2026).
//
// Action cluster (DownloadClip / ReuploadClip / FindDuplicates) stays on
// *Handler via clip_action.go; Action its own sub-handler will land in a
// later commit.
package clips

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
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
	MetaWriter       *semantic.MetadataWriter
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
	SearchAggregator *providers.SearchAggregator
	ReuploadUC       *appclips.ReuploadUseCase
	EnrichUC         *appclips.EnrichUseCase
	BulkUploadWorker *appclips.BulkUploadWorker
	ClipOpsService   *appclips.ClipOpsService
	UploadUC         *appupload.UseCase
}

// Handler owns every clip-related HTTP method. One receiver per method;
// methods live on *Handler until their cluster lands its own sub-handler
// (Search/Ingest/Ops already split; NonOps methods + Action methods stay
// inline until their future splits).
type Handler struct {
	// PR8 (June 2026): Idempotency is the reusable Gin idempotency
	// middleware (constructed once at server boot via NewHandler →
	// WireAssets → BuildRepoBundle.IdempotencyStore). Nil-tolerated so
	// test fixtures can opt out. Only WRITE routes (POST/PUT/PATCH/DELETE
	// on /clips/* and the upload/bulk routes) install it — READ routes
	// fall through unchanged.
	Idempotency gin.HandlerFunc

	// Mirror fields for INLINE NON-Ops methods on *Handler (Step 5 Split
	// 2 — these methods live inline on *Handler until their clusters
	// get dedicated sub-handlers in follow-up commits).
	jobsSvc          jobservice.Service         // EnrichMedia/ReindexClip/BatchReindex + RegisterJobHandlers
	bulkTagsUC       *appclips.BulkTagsUseCase  // BulkAddTags/BulkRemoveTags
	reprocessUC      *appclips.ReprocessUseCase // ReprocessClip
	enrichUC         *appclips.EnrichUseCase    // EnrichAndIndexClip helper + nil-check in EnrichMedia/ReindexClip
	clipIndexer      *clipindexer.Service       // ReindexClip/BatchReindex
	bulkUploadWorker *appclips.BulkUploadWorker // HandleBulkUploadYouTubeClipsJob

	// Action cluster mirror fields (split 3 TBD).
	assetRepo        asset.Repository
	searchAggregator *providers.SearchAggregator
	driveAdmin       drive.Admin
	downloadUC       *appclips.DownloadUseCase
	reuploadUC       *appclips.ReuploadUseCase
	log              *zap.Logger

	// Cfg used by driveRootForSource helper (Action cluster).
	cfg *config.Config

	// search (Split 1): Search sub-handler — 4 clip-search routes.
	search *SearchHandler
	// ingest (Split 2): Ingest sub-handler — 3 ingest routes.
	ingest *IngestHandler
	// ops (Step 5 Split 2): Ops sub-handler — 14 ops routes (5 read + 9 write+idem).
	ops *OpsHandler
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
	}

	// S1a (June 2026): when the composition root supplies a shared
	// EnrichUC, reuse it; when nil (test fixture, partial deploy),
	// construct a local fallback copy that preserves pre-lift behaviour.
	enrichUC := enrichUCOrLocal(d.EnrichUC, d.AssetRepo, d.ClipIndexer, d.MetaWriter, d.Log)
	bulkTagsUC := appclips.NewBulkTagsUseCase(d.ClipsRepo, d.AssetTreeSvc)
	downloadUC := appclips.NewDownloadUseCase(d.AssetRepo, d.VoiceoverRepo)
	reprocessUC := appclips.NewReprocessUseCase(d.AssetRepo, d.MediaProcessor, nil)

	return &Handler{
		Idempotency:      idem,
		jobsSvc:          d.JobsSvc,
		bulkTagsUC:       bulkTagsUC,
		reprocessUC:      reprocessUC,
		enrichUC:         enrichUC,
		clipIndexer:      d.ClipIndexer,
		bulkUploadWorker: d.BulkUploadWorker,

		assetRepo:        d.AssetRepo,
		searchAggregator: d.SearchAggregator,
		driveAdmin:       d.DriveAdmin,
		downloadUC:       downloadUC,
		reuploadUC:       d.ReuploadUC,
		log:              d.Log,
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
	}
}

// enrichUCOrLocal returns `shared` when non-nil, otherwise constructs a
// fresh EnrichUseCase with the supplied dependencies. Single-line
// helper that documents the share-or-construct decision inline.
func enrichUCOrLocal(
	shared *appclips.EnrichUseCase,
	repo asset.Repository,
	indexer *clipindexer.Service,
	mw *semantic.MetadataWriter,
	log *zap.Logger,
) *appclips.EnrichUseCase {
	if shared != nil {
		return shared
	}
	return appclips.NewEnrichUseCase(repo, indexer, mw, log)
}

// repoForSource resolves a clip source to its canonical repository
// by delegating to the Search sub-handler (Split 1, June 2026). The
// canonical impl lives on *SearchHandler; this thin delegator preserves
// byte-compatible behaviour for callers in clip_enrich.go (returned by
// Split 4) and any inline callers on the orchestrator.
func (h *Handler) repoForSource(source string) *assets.ClipsRepository {
	if h.search == nil {
		return nil
	}
	return h.search.repoForSource(source)
}

// ──────────────────────────────────────────────────────────────────────
// Thin delegators (Split 2, June 2026): Ingest sub-handler.
// These preserve byte-compatible behaviour for callers that wire
// Handler methods directly as gin.HandlerFunc (e.g.
// dispatcher_fail_closed_test.go: g.POST("/clips", h.CreateClip)).
// The canonical impl lives on *IngestHandler.
// ──────────────────────────────────────────────────────────────────────

// CreateClip thin-delegates to IngestHandler.CreateClip.
func (h *Handler) CreateClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.CreateClip(c)
}

// UpdateClip thin-delegates to IngestHandler.UpdateClip.
func (h *Handler) UpdateClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.UpdateClip(c)
}

// UploadVideoClip thin-delegates to IngestHandler.UploadVideoClip.
func (h *Handler) UploadVideoClip(c *gin.Context) {
	if h.ingest == nil {
		apiutil.Error(c, 503, "ingest sub-handler not wired")
		return
	}
	h.ingest.UploadVideoClip(c)
}

// ──────────────────────────────────────────────────────────────────────
// Thin delegators (Step 5 Split 2, June 2026): Ops sub-handler.
// Canonical impl lives on *OpsHandler (ops.go::RegisterRoutes). These
// preserve byte-compatible behaviour for callers that wire Handler
// methods directly as gin.HandlerFunc (clip_ops_test.go uses
// g.POST("/:source/cleanup", h.Cleanup) — test compat preserved).
// ──────────────────────────────────────────────────────────────────────

// VerifyClip thin-delegates to OpsHandler.VerifyClip.
func (h *Handler) VerifyClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.VerifyClip(c)
}

// HandleFixHash thin-delegates to OpsHandler.HandleFixHash.
func (h *Handler) HandleFixHash(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.HandleFixHash(c)
}

// TrashClip thin-delegates to OpsHandler.TrashClip.
func (h *Handler) TrashClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.TrashClip(c)
}

// DeleteClip thin-delegates to OpsHandler.DeleteClip.
func (h *Handler) DeleteClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.DeleteClip(c)
}

// Reconcile thin-delegates to OpsHandler.Reconcile.
func (h *Handler) Reconcile(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.Reconcile(c)
}

// Cleanup thin-delegates to OpsHandler.Cleanup.
func (h *Handler) Cleanup(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.Cleanup(c)
}

// ListFolders thin-delegates to OpsHandler.ListFolders.
func (h *Handler) ListFolders(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.ListFolders(c)
}

// FolderStatus thin-delegates to OpsHandler.FolderStatus.
func (h *Handler) FolderStatus(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.FolderStatus(c)
}

// RegenerateManifest thin-delegates to OpsHandler.RegenerateManifest.
func (h *Handler) RegenerateManifest(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.RegenerateManifest(c)
}

// TrashFolder thin-delegates to OpsHandler.TrashFolder.
func (h *Handler) TrashFolder(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.TrashFolder(c)
}

// DeleteFolder thin-delegates to OpsHandler.DeleteFolder.
func (h *Handler) DeleteFolder(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.DeleteFolder(c)
}

// GetFolderChildren thin-delegates to OpsHandler.GetFolderChildren.
func (h *Handler) GetFolderChildren(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetFolderChildren(c)
}

// GetTree thin-delegates to OpsHandler.GetTree.
func (h *Handler) GetTree(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetTree(c)
}

// GetBreadcrumb thin-delegates to OpsHandler.GetBreadcrumb.
func (h *Handler) GetBreadcrumb(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.GetBreadcrumb(c)
}

// ──────────────────────────────────────────────────────────────────────
// NON-Ops methods (Step 5 Split 2, June 2026): only BulkTags endpoints
// stay INLINE. ReprocessClip → handler_reprocess.go, EnrichMedia +
// EnrichAndIndexClip → handler_download.go, ReindexClip + BatchReindex
// → handler_index.go per PG-028 capability split (July 2026).
// ──────────────────────────────────────────────────────────────────────

// BulkAddTags adds tags to multiple clips in one request.
func (h *Handler) BulkAddTags(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	result, err := h.bulkTagsUC.AddTags(c.Request.Context(), appclips.BulkTagsRequest{
		Source: source,
		IDs:    req.IDs,
		Tags:   req.Tags,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  result.Source,
		"count":   result.Count,
		"message": result.Message,
	})
}

// BulkRemoveTags removes tags from multiple clips.
func (h *Handler) BulkRemoveTags(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	result, err := h.bulkTagsUC.RemoveTags(c.Request.Context(), appclips.BulkTagsRequest{
		Source: source,
		IDs:    req.IDs,
		Tags:   req.Tags,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  result.Source,
		"count":   result.Count,
		"message": result.Message,
	})
}

// ──────────────────────────────────────────────────────────────────────
// Action cluster helpers — Step 3 TBD; for now inline on *Handler via
// clip_action.go (DownloadClip / ReuploadClip / FindDuplicates).
// ──────────────────────────────────────────────────────────────────────

// driveRootForSource returns the Drive root folder for a clip source
// along with the URL marker the source-checker uses. Used by Action
// cluster methods (DownloadClip / ReuploadClip).
// Collapse (June 2026): local map eliminated — canonical source
// routing via artifacts.CanonicalSource + config dispatch.
func (h *Handler) driveRootForSource(source string) (string, string) {
	if h.cfg == nil {
		return "", ""
	}
	canonical := artifacts.CanonicalSource(source)
	switch canonical {
	case "clips", "youtube":
		return h.cfg.Drive.ClipsFolder(), "/clips/"
	case "artlist":
		return h.cfg.Drive.ArtlistFolder(), "/artlist/"
	case "stock":
		return h.cfg.Drive.StockFolder(), "/stock/"
	default:
		return "", ""
	}
}

// RegisterJobHandlers wires up the bulk-upload worker. SourcesHandler's
// RegisterJobHandlers delegates here. Step 5 Split 5 = BulkUpload cluster
// will move this onto a dedicated *BulkUploadTransport receiver; for
// now the dispatcher lives in clip_ops_handlers.go on *Handler.
func (h *Handler) RegisterJobHandlers() error {
	if h.jobsSvc == nil {
		return nil
	}
	return h.jobsSvc.RegisterHandler("bulk_upload_youtube_clips", h.HandleBulkUploadYouTubeClipsJob)
}

// PR8 helper: idemWriter returns h.Idempotency if set, else a no-op
// pass-through handler. Used only for Write routes.
func (h *Handler) idemWriter() gin.HandlerFunc {
	if h.Idempotency == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return h.Idempotency
}

// RegisterRoutes mounts the entire clip-route surface on the supplied
// gin router group.
//
// Step 5 Split 2 (June 2026, override ADR 0009): Search/Ingest/Ops
// sub-handlers own their idiomatic-route band via per-cluster
// RegisterRoutes. The orchestrator installs idem ONCE and forwards.
// NON-Ops methods (BulkTags, Reprocess, Enrich, Reindex, BulkUpload)
// stay inline on *Handler until their future sub-handlers land.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()

	// Ingest sub-handler (Split 2, June 2026):
	//   POST  /:source/clips           -> CreateClip      (write+idem)
	//   PATCH /:source/clips/:id       -> UpdateClip      (write+idem)
	//   POST  /upload-video            -> UploadVideoClip (write+idem)
	h.ingest.RegisterRoutes(r, idem)

	// Search sub-handler (Split 1, June 2026):
	//   GET  /:source/clips               -> ListClips       (read)
	//   GET  /:source/clips/:id           -> GetClip         (read)
	//   POST /:source/clips/:id/status    -> ClipStatus      (write+idem)
	//   POST /search/advanced             -> AdvancedSearch  (write+idem)
	h.search.RegisterRoutes(r, idem)

	// Ops sub-handler (Step 5 Split 2 + Blocco A3, June 2026):
	// 5 read + 7 write+idem = 12 routes (see ops.go doc-comment).
	// DELETE /:source/clips/:id  +  DELETE /:source/folders/:id
	// replace the old POST .../trash + POST .../delete (Blocco A3).
	h.ops.RegisterRoutes(r, idem)

	// NON-Ops routes (inline on *Handler):
	//   POST /:source/bulk/tags/add      -> BulkAddTags       (write+idem)
	//   POST /:source/bulk/tags/remove   -> BulkRemoveTags    (write+idem)
	//   POST /:source/clips/:id/reprocess -> ReprocessClip   (write+idem)
	//   POST /:source/clips/:id/reindex  -> ReindexClip       (write+idem)
	//   POST /enrich                     -> EnrichMedia       (write+idem)
	//   POST /enrich/batch               -> BatchReindex      (write+idem)
	r.POST("/:source/bulk/tags/add", idem, h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", idem, h.BulkRemoveTags)
	r.POST("/:source/clips/:id/reprocess", idem, h.ReprocessClip)
	r.POST("/:source/clips/:id/reindex", idem, h.ReindexClip)
	r.POST("/enrich", idem, h.EnrichMedia)
	r.POST("/enrich/batch", idem, h.BatchReindex)

	// Action cluster routes stay on *Handler (clip_action.go):
	//   POST /:source/clips/:id/download -> DownloadClip   (write+idem)
	//   POST /:source/clips/:id/duplicates -> FindDuplicates (write+idem)
	//   POST /:source/clips/:id/reupload -> ReuploadClip   (write+idem)
	r.POST("/:source/clips/:id/download", idem, h.DownloadClip)
	r.POST("/:source/clips/:id/duplicates", idem, h.FindDuplicates)
	r.POST("/:source/clips/:id/reupload", idem, h.ReuploadClip)
}

// ──────────────────────────────────────────────────────────────────────
// Local typed alias for jobs.EnqueueRequest so Handler-inline enqueue
// sites don't have to import jobservice.EnqueueRequest verbatim. Step 5
// Split 2 — allocator decided that any handler inline call to JobsSvc
// uses this local type to keep the handler file's import surface tight.
// ──────────────────────────────────────────────────────────────────────

type enqueueRequest = jobservice.EnqueueRequest

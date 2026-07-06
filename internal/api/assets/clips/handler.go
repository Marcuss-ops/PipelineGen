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
// by delegating to the Search sub-handler (Split 1, June 2026).
func (h *Handler) repoForSource(source string) *assets.ClipsRepository {
	if h.search == nil {
		return nil
	}
	return h.search.repoForSource(source)
}

// RegisterRoutes mounts the entire clip-route surface.
// Thin delegators + helpers moved to handler_delegators.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band B #7).
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()

	h.ingest.RegisterRoutes(r, idem)
	h.search.RegisterRoutes(r, idem)
	h.ops.RegisterRoutes(r, idem)

	r.POST("/:source/bulk/tags/add", idem, h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", idem, h.BulkRemoveTags)
	r.POST("/:source/clips/:id/reprocess", idem, h.ReprocessClip)
	r.POST("/:source/clips/:id/reindex", idem, h.ReindexClip)
	r.POST("/enrich", idem, h.EnrichMedia)
	r.POST("/enrich/batch", idem, h.BatchReindex)

	r.POST("/:source/clips/:id/download", idem, h.DownloadClip)
	r.POST("/:source/clips/:id/duplicates", idem, h.FindDuplicates)
	r.POST("/:source/clips/:id/reupload", idem, h.ReuploadClip)
}

// Local typed alias for jobs.EnqueueRequest.
type enqueueRequest = jobservice.EnqueueRequest

// Package clips hosts the unified HTTP handler that owns every clip-related
// endpoint. PR-A Phase 4 BULK consolidation: a single Handler struct carries
// the full 27-dep surface and exposes every method previously scattered
// across handler_sources_clip_*.go in the flat sources package.
//
// Sub-handler fan-out (DeleteHandler, SearchHandler) is replaced by
// receivers on *Handler — there is no longer a need for nested structs.
// SourcesHandler keeps a single *clips.Handler field and delegates each
// clip-route registration to clips.Handler.{CreateClip, GetClip, ...}.
//
// Splits 1, 2, 4 (June 2026, override ADR 0009): Search / Ingest / Ops
// sub-handlers own their idiomatic-route band via per-cluster
// RegisterRoutes. Each sub-handler receiver receives only the deps it
// consumes (cluster × deps matrix §4, June 2026). The orchestrator
// *Handler keeps a public Deps bag, applies one idempotency middleware
// (PR8) for all writes, and calls RegisterRoutes on each sub-handler.
// The pre-split pattern of inline `r.POST(..., h.<method>)` works only
// for Action cluster routes (Split 3, not yet landed).
package clips

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// Deps is the constructor bag for Handler. Keeping deps in a struct
// rather than 14 positional arguments makes wiring sites readable and
// future dep additions non-breaking.
// VectorStore field removed from handler deps.
type Deps struct {
	SourceResolver *artifacts.SourceResolver
	AssetRepo      asset.Repository
	DeletionSvc    *deletion.DeletionService
	DriveUploader  *drive.Uploader
	MediaProcessor asset.Processor
	AssetTreeSvc   *assettree.Service
	MetaWriter     *semantic.MetadataWriter
	ClipIndexer    *clipindexer.Service
	JobsSvc        jobservice.Service
	Cfg            *config.Config
	Log            *zap.Logger
	// VoiceoverRepo enables the voiceover-source branch in DownloadClip.
	// Nil-tolerated so absence of voiceover wiring never crashes the chain.
	VoiceoverRepo *assets.VoiceoversRepository
	// ImagesRepo enables the "source=images" branch in ListClips.
	// Nil-tolerated; nil means GET /:source/clips for that source returns 400.
	ImagesRepo *assets.ImagesRepository
	// ArtifactSvc streams uploaded files through content-addressed drive.
	// Used by UploadVideoClip. Nil means POST /upload-video returns 500.
	ArtifactSvc *artifacts.Service
	// FolderMemSvc supports manifest regeneration heuristics.
	FolderMemSvc *foldermemory.Service
	// SearchSvc owns advanced multi-source clip search.
	// Wave 21 PR 10 (June 2026): type changed from
	// *appclipssearch.Service to *search.Aggregator — the
	// canonical Search capability SSOT. Field NAME kept to
	// minimise consumer churn. See
	// architecture/deprecations.yaml PR-SEARCH-LEGACY-CLIPSSEARCH.
	SearchSvc *search.Aggregator
	// ProcessRunner executes external subprocesses (ffprobe, mediainfo, etc.).
	ProcessRunner appassets.ProcessRunner
	// Dispatcher is the application port (NOT the concrete
	// *outbox.Dispatcher) for QDRANT-002 routing. When non-nil,
	// UpdateClip routes through port.EnqueueAndIndex instead of raw
	// repo.UpsertClip. Nil-tolerated for test fixtures.
	//
	// Depends on appclips.ClipIndexDispatcherPort to keep this
	// handler as thin transport per AGENTS.md Pattern 8 (API must
	// not import concrete infrastructure). The composition root
	// (`internal/app`) wires a clipsDispatcherAdapter that wraps
	// the concrete *outbox.Dispatcher.
	Dispatcher appclips.ClipIndexDispatcherPort
	// MutationsDispatcher is the canonical mutations.AssetMutationDispatcher
	// SSOT (QDRANT-002 PR7). When non-nil, ReprocessUseCase routes its
	// post-process media_assets UPSERT through port.EnqueueAndIndex.
	// Nil-tolerated for test fixtures and partial deploys (strict
	// fail-closed at composition root surfaces a 503 if production
	// wiring accidentally supplies nil).
	MutationsDispatcher mutations.AssetMutationDispatcher
	// SearchAggregator (S3d, June 2026): MANDATORY for FindDuplicates.
	// The HashQuery path fans out to all registered ClipHashSource
	// adapters via the providers.Registry. A nil value causes
	// FindDuplicates to return 503. Composition root wires this when
	// the providers.Registry + ClipHashSource adapters are available
	// (post-Freeze). The legacy direct-repo loop was removed in
	// P0.4 / PR-CLIP-DEDUP-MIGRATION (June 2026).
	SearchAggregator *providers.SearchAggregator
	// ReuploadUC (P0.5, June 2026): the ReuploadClip use case.
	// Extracted from clip_action.go to keep the API layer thin
	// (AGENTS.md Pattern 8). MANDATORY — nil causes ReuploadClip
	// to return 500. Composition root wires this via
	// appclips.NewReuploadUseCase(...).
	ReuploadUC *appclips.ReuploadUseCase
	// EnrichUC, when non-nil, is shared with the worker
	// (`media.enrich`) registered at composition time. S1a
	// (June 2026) lifts the use-case construction out of the
	// handler so the worker and the handler reuse the same
	// instance — the worker was previously orphaned because the
	// handler constructed the use case internally. Nil-tolerated
	// for test fixtures and partial deploys: when nil, NewHandler
	// constructs a local copy preserving pre-lift behaviour.
	EnrichUC *appclips.EnrichUseCase
	// BulkUploadWorker handles the bulk_upload_youtube_clips job.
	BulkUploadWorker *appclips.BulkUploadWorker
	// ClipOpsService owns reconcile / cleanup / verify / fix-hash orchestration.
	ClipOpsService *appclips.ClipOpsService
}

// Handler owns every clip-related HTTP method. One receiver per method;
// no nested struct fan-out.
type Handler struct {
	// PR8 (June 2026): Idempotency is the reusable Gin idempotency
	// middleware (constructed once at server boot via NewHandler →
	// WireAssets → BuildRepoBundle.IdempotencyStore). Nil-tolerated so
	// test fixtures can opt out. Only WRITE routes (POST/PUT/PATCH/DELETE
	// on /clips/* and the upload/bulk routes) install it — READ routes
	// fall through unchanged.
	Idempotency gin.HandlerFunc

	// assetRepo (Split 1 leftover, June 2026): retained on Handler
	// because clip_action.go::FindDuplicates (Action cluster, Split 3
	// = future Commit) still references h.assetRepo directly. SearchHandler
	// receives the same *asset.Repository instance via SearchDeps.AssetRepo;
	// both pointers stay valid. This mirror field will be removed when Action
	// is split out and FindDuplicates migrates onto an ActionReceiver.
	assetRepo      asset.Repository
	deletionSvc    *deletion.DeletionService
	driveUploader  *drive.Uploader
	mediaProcessor asset.Processor
	assetTreeSvc   *assettree.Service
	metaWriter     *semantic.MetadataWriter
	clipIndexer    *clipindexer.Service
	jobsSvc        jobservice.Service
	cfg            *config.Config
	log            *zap.Logger
	// artifactSvc mirrors Deps.ArtifactSvc. Same late-binding semantics.
	artifactSvc  *artifacts.Service
	folderMemSvc *foldermemory.Service
	// processRunner mirrors Deps.ProcessRunner.
	processRunner appassets.ProcessRunner
	// dispatcher mirrors Deps.Dispatcher (now the application port
	// type, see ClipIndexDispatcherPort for the rationale). Nil-
	// tolerated for test fixtures and partial deployments.
	dispatcher appclips.ClipIndexDispatcherPort
	// mutationsDispatcher mirrors Deps.MutationsDispatcher. PR 7
	// (June 2026): the canonical SSOT for the ReprocessUseCase
	// media_assets write.
	mutationsDispatcher mutations.AssetMutationDispatcher
	// searchAggregator mirrors Deps.SearchAggregator (S3d, June 2026).
	searchAggregator *providers.SearchAggregator
	// bulkUploadWorker handles the bulk_upload_youtube_clips job.
	bulkUploadWorker *appclips.BulkUploadWorker
	// clipOpsService owns the high-level clip ops endpoints.
	clipOpsService *appclips.ClipOpsService

	// search (Split 1, June 2026, override ADR 0009): the Search sub-handler
	// owns the 4 clip-search routes (GET /:source/clips, GET /:source/clips/:id,
	// POST /:source/clips/:id/status, POST /search/advanced). The 5 deps it
	// consumes (SourceResolver, AssetRepo, VoiceoverRepo, ImagesRepo,
	// SearchSvc) are extracted from Deps by NewHandler into a SearchDeps
	// shape. Subsequent splits (Ingest/Action/Ops/BulkUpload) follow the
	// same pattern in their respective atomic commits.
	search *SearchHandler
	// ingest (Split 2, June 2026, override ADR 0009): the Ingest
	// sub-handler owns the 3 ingest routes (POST /:source/clips,
	// PATCH /:source/clips/:id, POST /upload-video). The 12 deps it
	// consumes are extracted from Deps by NewHandler into an
	// IngestDeps shape. ActionReceiver (Split 3) follows the
	// same pattern.
	ingest *IngestHandler
	// ops (Split 4, June 2026, override ADR 0009): the Ops sub-handler
	// owns the 20 ops routes (15 write+idem + 5 read = folder/tree/
	// bulk-tags/verify/cleanup/reconcile/fix-hash/trash/delete/reprocess/
	// reindex/enrich). The 12 deps it consumes are extracted from Deps
	// by NewHandler into an OpsDeps shape. BulkUploadReceiver (Split 5)
	// follows the same pattern.
	ops *OpsHandler

	// Use cases — business logic extracted from handlers
	reprocessUC *appclips.ReprocessUseCase
	downloadUC  *appclips.DownloadUseCase
	bulkTagsUC  *appclips.BulkTagsUseCase
	enrichUC    *appclips.EnrichUseCase
	reuploadUC  *appclips.ReuploadUseCase
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

	// Lift use-case construction out of the struct literal so the same
	// instances can be shared with the Ops sub-handler (Split 4). Single
	// source of construction — both the orchestrator mirror fields and
	// the OpsDeps shape point at the same *appclips.* instances.
	enrichUC := enrichUCOrLocal(d.EnrichUC, d.AssetRepo, d.ClipIndexer, d.MetaWriter, d.Log)
	reprocessUC := appclips.NewReprocessUseCase(d.AssetRepo, d.MediaProcessor, d.MutationsDispatcher)
	downloadUC := appclips.NewDownloadUseCase(d.AssetRepo, d.VoiceoverRepo)
	bulkTagsUC := appclips.NewBulkTagsUseCase(d.SourceResolver, d.AssetTreeSvc)
	return &Handler{
		Idempotency:         idem,
		assetRepo:           d.AssetRepo,
		deletionSvc:         d.DeletionSvc,
		driveUploader:       d.DriveUploader,
		mediaProcessor:      d.MediaProcessor,
		assetTreeSvc:        d.AssetTreeSvc,
		metaWriter:          d.MetaWriter,
		clipIndexer:         d.ClipIndexer,
		jobsSvc:             d.JobsSvc,
		cfg:                 d.Cfg,
		log:                 d.Log,
		artifactSvc:         d.ArtifactSvc,
		folderMemSvc:        d.FolderMemSvc,
		processRunner:       d.ProcessRunner,
		dispatcher:          d.Dispatcher,
		mutationsDispatcher: d.MutationsDispatcher,
		searchAggregator:    d.SearchAggregator,
		bulkUploadWorker:    d.BulkUploadWorker,
		clipOpsService:      d.ClipOpsService,
		// Split 1 (June 2026, override ADR 0009): Search sub-handler
		// owns 4 routes. The 5 Deps fields below move into the
		// SearchDeps shape; orchestrator Deps struct keeps the public
		// API unchanged so the composition root signature is non-breaking.
		search: NewSearchHandler(SearchDeps{
			SourceResolver: d.SourceResolver,
			AssetRepo:      d.AssetRepo,
			VoiceoverRepo:  d.VoiceoverRepo,
			ImagesRepo:     d.ImagesRepo,
			SearchSvc:      d.SearchSvc,
		}),
		// Split 2 (June 2026, override ADR 0009): Ingest sub-handler
		// owns 3 write+idem routes. The 12 Deps fields below move into
		// the IngestDeps shape; orchestrator Deps struct keeps the
		// public API unchanged so the composition root signature is
		// non-breaking. EnrichUC pointer is shared with the
		// orchestrator's local (single source of construction).
		ingest: NewIngestHandler(IngestDeps{
			Dispatcher:     d.Dispatcher,
			AssetTreeSvc:   d.AssetTreeSvc,
			JobsSvc:        d.JobsSvc,
			SourceResolver: d.SourceResolver,
			ArtifactSvc:    d.ArtifactSvc,
			DriveUploader:  d.DriveUploader,
			ProcessRunner:  d.ProcessRunner,
			Cfg:            d.Cfg,
			ClipIndexer:    d.ClipIndexer,
			MetaWriter:     d.MetaWriter,
			EnrichUC:       enrichUC,
			Log:            d.Log,
		}),
		// Split 4 (June 2026, override ADR 0009): Ops sub-handler
		// owns 20 routes (15 write+idem + 5 read). The 12 Deps
		// fields below move into the OpsDeps shape; orchestrator
		// Deps struct keeps the public API unchanged so the
		// composition root signature is non-breaking. EnrichUC,
		// ReprocessUC, BulkTagsUC are shared with the orchestrator
		// mirror via the local instances above (single source of
		// construction).
		ops: NewOpsHandler(OpsDeps{
			DeletionSvc:    d.DeletionSvc,
			ClipOpsService: d.ClipOpsService,
			SourceResolver: d.SourceResolver,
			AssetTreeSvc:   d.AssetTreeSvc,
			FolderMemSvc:   d.FolderMemSvc,
			DriveUploader:  d.DriveUploader,
			BulkTagsUC:     bulkTagsUC,
			ReprocessUC:    reprocessUC,
			EnrichUC:       enrichUC,
			JobsSvc:        d.JobsSvc,
			ClipIndexer:    d.ClipIndexer,
			Log:            d.Log,
		}),

		// Initialize use cases
		// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): pass the
		// SSOT mutations dispatcher to ReprocessUseCase so the
		// post-process media_assets UPSERT routes through the canonical
		// outbox+tx writer (QDRANT-002 atomicity invariant).
		reprocessUC: reprocessUC,
		downloadUC:  downloadUC,
		bulkTagsUC:  bulkTagsUC,
		// S1a (June 2026): when the composition root supplies a
		// shared EnrichUC, reuse it — the worker
		// (clips.MediaEnrichWorker) is wired with the same
		// instance. When nil (test fixture, partial deploy), the
		// handler constructs a local copy so pre-lift behaviour
		// is preserved bit-for-bit. The fallback exists so
		// legacy test fixtures that construct `NewHandler`
		// without `Deps.EnrichUC` don't break.
		enrichUC: enrichUC,
		// P0.5 (June 2026): ReuploadUseCase wired from Deps;
		// nil tolerated for test fixtures (returns 500 at call time).
		reuploadUC: d.ReuploadUC,
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
// Pre-Split-1 this method was the authoritative implementation;
// post-Split-1 the impl lives on *SearchHandler (vector of cluster
// ownership) and this thin dispatcher preserves byte-compatible
// behaviour for callers in clip_update.go, clip_enrich.go,
// folder.go, folder_tree.go.
func (h *Handler) repoForSource(source string) *assets.ClipsRepository {
	if h.search == nil {
		return nil
	}
	return h.search.repoForSource(source)
}

// ──────────────────────────────────────────────────────────────────────
// Thin delegators (Split 2, June 2026) — Pattern mirrors Split 1's
// repoForSource thin-dispatcher above. These preserve byte-compatible
// behaviour for callers that wire Handler methods directly as
// gin.HandlerFunc (e.g. dispatcher_fail_closed_test.go: g.POST("/clips",
// h.CreateClip)). The canonical impl lives on *IngestHandler.
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
// Thin delegators (Split 4, June 2026) — same pattern as Split 2.
// Canonical impl lives on *OpsHandler (ops.go::RegisterRoutes). These
// preserve byte-compatible behaviour for callers that wire Handler
// methods directly as gin.HandlerFunc (legacy test fixtures, sources
// sub-handler forwarding routes).
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

// ReprocessClip thin-delegates to OpsHandler.ReprocessClip.
func (h *Handler) ReprocessClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.ReprocessClip(c)
}

// ReindexClip thin-delegates to OpsHandler.ReindexClip.
func (h *Handler) ReindexClip(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.ReindexClip(c)
}

// BulkAddTags thin-delegates to OpsHandler.BulkAddTags.
func (h *Handler) BulkAddTags(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.BulkAddTags(c)
}

// BulkRemoveTags thin-delegates to OpsHandler.BulkRemoveTags.
func (h *Handler) BulkRemoveTags(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.BulkRemoveTags(c)
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

// EnrichMedia thin-delegates to OpsHandler.EnrichMedia.
func (h *Handler) EnrichMedia(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.EnrichMedia(c)
}

// BatchReindex thin-delegates to OpsHandler.BatchReindex.
func (h *Handler) BatchReindex(c *gin.Context) {
	if h.ops == nil {
		apiutil.Error(c, 503, "ops sub-handler not wired")
		return
	}
	h.ops.BatchReindex(c)
}

// EnrichAndIndexClip thin-delegator preserves the legacy pre-Split-4
// public surface. Callers in batch / mixin helpers expect this method
// on the clips.Handler. Ops is the new owner of the substantive
// implementation; the orchestrator-level thin delegator forwards to
// it. Since Ops and the orchestrator mirror both hold the SAME
// *EnrichUseCase instance (single source of construction via
// enrichUCOrLocal in NewHandler), a fallback branch would be
// functionally identical to the forward — we drop it as dead code
// per Step 5 Split 4 code-review.
func (h *Handler) EnrichAndIndexClip(ctx context.Context, clip *asset.Asset, source string) {
	if h.ops == nil {
		return
	}
	h.ops.EnrichAndIndexClip(ctx, clip, source)
}

// ──────────────────────────────────────────────────────────────────────
// Action cluster (Split 3, not yet landed) — these 3 routes stay on
// *Handler until Split 3 = ActionReceiver lands. Per the discovery
// matrix DownloadClip / ReuploadClip / FindDuplicates are Action
// (consume downloadUC / reuploadUC / assetRepo + searchAggregator
// respectively). They are NOT in OpsDeps by design.
// ──────────────────────────────────────────────────────────────────────

func (h *Handler) driveRootForSource(source string) (string, string) {
	spec, ok := map[string]struct {
		root   func(*config.Config) string
		marker string
	}{
		"clips": {
			root:   func(cfg *config.Config) string { return cfg.Drive.ClipsFolder() },
			marker: "/clips/",
		},
		"artlist": {
			root:   func(cfg *config.Config) string { return cfg.Drive.ArtlistFolder() },
			marker: "/artlist/",
		},
		"stock": {
			root:   func(cfg *config.Config) string { return cfg.Drive.StockFolder() },
			marker: "/stock/",
		},
	}[artifacts.CanonicalSource(source)]
	if !ok {
		return "", ""
	}
	return spec.root(h.cfg), spec.marker
}

// RegisterJobHandlers wires up the bulk-upload worker. SourcesHandler's
// RegisterJobHandlers delegates here.
//
// Split 4 (June 2026): HandleBulkUploadYouTubeClipsJob stays on
// *Handler (clip_ops_handlers.go) for the bulk_upload_youtube_clips
// type — Split 5 = BulkUploadTransport will move it onto an
// *BulkUploadHandler receiver alongside BulkUploadYouTubeClips.
func (h *Handler) RegisterJobHandlers() error {
	if h.jobsSvc == nil {
		return nil
	}
	return h.jobsSvc.RegisterHandler("bulk_upload_youtube_clips", h.HandleBulkUploadYouTubeClipsJob)
}

// PR8 helper: idemWriter returns h.Idempotency if set, else a no-op
// pass-through handler. Used only for Write routes (POST/PUT/PATCH/DELETE);
// read routes never need idempotency.
func (h *Handler) idemWriter() gin.HandlerFunc {
	if h.Idempotency == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return h.Idempotency
}

// RegisterRoutes mounts the entire clip-route surface on the supplied
// gin router group. SourcesHandler keeps the Voiceover, SoundEffect,
// diagnostics, and Drive-move/fold/sync-route families and delegates
// everything else to h.clips.
//
// PR8 (June 2026): write routes (POST/PUT/PATCH/DELETE) install
// h.Idempotency BEFORE the handler — when present — so Idempotency-Key
// replay, body-hash conflict (422), and in-flight (409) semantics
// apply uniformly. Read routes are unchanged.
//
// Splits 1/2/4 (June 2026, override ADR 0009): Search/Ingest/Ops
// sub-handlers own their idiomatic-route band via per-cluster
// RegisterRoutes. Each sub-handler receiver receives only the deps
// it consumes; the orchestrator installs idem ONCE and forwards.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()
	// Ingest sub-handler (Split 2, June 2026, override ADR 0009):
	//   POST  /:source/clips           -> CreateClip      (write+idem)
	//   PATCH /:source/clips/:id       -> UpdateClip      (write+idem)
	//   POST  /upload-video            -> UploadVideoClip (write+idem)
	h.ingest.RegisterRoutes(r, idem)
	// Search sub-handler (Split 1, June 2026) owns these 4 routes:
	//   GET  /:source/clips               -> ListClips       (read, no idem)
	//   GET  /:source/clips/:id           -> GetClip         (read, no idem)
	//   POST /:source/clips/:id/status    -> ClipStatus      (write+idem)
	//   POST /search/advanced             -> AdvancedSearch  (write+idem)
	h.search.RegisterRoutes(r, idem)
	// Ops sub-handler (Split 4, June 2026, override ADR 0009) owns
	// these 20 routes (15 write+idem + 5 read):
	//   GET  /:source/folders                          -> ListFolders          (read)
	//   GET  /:source/folders/:id                      -> FolderStatus         (read)
	//   GET  /:source/folders/:id/children             -> GetFolderChildren    (read)
	//   GET  /:source/tree                             -> GetTree              (read)
	//   GET  /:source/breadcrumb                       -> GetBreadcrumb        (read)
	//   POST /:source/clips/:id/verify                 -> VerifyClip           (write+idem)
	//   POST /:source/clips/:id/fix-hash               -> HandleFixHash        (write+idem)
	//   POST /:source/clips/:id/trash                  -> TrashClip            (write+idem)
	//   POST /:source/clips/:id/delete                 -> DeleteClip           (write+idem)
	//   POST /:source/clips/:id/reprocess              -> ReprocessClip        (write+idem)
	//   POST /:source/clips/:id/reindex                -> ReindexClip          (write+idem)
	//   POST /:source/bulk/tags/add                    -> BulkAddTags          (write+idem)
	//   POST /:source/bulk/tags/remove                 -> BulkRemoveTags       (write+idem)
	//   POST /:source/reconcile                        -> Reconcile            (write+idem)
	//   POST /:source/cleanup                          -> Cleanup              (write+idem)
	//   POST /:source/folders/:id/manifest             -> RegenerateManifest   (write+idem)
	//   POST /:source/folders/:id/trash                -> TrashFolder          (write+idem)
	//   POST /:source/folders/:id/delete               -> DeleteFolder         (write+idem)
	//   POST /enrich                                   -> EnrichMedia          (write+idem)
	//   POST /enrich/batch                             -> BatchReindex         (write+idem)
	h.ops.RegisterRoutes(r, idem)

	// Action cluster routes stay on *Handler until Split 3 lands:
	//   POST /:source/clips/:id/download           -> DownloadClip           (write+idem)
	//   POST /:source/clips/:id/duplicates        -> FindDuplicates         (write+idem)
	//   POST /:source/clips/:id/reupload          -> ReuploadClip           (write+idem)
	r.POST("/:source/clips/:id/download", idem, h.DownloadClip)
	r.POST("/:source/clips/:id/duplicates", idem, h.FindDuplicates)
	r.POST("/:source/clips/:id/reupload", idem, h.ReuploadClip)
}

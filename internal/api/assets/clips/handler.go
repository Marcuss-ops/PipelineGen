// Package clips hosts the unified HTTP handler that owns every clip-related
// endpoint. PR-A Phase 4 BULK consolidation: a single Handler struct carries
// the full dep surface and exposes every method previously scattered
// across handler_sources_clip_*.go in the flat sources package.
//
// Sub-handler fan-out (DeleteHandler, SearchHandler) is replaced by
// receivers on *Handler — there is no longer a need for nested structs.
// SourcesHandler keeps a single *clips.Handler field and delegates each
// clip-route registration to clips.Handler.{CreateClip, GetClip, ...}.
package clips

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/manifest"
	appclipssearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/clipssearch"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// Deps is the constructor bag for Handler.
type Deps struct {
	SourceResolver *artifacts.SourceResolver
	AssetRepo      asset.Repository
	ClipsRepo      *assets.ClipsRepository
	StockRepo      *assets.ClipsRepository
	ArtlistRepo    *assets.ClipsRepository
	DeletionSvc    *deletion.DeletionService
	DriveUploader  *drive.Uploader
	MediaProcessor asset.Processor
	AssetTreeSvc   *assettree.Service
	MetaWriter     *semantic.MetadataWriter
	ClipIndexer    *clipindexer.Service
	JobsSvc        jobservice.Service
	Cfg            *config.Config
	Log            *zap.Logger
	VoiceoverRepo  *assets.VoiceoversRepository
	ImagesRepo     *assets.ImagesRepository
	ArtifactSvc    *artifacts.Service
	FolderMemSvc   *foldermemory.Service
	SearchSvc      *appclipssearch.Service
	ProcessRunner  appassets.ProcessRunner
	Dispatcher     appclips.ClipIndexDispatcherPort

	// BulkUploadWorker is the canonical port-based worker for the
	// "bulk_upload_youtube_clips" job. W14 PR2 slice 3 (June 2026):
	// Nil-tolerated so test fixtures can opt out.
	BulkUploadWorker *appclips.BulkUploadWorker

	// ClipOpsService owns the orchestration behind the HTTP verbs
	// Reconcile / Cleanup / VerifyClip. PR 2 (June 2026) cutover:
	// Nil-tolerated for legacy fixtures.
	ClipOpsService *appclips.ClipOpsService

	// ManifestService is the canonical AssetManifestService —
	// PR 6/PR 7 (codex/asset-manifest-cutover) cutover. The pre-PR7
	// helper-method + cumulative Drive-adapter pair
	// is REMOVED; clip_upload.go step 10b calls this directly.
	// Nil-tolerated for legacy fixtures (manifest writes skipped).
	ManifestService manifest.Service
}

// Handler owns every clip-related HTTP method.
type Handler struct {
	Idempotency gin.HandlerFunc

	sourceResolver *artifacts.SourceResolver
	assetRepo      asset.Repository
	clipsRepo      *assets.ClipsRepository
	stockRepo      *assets.ClipsRepository
	artlistRepo    *assets.ClipsRepository
	deletionSvc    *deletion.DeletionService
	driveUploader  *drive.Uploader
	mediaProcessor asset.Processor
	assetTreeSvc   *assettree.Service
	metaWriter     *semantic.MetadataWriter
	clipIndexer    *clipindexer.Service
	jobsSvc        jobservice.Service
	cfg            *config.Config
	log            *zap.Logger
	voiceoverRepo  *assets.VoiceoversRepository
	imagesRepo     *assets.ImagesRepository
	artifactSvc    *artifacts.Service
	folderMemSvc   *foldermemory.Service
	searchSvc      *appclipssearch.Service
	processRunner  appassets.ProcessRunner
	dispatcher     appclips.ClipIndexDispatcherPort
	manifestService manifest.Service

	reprocessUC      *appclips.ReprocessUseCase
	downloadUC       *appclips.DownloadUseCase
	bulkTagsUC       *appclips.BulkTagsUseCase
	enrichUC         *appclips.EnrichUseCase
	bulkUploadWorker *appclips.BulkUploadWorker
	clipOpsService   *appclips.ClipOpsService
}

// NewHandler constructs the unified Handler. May be called before every
// dependency is wired — individual methods that need a missing dep will
// internal-error handle it.
func NewHandler(d Deps, idempotencyMiddleware gin.HandlerFunc) *Handler {
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}
	return &Handler{
		Idempotency:      idem,
		sourceResolver:   d.SourceResolver,
		assetRepo:        d.AssetRepo,
		clipsRepo:        d.ClipsRepo,
		stockRepo:        d.StockRepo,
		artlistRepo:      d.ArtlistRepo,
		deletionSvc:      d.DeletionSvc,
		driveUploader:    d.DriveUploader,
		mediaProcessor:   d.MediaProcessor,
		assetTreeSvc:     d.AssetTreeSvc,
		metaWriter:       d.MetaWriter,
		clipIndexer:      d.ClipIndexer,
		jobsSvc:          d.JobsSvc,
		cfg:              d.Cfg,
		log:              d.Log,
		voiceoverRepo:    d.VoiceoverRepo,
		imagesRepo:       d.ImagesRepo,
		artifactSvc:      d.ArtifactSvc,
		folderMemSvc:     d.FolderMemSvc,
		searchSvc:        d.SearchSvc,
		processRunner:    d.ProcessRunner,
		dispatcher:       d.Dispatcher,
		manifestService:  d.ManifestService,
		bulkUploadWorker: d.BulkUploadWorker,
		clipOpsService:   d.ClipOpsService,

		reprocessUC: appclips.NewReprocessUseCase(d.AssetRepo, d.MediaProcessor),
		downloadUC:  appclips.NewDownloadUseCase(d.AssetRepo, d.VoiceoverRepo),
		bulkTagsUC:  appclips.NewBulkTagsUseCase(d.SourceResolver, d.AssetTreeSvc),
		enrichUC:    appclips.NewEnrichUseCase(d.AssetRepo, d.ClipIndexer, d.MetaWriter, d.Log),
	}
}

// repoForSource resolves a clip source to its canonical repository.
func (h *Handler) repoForSource(source string) *assets.ClipsRepository {
	if h.sourceResolver == nil {
		return nil
	}
	return h.sourceResolver.ResolveRepo(source)
}

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

// RegisterJobHandlers wires up the bulk-upload worker via the
// application-layer BulkUploadWorker.
func (h *Handler) RegisterJobHandlers() error {
	if h.jobsSvc == nil {
		return nil
	}
	if h.bulkUploadWorker == nil {
		return nil
	}
	return h.jobsSvc.RegisterHandler("bulk_upload_youtube_clips", h.bulkUploadWorker.HandleJob)
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
// PR8 (June 2026): write routes (POST/PUT/PATCH/DELETE) install
// h.Idempotency BEFORE the handler.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()
	r.POST("/:source/clips", idem, h.CreateClip)
	r.GET("/:source/clips", h.ListClips)
	r.GET("/:source/clips/:id", h.GetClip)
	r.PATCH("/:source/clips/:id", idem, h.UpdateClip)
	r.POST("/:source/clips/:id/status", idem, h.ClipStatus)
	r.POST("/:source/clips/:id/verify", idem, h.VerifyClip)
	r.POST("/:source/clips/:id/trash", idem, h.TrashClip)
	r.POST("/:source/clips/:id/delete", idem, h.DeleteClip)
	r.POST("/:source/clips/:id/download", idem, h.DownloadClip)
	r.POST("/:source/clips/:id/duplicates", idem, h.FindDuplicates)
	r.POST("/:source/clips/:id/reupload", idem, h.ReuploadClip)
	r.POST("/:source/clips/:id/reprocess", idem, h.ReprocessClip)
	r.POST("/:source/clips/:id/reindex", idem, h.ReindexClip)

	r.POST("/:source/bulk/tags/add", idem, h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", idem, h.BulkRemoveTags)
	r.POST("/:source/reconcile", idem, h.Reconcile)
	r.POST("/:source/cleanup", idem, h.Cleanup)

	r.GET("/:source/folders", h.ListFolders)
	r.GET("/:source/folders/:id", h.FolderStatus)
	r.POST("/:source/folders/:id/manifest", idem, h.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", idem, h.TrashFolder)
	r.POST("/:source/folders/:id/delete", idem, h.DeleteFolder)
	r.GET("/:source/folders/:id/children", h.GetFolderChildren)
	r.GET("/:source/tree", h.GetTree)
	r.GET("/:source/breadcrumb", h.GetBreadcrumb)

	r.POST("/enrich", idem, h.EnrichMedia)
	r.POST("/enrich/batch", idem, h.BatchReindex)
	r.POST("/upload-video", idem, h.UploadVideoClip)
	r.POST("/search/advanced", idem, h.AdvancedSearch)
}

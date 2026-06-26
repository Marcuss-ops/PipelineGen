// Package clips hosts the unified HTTP handler that owns every clip-related
// endpoint. PR-A Phase 4 BULK consolidation: a single Handler struct carries
// the canonical 22-field dep surface and exposes every method previously
// scattered across handler_sources_clip_*.go in the flat sources package.
//
// W14-PR2 slice 5 (June 2026): the previous handler held 5 direct
// `internal/infrastructure/*` references (*assets.ClipsRepository,
// *semantic.MetadataWriter, *clipindexer.Service, *drive.Uploader,
// *foldermemory.Service) plus *config.Config + *artifacts.SourceResolver +
// *assettree.Service that crossed the boundary via raw imports. Per
// AGENTS.md Pattern 0 + Pattern 8, every infra-shaped dep is now a typed
// port declared in internal/application/clips/ports.go and wired by
// the composition root via `newClipsAdapterBundle` (see
// internal/app/module_media.go::WireAssets). This file has zero
// internal/infrastructure/* imports — Check 19 hard-fail gate passes.
//
// Sub-handler fan-out (DeleteHandler, SearchHandler) is replaced by
// receivers on *Handler — there is no longer a need for nested structs.
// SourcesHandler keeps a single *clips.Handler field and delegates each
// clip-route registration to clips.Handler.{CreateClip, GetClip, ...}.
package clips

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	appclipssearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/clipssearch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/restore"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Deps is the constructor bag for Handler. Keeping deps in a struct
// rather than 22 positional arguments makes wiring sites readable and
// future dep additions non-breaking.
//
// W14-PR2 slice 5: every infra reaching through slot is now a typed
// port. The platform-Deps entries that stay concrete (DeletionSvc,
// MediaProcessor, SearchSvc, ProcessRunner, JobsSvc, AssetRepo,
// ArtifactSvc, Dispatcher) accept either domain interfaces
// (asset.Repository, asset.Processor, job.Service, appassets.ProcessRunner)
// or live in the application layer (deletion.DeletionService,
// clipssearch.Service). The ONLY concrete types that retained direct
// infra roundtrips are gone — `clipsapi.Deps` is now zero-infra.
type Deps struct {
	// Typed ports (12 of them) — composition root constructs via
	// newClipsAdapterBundle and assigns the pre-built adapters here.
	SourceResolver appclips.SourceResolverPort
	ClipsRepo      appclips.ClipRepositoryPort
	StockRepo      appclips.ClipRepositoryPort
	ArtlistRepo    appclips.ClipRepositoryPort
	VoiceoverRepo  appclips.VoiceoverRepositoryPort
	ImagesRepo     appclips.ImageRepositoryPort
	DriveUploader  appclips.ClipDriveUploaderPort
	MetaWriter     appclips.ClipMetaWriterPort
	ClipIndexer    appclips.ClipIndexerPort
	FolderMemSvc   appclips.ClipFolderMemoryPort
	HashSvc        appclips.ClipHashPort
	Cfg            appclips.ClipConfigPort
	TreeBuilderSvc appclips.ClipTreeBuilderPort

	// Domain / application layers (no infra roundtrip)
	AssetRepo      asset.Repository
	MediaProcessor asset.Processor
	AssetTreeSvc   *assettree.Service
	SearchSvc      *appclipssearch.Service
	ProcessRunner  appassets.ProcessRunner
	JobsSvc        jobservice.Service
	DeletionSvc    *deletion.DeletionService

	// QDRANT-002 close-out (June 2026): two new application-level
	// wrappers around outbox.Dispatcher for explicit operator-driven
	// hard-delete + restore flows. Both are nil-tolerated (admin
	// tools + composition root SHOULD pass non-nil; tests use nil).
	HardDeleteSvc *deletion.Service
	RestoreSvc    *restore.Service

	// Platform (composition root concerns; nil-tolerated)
	Log *zap.Logger

	// Optional staging services (already application-layer)
	ArtifactSvc *artifacts.Service

	// Outbox dispatcher port for QDRANT-002 routing (W12 — already typified).
	Dispatcher appclips.ClipIndexDispatcherPort

	// Use cases (W14-PR2 slice 5: constructed in composition root
	// internal/app/module_media.go::WireAssets because each constructor
	// takes concrete infra types — adapters would be pointless
	// indirection here. The api layer stays zero-infra by treating
	// these as opaque already-wired bundles).
	ReprocessUC *appclips.ReprocessUseCase
	DownloadUC  *appclips.DownloadUseCase
	BulkTagsUC  *appclips.BulkTagsUseCase
	EnrichUC    *appclips.EnrichUseCase
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

	// Typed ports (mirror Deps, set once at NewHandler time).
	sourceResolver appclips.SourceResolverPort
	clipsRepo      appclips.ClipRepositoryPort
	stockRepo      appclips.ClipRepositoryPort
	artlistRepo    appclips.ClipRepositoryPort
	voiceoverRepo  appclips.VoiceoverRepositoryPort
	imagesRepo     appclips.ImageRepositoryPort
	driveUploader  appclips.ClipDriveUploaderPort
	metaWriter     appclips.ClipMetaWriterPort
	clipIndexer    appclips.ClipIndexerPort
	folderMemSvc   appclips.ClipFolderMemoryPort
	hashSvc        appclips.ClipHashPort
	cfg            appclips.ClipConfigPort
	treeBuilderSvc appclips.ClipTreeBuilderPort

	// Domain / application mirrors.
	artifactSvc     *artifacts.Service
	folderMemAppSvc appclips.ClipFolderMemoryPort // alias — same as folderMemSvc
	searchSvc       *appclipssearch.Service
	processRunner   appassets.ProcessRunner
	dispatcher      appclips.ClipIndexDispatcherPort

	// Domain interfaces (still concrete-pointer-backed but no infra import).
	assetRepo      asset.Repository
	mediaProcessor asset.Processor
	assetTreeSvc   *assettree.Service
	jobsSvc        jobservice.Service
	deletionSvc    *deletion.DeletionService
	log            *zap.Logger

	// QDRANT-002 close-out (June 2026): see Deps comments.
	hardDeleteSvc *deletion.Service
	restoreSvc    *restore.Service

	// Use cases — business logic extracted from handlers.
	// W14-PR2 slice 5: each use-case constructor receives the typed
	// ports where applicable. DownloadUseCase still takes the raw
	// voiceover concrete (separate Refactor; the use case signature is
	// internal/application/clips, not internal/api — Pattern 8 applies
	// to api only).
	reprocessUC *appclips.ReprocessUseCase
	downloadUC  *appclips.DownloadUseCase
	bulkTagsUC  *appclips.BulkTagsUseCase
	enrichUC    *appclips.EnrichUseCase
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
	return &Handler{
		Idempotency:     idem,
		sourceResolver:  d.SourceResolver,
		clipsRepo:       d.ClipsRepo,
		stockRepo:       d.StockRepo,
		artlistRepo:     d.ArtlistRepo,
		voiceoverRepo:   d.VoiceoverRepo,
		imagesRepo:      d.ImagesRepo,
		driveUploader:   d.DriveUploader,
		metaWriter:      d.MetaWriter,
		clipIndexer:     d.ClipIndexer,
		folderMemSvc:    d.FolderMemSvc,
		hashSvc:         d.HashSvc,
		cfg:             d.Cfg,
		treeBuilderSvc:  d.TreeBuilderSvc,
		artifactSvc:     d.ArtifactSvc,
		searchSvc:       d.SearchSvc,
		processRunner:   d.ProcessRunner,
		dispatcher:      d.Dispatcher,
		assetRepo:       d.AssetRepo,
		mediaProcessor:  d.MediaProcessor,
		assetTreeSvc:    d.AssetTreeSvc,
		jobsSvc:         d.JobsSvc,
		deletionSvc:     d.DeletionSvc,
		hardDeleteSvc:   d.HardDeleteSvc,
		restoreSvc:      d.RestoreSvc,
		log:             d.Log,
		folderMemAppSvc: d.FolderMemSvc,
		// Wire use cases constructed in the composition root.
		// W14-PR2 slice 5 (June 2026): use cases are constructed in
		// internal/app/module_media.go::WireAssets where the concrete
		// infra types are in scope. The api layer just assigns.
		reprocessUC: d.ReprocessUC,
		downloadUC:  d.DownloadUC,
		bulkTagsUC:  d.BulkTagsUC,
		enrichUC:    d.EnrichUC,
	}
}

// repoForSource resolves a clip source to its canonical repository.
// Returns the typed port (ClipRepositoryPort) — callers never see the
// concrete *assets.ClipsRepository, keeping the api layer zero-infra.
func (h *Handler) repoForSource(source string) appclips.ClipRepositoryPort {
	if h.sourceResolver == nil {
		return nil
	}
	return h.sourceResolver.ResolveRepo(source)
}

// driveRootForSource resolves Drive root folder + path marker using the
// typed ClipConfigPort (no *config.Config dependency here).
func (h *Handler) driveRootForSource(source string) (string, string) {
	spec, ok := map[string]struct {
		root   func(appclips.ClipConfigPort) string
		marker string
	}{
		"clips": {
			root:   func(cfg appclips.ClipConfigPort) string { return cfg.ClipsDriveFolder() },
			marker: "/clips/",
		},
		"artlist": {
			root:   func(cfg appclips.ClipConfigPort) string { return cfg.ArtlistDriveFolder() },
			marker: "/artlist/",
		},
		"stock": {
			root:   func(cfg appclips.ClipConfigPort) string { return cfg.StockDriveFolder() },
			marker: "/stock/",
		},
	}[artifacts.CanonicalSource(source)]
	if !ok {
		return "", ""
	}
	if h.cfg == nil {
		return "", ""
	}
	return spec.root(h.cfg), spec.marker
}

// RegisterJobHandlers wires up the bulk-upload worker. SourcesHandler's
// RegisterJobHandlers delegates here.
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
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	idem := h.idemWriter()
	// Clip-level endpoints
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

	// Source-level bulk actions
	r.POST("/:source/bulk/tags/add", idem, h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", idem, h.BulkRemoveTags)
	r.POST("/:source/reconcile", idem, h.Reconcile)
	r.POST("/:source/cleanup", idem, h.Cleanup)

	// Folders + tree (writes only)
	r.GET("/:source/folders", h.ListFolders)
	r.GET("/:source/folders/:id", h.FolderStatus)
	r.POST("/:source/folders/:id/manifest", idem, h.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", idem, h.TrashFolder)
	r.POST("/:source/folders/:id/delete", idem, h.DeleteFolder)
	r.GET("/:source/folders/:id/children", h.GetFolderChildren)
	r.GET("/:source/tree", h.GetTree)
	r.GET("/:source/breadcrumb", h.GetBreadcrumb)

	// Cross-cutting actions on existing clip contexts
	r.POST("/enrich", idem, h.EnrichMedia)
	r.POST("/enrich/batch", idem, h.BatchReindex)

	// QDRANT-002 close-out (June 2026): explicit operator-driven
	// hard-delete + restore (canonical write paths behind
	// outbox.Dispatcher.EnqueueAndHardDelete / EnqueueAndRestore).
	// Both routes are mounted under /:source so the source-aware
	// asset_id-resolve path matches unified-handler expectations;
	// the handlers themselves call into the new application-layer
	// services provided by Deps.HardDeleteSvc / Deps.RestoreSvc.
	r.POST("/:source/clips/:id/hard-delete", idem, h.HardDeleteClip)
	r.POST("/:source/clips/:id/restore", idem, h.RestoreClip)

	// Upload endpoints (multipart body bypasses body-hash; idempotency still
	// observes in-flight 409 + completed replay).
	r.POST("/upload-video", idem, h.UploadVideoClip)

	// Search endpoint
	r.POST("/search/advanced", idem, h.AdvancedSearch)
}

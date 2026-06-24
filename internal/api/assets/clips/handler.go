// Package clips hosts the unified HTTP handler that owns every clip-related
// endpoint. PR-A Phase 4 BULK consolidation: a single Handler struct carries
// the full 14-dep surface and exposes every method previously scattered
// across handler_sources_clip_*.go in the flat sources package.
//
// Sub-handler fan-out (DeleteHandler, SearchHandler) is replaced by
// receivers on *Handler — there is no longer a need for nested structs.
// SourcesHandler keeps a single *clips.Handler field and delegates each
// clip-route registration to clips.Handler.{CreateClip, GetClip, ...}.
//
// PG-005 (June 2026): every field that previously held a concrete
// internal/infrastructure/* pointer (config.Config, assets.ClipsRepository,
// drive.Uploader, semantic.MetadataWriter, clipindexer.Service,
// foldermemory.Service, assets.VoiceoversRepository, assets.ImagesRepository,
// artifacts.SourceResolver) is now a typed port declared in
// internal/application/clips/ports.go. Concrete adapters live in
// internal/app/clips_adapters.go. This file has zero
// internal/infrastructure/* imports (verified by Check 19 in
// scripts/ci-architectural-checks.sh).
package clips

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	appclipssearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/clipssearch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Deps is the constructor bag for Handler. Keeping deps in a struct
// rather than 14 positional arguments makes wiring sites readable and
// future dep additions non-breaking.
//
// PG-005 (June 2026): every infrastructure-shaped field is now a typed
// port. Production wiring in internal/app/module_assets.go calls
// newClipsAdapterBundle(...) and passes each port. Tests can pass nil
// for any subset and observe the matching `if h.xy != nil` short-
// circuit behaviour the handler code has long relied on.
type Deps struct {
	SourceResolver appclips.SourceResolverPort
	AssetRepo      asset.Repository
	ClipsRepo      appclips.ClipRepositoryPort
	StockRepo      appclips.ClipRepositoryPort
	ArtlistRepo    appclips.ClipRepositoryPort
	DeletionSvc    *deletion.DeletionService
	DriveUploader  appclips.ClipDriveUploaderPort
	MediaProcessor asset.Processor
	AssetTreeSvc   *assettree.Service
	MetaWriter     appclips.ClipMetaWriterPort
	ClipIndexer    appclips.ClipIndexerPort
	VectorStore    appclips.VectorStorePort
	JobsSvc        jobservice.Service
	Cfg            appclips.ClipConfigPort
	Log            *zap.Logger
	// VoiceoverRepo enables the voiceover-source branch in DownloadClip.
	// Nil-tolerated so absence of voiceover wiring never crashes the chain.
	VoiceoverRepo appclips.VoiceoverRepositoryPort
	// ImagesRepo enables the "source=images" branch in ListClips.
	// Nil-tolerated; nil means GET /:source/clips for that source returns 400.
	ImagesRepo appclips.ImageRepositoryPort
	// ArtifactSvc streams uploaded files through content-addressed drive.
	// Used by UploadVideoClip. Nil means POST /upload-video returns 500.
	ArtifactSvc *artifacts.Service
	// FolderMemSvc supports manifest regeneration heuristics. Empty-marker
	// port — the handler stores the dep but does not call any method.
	FolderMemSvc appclips.ClipFolderMemoryPort
	// SearchSvc owns advanced multi-source clip search.
	SearchSvc *appclipssearch.Service
	// ProcessRunner executes external subprocesses (ffprobe, mediainfo, etc.).
	ProcessRunner appassets.ProcessRunner
	// HashSvc computes MD5 hashes for bulk-upload workers. PG-005
	// (June 2026): was a bare hashutil.MD5File() call inside
	// bulk_upload_worker.go; now flows through a typed port so the
	// handler keeps zero infra reach-through. The adapter wraps
	// files.MD5File() at the composition root
	// (internal/app/clips_adapters.go::clipsHashAdapter).
	HashSvc appclips.ClipHashPort
	// TreeBuilderSvc builds an asset-tree node from a clip asset. PG-005
	// (June 2026): replaces the previous direct *assettree.Service dep
	// so bulk_tags.go's BulkTagsUseCase can reach the tree through a
	// typed port (and the use case drops its infrastructure import).
	TreeBuilderSvc appclips.ClipTreeBuilderPort
}

// Handler owns every clip-related HTTP method. One receiver per method;
// no nested struct fan-out.
type Handler struct {
	sourceResolver appclips.SourceResolverPort
	assetRepo      asset.Repository
	clipsRepo      appclips.ClipRepositoryPort
	stockRepo      appclips.ClipRepositoryPort
	artlistRepo    appclips.ClipRepositoryPort
	deletionSvc    *deletion.DeletionService
	driveUploader  appclips.ClipDriveUploaderPort
	mediaProcessor asset.Processor
	assetTreeSvc   *assettree.Service
	metaWriter     appclips.ClipMetaWriterPort
	clipIndexer    appclips.ClipIndexerPort
	jobsSvc        jobservice.Service
	cfg            appclips.ClipConfigPort
	log            *zap.Logger
	// voiceoverRepo is mirrored from Deps.VoiceoverRepo via NewHandler
	voiceoverRepo appclips.VoiceoverRepositoryPort
	// imagesRepo mirrors Deps.ImagesRepo. Same late-binding semantics.
	imagesRepo appclips.ImageRepositoryPort
	// artifactSvc mirrors Deps.ArtifactSvc. Same late-binding semantics.
	artifactSvc  *artifacts.Service
	folderMemSvc appclips.ClipFolderMemoryPort
	// searchSvc mirrors Deps.SearchSvc.
	searchSvc *appclipssearch.Service
	// processRunner mirrors Deps.ProcessRunner.
	processRunner appassets.ProcessRunner
	// hashSvc mirrors Deps.HashSvc. PG-005 (June 2026): typed port
	// that backs bulk_upload_worker.go's MD5 computation; drops the
	// previous bare hashutil import.
	hashSvc appclips.ClipHashPort
	// treeBuilderSvc mirrors Deps.TreeBuilderSvc. PG-005 (June 2026):
	// typed port that backs bulk_tags.go's BulkTagsUseCase so the
	// use case drops the *assettree.Service concrete + assets.AssetNode
	// imports; the converter lives in the composition-root adapter.
	treeBuilderSvc appclips.ClipTreeBuilderPort

	// Use cases — business logic extracted from handlers
	reprocessUC *appclips.ReprocessUseCase
	downloadUC  *appclips.DownloadUseCase
	bulkTagsUC  *appclips.BulkTagsUseCase
	enrichUC    *appclips.EnrichUseCase
}

// NewHandler constructs the unified Handler. May be called before every
// dependency is wired — individual methods that need a missing dep will
// internal-error handle it (preserved legacy behavior).
func NewHandler(d Deps) *Handler {
	return &Handler{
		sourceResolver: d.SourceResolver,
		assetRepo:      d.AssetRepo,
		clipsRepo:      d.ClipsRepo,
		stockRepo:      d.StockRepo,
		artlistRepo:    d.ArtlistRepo,
		deletionSvc:    d.DeletionSvc,
		driveUploader:  d.DriveUploader,
		mediaProcessor: d.MediaProcessor,
		assetTreeSvc:   d.AssetTreeSvc,
		metaWriter:     d.MetaWriter,
		clipIndexer:    d.ClipIndexer,
		jobsSvc:        d.JobsSvc,
		cfg:            d.Cfg,
		log:            d.Log,
		voiceoverRepo:  d.VoiceoverRepo,
		imagesRepo:     d.ImagesRepo,
		artifactSvc:    d.ArtifactSvc,
		folderMemSvc:   d.FolderMemSvc,
		searchSvc:      d.SearchSvc,
		processRunner:  d.ProcessRunner,
		hashSvc:        d.HashSvc,
		treeBuilderSvc: d.TreeBuilderSvc,

		// Initialize use cases
		// PG-005 (June 2026): the constructor parameter types are now
		// typed ports. The use-case logic is unchanged.
		reprocessUC: appclips.NewReprocessUseCase(d.AssetRepo, d.MediaProcessor),
		downloadUC:  appclips.NewDownloadUseCase(d.AssetRepo, d.VoiceoverRepo),
		bulkTagsUC:  appclips.NewBulkTagsUseCase(d.SourceResolver, d.TreeBuilderSvc),
		enrichUC:    appclips.NewEnrichUseCase(d.AssetRepo, d.ClipIndexer, d.VectorStore, d.MetaWriter, d.Log),
	}
}

// repoForSource resolves a clip source to its canonical repository.
// Standard clip sources are resolved through the shared source resolver.
func (h *Handler) repoForSource(source string) appclips.ClipRepositoryPort {
	if h.sourceResolver == nil {
		return nil
	}
	return h.sourceResolver.ResolveRepo(source)
}

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

// RegisterRoutes mounts the entire clip-route surface on the supplied
// gin router group. SourcesHandler keeps the Voiceover, SoundEffect,
// diagnostics, and Drive-move/fold/sync-route families and delegates
// everything else to h.clips.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	// Clip-level endpoints
	r.POST("/:source/clips", h.CreateClip)
	r.GET("/:source/clips", h.ListClips)
	r.GET("/:source/clips/:id", h.GetClip)
	r.PATCH("/:source/clips/:id", h.UpdateClip)
	r.POST("/:source/clips/:id/status", h.ClipStatus)
	r.POST("/:source/clips/:id/verify", h.VerifyClip)
	r.POST("/:source/clips/:id/trash", h.TrashClip)
	r.POST("/:source/clips/:id/delete", h.DeleteClip)
	r.POST("/:source/clips/:id/download", h.DownloadClip)
	r.POST("/:source/clips/:id/duplicates", h.FindDuplicates)
	r.POST("/:source/clips/:id/reupload", h.ReuploadClip)
	r.POST("/:source/clips/:id/reprocess", h.ReprocessClip)
	r.POST("/:source/clips/:id/reindex", h.ReindexClip)

	// Source-level bulk actions
	r.POST("/:source/bulk/tags/add", h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", h.BulkRemoveTags)
	r.POST("/:source/reconcile", h.Reconcile)
	r.POST("/:source/cleanup", h.Cleanup)

	// Folders + tree
	r.GET("/:source/folders", h.ListFolders)
	r.GET("/:source/folders/:id", h.FolderStatus)
	r.POST("/:source/folders/:id/manifest", h.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", h.TrashFolder)
	r.POST("/:source/folders/:id/delete", h.DeleteFolder)
	r.GET("/:source/folders/:id/children", h.GetFolderChildren)
	r.GET("/:source/tree", h.GetTree)
	r.GET("/:source/breadcrumb", h.GetBreadcrumb)

	// Cross-cutting actions on existing clip contexts
	r.POST("/enrich", h.EnrichMedia)
	r.POST("/enrich/batch", h.BatchReindex)

	// Upload endpoints
	r.POST("/upload-video", h.UploadVideoClip)

	// Search endpoint
	r.POST("/search/advanced", h.AdvancedSearch)
}

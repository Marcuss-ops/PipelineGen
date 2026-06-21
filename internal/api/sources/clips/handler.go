// Package clips hosts the unified HTTP handler that owns every clip-related
// endpoint. PR-A Phase 4 BULK consolidation: a single Handler struct carries
// the full 14-dep surface and exposes every method previously scattered
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

	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

// Deps is the constructor bag for Handler. Keeping deps in a struct
// rather than 14 positional arguments makes wiring sites readable and
// future dep additions non-breaking.
type Deps struct {
	AssetRepo      asset.Repository
	ClipsRepo      *assets.ClipsRepository
	StockRepo      *assets.ClipsRepository
	ArtlistRepo    *assets.ClipsRepository
	DeletionSvc    *media.DeletionService
	DriveUploader  *drive.Uploader
	MediaProcessor processor.Processor
	AssetTreeSvc   *assettree.Service
	MetaWriter     *semantic.MetadataWriter
	ClipIndexer    *clipindexer.Service
	VectorStore    *vectorstore.Service
	JobsSvc        *jobservice.Service
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
	// FolderMemSvc runs the legacy folder heuristic for manifest regen.
	// Used by RegenerateManifest. Nil means POST /folders/:id/manifest returns 500.
	FolderMemSvc *foldermemory.Service
}

// Handler owns every clip-related HTTP method. One receiver per method;
// no nested struct fan-out.
type Handler struct {
	assetRepo      asset.Repository
	clipsRepo      *assets.ClipsRepository
	stockRepo      *assets.ClipsRepository
	artlistRepo    *assets.ClipsRepository
	deletionSvc    *media.DeletionService
	driveUploader  *drive.Uploader
	mediaProcessor processor.Processor
	assetTreeSvc   *assettree.Service
	metaWriter     *semantic.MetadataWriter
	clipIndexer    *clipindexer.Service
	vectorStore    *vectorstore.Service
	jobsSvc        *jobservice.Service
	cfg            *config.Config
	log            *zap.Logger
	// voiceoverRepo is mirrored from Deps.VoiceoverRepo via NewHandler
	// or post-construction SetVoiceoverRepo. Both paths keep the same
	// repo reference; switching mid-process picks up the new repo on the
	// next handler invocation.
	voiceoverRepo *assets.VoiceoversRepository
	// imagesRepo mirrors Deps.ImagesRepo. Same late-binding semantics.
	imagesRepo *assets.ImagesRepository
	// artifactSvc mirrors Deps.ArtifactSvc. Same late-binding semantics.
	artifactSvc *artifacts.Service
	// folderMemSvc mirrors Deps.FolderMemSvc. Same late-binding semantics.
	folderMemSvc *foldermemory.Service
}

// NewHandler constructs the unified Handler. May be called before every
// dependency is wired — individual methods that need a missing dep will
// internal-error handle it (preserved legacy behavior).
func NewHandler(d Deps) *Handler {
	return &Handler{
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
		vectorStore:    d.VectorStore,
		jobsSvc:        d.JobsSvc,
		cfg:            d.Cfg,
		log:            d.Log,
		voiceoverRepo:  d.VoiceoverRepo,
		imagesRepo:     d.ImagesRepo,
		artifactSvc:    d.ArtifactSvc,
		folderMemSvc:   d.FolderMemSvc,
	}
}

// SetVoiceoverRepo is a post-construction setter for late-binding the
// voiceover repository. SourcesHandler.SetVoiceoverRepo delegates here.
func (h *Handler) SetVoiceoverRepo(repo *assets.VoiceoversRepository) {
	h.voiceoverRepo = repo
}

// SetImagesRepo is a post-construction setter for the images repository.
// Mirrors SourcesHandler.SetImagesRepo. clips.Handler reads it in
// ListClips for the "source=images" branch.
func (h *Handler) SetImagesRepo(repo *assets.ImagesRepository) {
	h.imagesRepo = repo
}

// SetArtifactSvc is a post-construction setter for the artifact service.
// Mirrors SourcesHandler.SetArtifactSvc. clips.Handler uses it in
// UploadVideoClip for content-addressed blob drive.
func (h *Handler) SetArtifactSvc(svc *artifacts.Service) {
	h.artifactSvc = svc
}

// SetFolderMemSvc is a post-construction setter for the folder memory
// service. Mirrors SourcesHandler.SetFolderMemSvc. clips.Handler uses it
// in RegenerateManifest for the folder heuristic.
func (h *Handler) SetFolderMemSvc(svc *foldermemory.Service) {
	h.folderMemSvc = svc
}

// resolveRepo standardizes source-string → sqliteclips.Repository dispatch.
// Returns nil for unknown sources (callers must validate before use).
func (h *Handler) resolveRepo(source string) *assets.ClipsRepository {
	switch source {
	case "youtube":
		return h.clipsRepo
	case "artlist":
		return h.artlistRepo
	case "stock":
		return h.stockRepo
	default:
		return nil
	}
}

// SetClipIndexer is a post-construction setter for late-binding the
// indexer service. SourcesHandler.SetClipIndexer delegates here.
func (h *Handler) SetClipIndexer(ci *clipindexer.Service) {
	h.clipIndexer = ci
}

// SetVectorStore is a post-construction setter for late-binding the vector
// store service. SourcesHandler.SetVectorStore delegates here.
func (h *Handler) SetVectorStore(vs *vectorstore.Service) {
	h.vectorStore = vs
}

// SetMetaWriter is a post-construction setter for late-binding the meta
// writer. SourcesHandler.SetMetaWriter delegates here.
func (h *Handler) SetMetaWriter(mw *semantic.MetadataWriter) {
	h.metaWriter = mw
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

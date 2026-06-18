package sources

import (
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/artifacts"
	"velox/go-master/internal/config"
	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/core/processor"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	"velox/go-master/internal/media/catalogsync"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/foldermemory"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/media/voiceover"
	voiceoversync "velox/go-master/internal/media/voiceoversync"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/sources/artlist"
	"velox/go-master/internal/sources/youtube"
	"velox/go-master/internal/storage/drivecleanup"
	"velox/go-master/internal/upload/drive"
)

// Handler handles common media operations.
type Handler struct {
	cfg            *config.Config
	artlistSvc     *artlist.Service
	youtubeSvc     *youtube.Service
	voiceoverSvc   *voiceover.Service
	jobsSvc        *jobservice.Service
	catalogRepo    *catalog.Repository
	assetIndexSvc  *assetindex.Service
	artlistRepo    *clips.Repository
	clipsRepo      *clips.Repository
	stockRepo      *clips.Repository
	voiceoverRepo  *voiceovers.Repository
	imagesRepo     *images.Repository
	cleanupSvc     *drivecleanup.Service
	folderMemSvc   *foldermemory.Service
	assetTreeSvc   *assettree.Service
	driveUploader  *drive.Uploader
	mediaProcessor processor.Processor
	deletionSvc    *media.DeletionService
	catalogSync    *catalogsync.Service
	maintenanceSvc *maintenance.Service
	realtimeSvc    *realtime.Service
	clipIndexer    *clipindexer.Service
	vectorStore    *vectorstore.Service
	metaWriter     *semantic.MetadataWriter
	artifactSvc    *artifacts.Service
	log            *zap.Logger

	// downloadCache prevents re-downloading the same YouTube video when
	// registering multiple segments (clips) from it. Key: videoID, Value: local path.
	downloadCache sync.Map

	// Sub-handlers
	Voiceover   *VoiceoverHandler
	SoundEffect *SoundEffectHandler
}

// SetRealtimeService sets the realtime service for semantic search.
func (h *Handler) SetRealtimeService(svc *realtime.Service) {
	h.realtimeSvc = svc
}

// SetClipIndexer sets the clip indexer for generating search_text/embeddings.
func (h *Handler) SetClipIndexer(ci *clipindexer.Service) {
	h.clipIndexer = ci
}

// SetVectorStore sets the vector store for Qdrant upsert after indexing.
func (h *Handler) SetVectorStore(vs *vectorstore.Service) {
	h.vectorStore = vs
}

// SetMetaWriter sets the unified metadata writer for semantic enrichment.
func (h *Handler) SetMetaWriter(mw *semantic.MetadataWriter) {
	h.metaWriter = mw
	if h.SoundEffect != nil {
		h.SoundEffect.metaWriter = mw
	}
}

// SetArtifactService sets the artifact service for content-addressed file storage.
func (h *Handler) SetArtifactService(svc *artifacts.Service) {
	h.artifactSvc = svc
}

// NewHandler creates a new common media handler.
func NewHandler(
	cfg *config.Config,
	artlistSvc *artlist.Service,
	youtubeSvc *youtube.Service,
	voiceoverSvc *voiceover.Service,
	voiceoverSync *voiceoversync.Service,
	jobsSvc *jobservice.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
	artlistRepo, clipsRepo, stockRepo *clips.Repository,
	cleanupSvc *drivecleanup.Service,
	folderMemSvc *foldermemory.Service,
	assetTreeSvc *assettree.Service,
	driveUploader *drive.Uploader,
	mediaProcessor processor.Processor,
	deletionSvc *media.DeletionService,
	catalogSync *catalogsync.Service,
	maintenanceSvc *maintenance.Service,
	log *zap.Logger,
) *Handler {
	h := &Handler{
		cfg:            cfg,
		artlistSvc:     artlistSvc,
		youtubeSvc:     youtubeSvc,
		voiceoverSvc:   voiceoverSvc,
		jobsSvc:        jobsSvc,
		catalogRepo:    catalogRepo,
		assetIndexSvc:  assetIndexSvc,
		artlistRepo:    artlistRepo,
		clipsRepo:      clipsRepo,
		stockRepo:      stockRepo,
		cleanupSvc:     cleanupSvc,
		folderMemSvc:   folderMemSvc,
		assetTreeSvc:   assetTreeSvc,
		driveUploader:  driveUploader,
		mediaProcessor: mediaProcessor,
		deletionSvc:    deletionSvc,
		catalogSync:    catalogSync,
		maintenanceSvc: maintenanceSvc,
		log:            log,
	}

	// Build the topic→folder resolver. Best-effort: nil-tolerated so a
	// broken resolver never crashes the whole handler chain; warn loudly
	// so operators see it in the boot logs.
	var groupsResolver *voiceover.GroupsResolver
	if assetTreeSvc != nil {
		gr, grErr := voiceover.NewGroupsResolver(assetTreeSvc, log)
		if grErr != nil {
			log.Warn("voiceover groups_resolver not initialized", zap.Error(grErr))
		} else {
			groupsResolver = gr
		}
	} else {
		log.Warn("voiceover groups_resolver not initialized: assetTreeSvc is nil (topic→folder routing disabled)")
	}

	// Require cfg.Drive.VoiceoverRootFolder to be explicitly set so the
	// groups_resolver and GroupsResolver can find the canonical voiceover
	// tree. No magic fallback: if the tree moves, the operator must
	// update config.yaml (keeps DB seed + handler in sync explicitly).
	defaultVoiceoverRoot := strings.TrimSpace(cfg.Drive.VoiceoverRootFolder)
	if defaultVoiceoverRoot == "" {
		log.Warn("voiceover groups_resolver DISABLED: cfg.Drive.VoiceoverRootFolder is empty",
			zap.String("env_var", "VELOX_DRIVE_VOICEOVER_ROOT"),
			zap.String("yaml_key", "drive.voiceover_root_folder"),
			zap.String("action", "set the config explicitly; topic→folder routing via /api/media/voiceover/groups will be unreachable"))
	} else {
		log.Info("voiceover groups_resolver enabled",
			zap.String("root", defaultVoiceoverRoot))
	}

	h.Voiceover = NewVoiceoverHandler(
		voiceoverSvc,
		voiceoverSync,
		jobsSvc,
		groupsResolver,
		defaultVoiceoverRoot,
		log,
	)
	h.SoundEffect = NewSoundEffectHandler(clipsRepo, driveUploader, h.metaWriter, cfg.Drive.SoundEffectsRootFolder, log)

	// Register job handlers for this package (bulk upload, etc.)
	if jobsSvc != nil {
		if err := h.RegisterJobHandlers(); err != nil {
			log.Warn("failed to register sources job handlers", zap.Error(err))
		}
	}

	return h
}

// SetVoiceoverRepo sets the voiceover repository.
func (h *Handler) SetVoiceoverRepo(repo *voiceovers.Repository) {
	h.voiceoverRepo = repo
}

// SetImagesRepo sets the images repository.
func (h *Handler) SetImagesRepo(repo *images.Repository) {
	h.imagesRepo = repo
}

// RegisterRoutes registers media routes with source parameter.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering common media routes")

	// Clip-level endpoints
	r.POST("/:source/clips", h.CreateClip)
	r.GET("/:source/clips/:id", h.GetClip)
	r.PATCH("/:source/clips/:id", h.UpdateClip)
	r.POST("/:source/clips/:id/status", h.ClipStatus)
	r.POST("/:source/clips/:id/verify", h.VerifyClip)
	r.POST("/:source/clips/:id/trash", h.TrashClip)
	r.POST("/:source/clips/:id/delete", h.DeleteClip)
	r.POST("/:source/clips/:id/reupload", h.ReuploadClip)
	r.POST("/:source/clips/:id/reprocess", h.ReprocessClip)
	r.POST("/:source/clips/:id/reindex", h.ReindexClip)
	r.POST("/enrich", h.EnrichMedia)
	r.POST("/enrich/batch", h.BatchReindex)
	r.GET("/:source/clips/:id/duplicates", h.FindDuplicates)
	r.GET("/:source/clips/:id/download", h.DownloadClip)
	r.POST("/:source/bulk/tags/add", h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", h.BulkRemoveTags)

	// Source-level endpoints
	r.GET("/search", h.Search)
	r.GET("/semantic-search", h.SemanticSearch)
	r.POST("/search/advanced", h.AdvancedSearch)
	r.POST("/recommend", h.RecommendClips)
	r.GET("/:source/clips", h.ListClips)
	r.POST("/:source/reconcile", h.Reconcile)
	r.POST("/:source/cleanup", h.Cleanup)
	r.GET("/:source/folders", h.ListFolders)
	r.GET("/:source/folders/:id", h.FolderStatus)
	r.POST("/:source/folders/:id/manifest", h.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", h.TrashFolder)
	r.POST("/:source/folders/:id/delete", h.DeleteFolder)
	r.GET("/:source/folders/:id/children", h.GetFolderChildren)
	r.GET("/:source/tree", h.GetTree)
	r.GET("/:source/breadcrumb", h.GetBreadcrumb)

	// Voiceover specific routes
	voiceover := r.Group("/voiceover")
	{
		h.Voiceover.RegisterRoutes(voiceover)
	}

	// SoundEffect specific routes
	sfxGroup := r.Group("/sound_effect")
	{
		h.SoundEffect.RegisterRoutes(sfxGroup)
	}

	// Video upload (multipart form — file + metadata)
	r.POST("/upload-video", h.UploadVideoClip)

	// Register from YouTube URL (download + metadata + Drive + Qdrant)
	r.POST("/register-from-youtube", h.RegisterFromYouTube)

	// Batch register multiple YouTube clips
	r.POST("/register-batch", h.BatchRegisterFromYouTube)

	// Bulk upload local .mp4 folders to Drive + DB + embeddings + Qdrant (async job)
	r.POST("/bulk-upload-youtube-clips", h.BulkUploadYouTubeClips)

	// System diagnostics
	r.GET("/diagnostics", h.GetDiagnostics)
	r.GET("/index-health", h.IndexHealth)

	// Qdrant maintenance
	r.POST("/qdrant/cleanup", h.QdrantCleanup)

	// Sync any Drive folder recursively into DB + asset tree + Qdrant.
	// POST /api/media/sync-drive-folder
	// Body: {"drive_folder_id":"...", "source":"youtube", "name":"MyFolder"}
	r.POST("/sync-drive-folder", h.SyncDriveFolder)

	// Upload local .mp4 + metadata.json to Drive (async job).
	// POST /api/media/local-to-drive
	r.POST("/local-to-drive", h.LocalToDrive)

	// Rename a file or folder on Google Drive.
	// POST /api/media/rename-drive-file
	r.POST("/rename-drive-file", h.RenameDriveFile)

	// Qdrant health probe — public, no auth.
	// GET /api/media/qdrant/health → { ok, healthy, enabled, error? }
	r.GET("/qdrant/health", h.QdrantHealth)

	// Move files between Drive folders (skip duplicates by name).
	// POST /api/media/drive/move-files
	r.POST("/drive/move-files", h.MoveDriveFiles)

	// Create multiple subfolders inside a parent Drive folder.
	// POST /api/media/drive/create-folders
	r.POST("/drive/create-folders", h.CreateDriveFolders)

	// Sync video files into per-video subfolders (videoID-title-slug).
	// POST /api/media/drive/sync-to-subfolders
	r.POST("/drive/sync-to-subfolders", h.SyncToSubfolders)
}

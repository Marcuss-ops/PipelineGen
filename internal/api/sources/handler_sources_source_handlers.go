// Package sources owns the legacy flat HTTP handlers for media sources
// (YouTube, Artlist, Stock, Voiceover, SoundEffect, Drive ops) and the
// SourcesHandler that routes them. PR-A Phase 4 BULK moved the clip
// surface into the clips subpackage; SourcesHandler now delegates every
// clip route via the single h.clips.RegisterRoutes(r) call and keeps
// only non-clip routes in this file.
package sources

import (
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	clipsources "github.com/Marcuss-ops/PipelineGen/internal/api/sources/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"

	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

// SourcesHandler handles non-clip media operations and routes the clip
// surface via the embedded clipsources.Handler.
//
// PR-A Phase 4 BULK: clipsDelete + clipsSearch subhandlers are now folded
// into the unified clipsources.Handler. SourcesHandler is a thin router
// for the legacy non-clip area (Voiceover, SoundEffect, recommend,
// register-from-youtube, Drive ops, diagnostics) and the holder of dep
// singletons that both sub-handlers and clips.Handler need.
type SourcesHandler struct {
	cfg            *config.Config
	voiceoverSvc   *voiceover.Service
	jobsSvc        *jobservice.Service
	catalogRepo    *catalog.Repository
	assetIndexSvc  *assetindex.Service
	artlistRepo    *sqlite.ClipsRepository
	clipsRepo      *sqlite.ClipsRepository
	stockRepo      *sqlite.ClipsRepository
	voiceoverRepo  *sqlite.VoiceoversRepository
	imagesRepo     *sqlite.ImagesRepository
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
	assetRepo      asset.Repository
	log            *zap.Logger

	// providerRegistry is the canonical providers.Registry populated by
	// the composition root (internal/app/registry.go::WireRegistry) AFTER
	// NewSourcesHandler returns, so it is wired via SetProviderRegistry
	// rather than the constructor. Search handlers fan out to every
	// registered SearchProvider via ByCapability dispatch. When nil,
	// source-level search returns no results (unit test mode).
	// Late-binding matches the existing Set* setter pattern.
	providerRegistry *providers.Registry

	// downloadCache prevents re-downloading the same YouTube video when
	// registering multiple segments (clips) from it. Key: videoID, Value: local path.
	downloadCache sync.Map

	// Sub-handlers
	Voiceover   *VoiceoverHandler
	SoundEffect *SoundEffectHandler
	// clips owns ALL clip routes via clipsources.Handler.RegisterRoutes.
	// SourcesHandler just delegates; no fan-out infra in this file.
	clips *clipsources.Handler
}

// SetRealtimeService sets the realtime service for semantic search.
func (h *SourcesHandler) SetRealtimeService(svc *realtime.Service) {
	h.realtimeSvc = svc
}

// SetClipIndexer sets the clip indexer for generating search_text/embeddings.
// Forwards to h.clips so the same instance is reachable from the unified
// clips.Handler. Late-binding after NewSourcesHandler is supported.
func (h *SourcesHandler) SetClipIndexer(ci *clipindexer.Service) {
	h.clipIndexer = ci
	if h.clips != nil {
		h.clips.SetClipIndexer(ci)
	}
}

// SetVectorStore sets the vector store for Qdrant upsert after indexing.
// Forwards to h.clips (same reach from clips.Handler).
func (h *SourcesHandler) SetVectorStore(vs *vectorstore.Service) {
	h.vectorStore = vs
	if h.clips != nil {
		h.clips.SetVectorStore(vs)
	}
}

// SetMetaWriter sets the unified metadata writer for semantic enrichment.
// Forwards to h.clips and to SoundEffect (legacy in-package wiring).
func (h *SourcesHandler) SetMetaWriter(mw *semantic.MetadataWriter) {
	h.metaWriter = mw
	if h.SoundEffect != nil {
		h.SoundEffect.metaWriter = mw
	}
	if h.clips != nil {
		h.clips.SetMetaWriter(mw)
	}
}

// SetArtifactService sets the artifact service for content-addressed file drive.
// Forwards to h.clips so UploadVideoClip's content-addressed blob storage
// sees the late-bound service. Same late-binding semantics as the other
// Set* setters that delegate.
func (h *SourcesHandler) SetArtifactService(svc *artifacts.Service) {
	h.artifactSvc = svc
	if h.clips != nil {
		h.clips.SetArtifactSvc(svc)
	}
}

// SetAssetRepo sets the canonical asset repository (replaces sqlite.ClipsRepository
// for handlers that have been migrated to use asset.Asset directly). The
// assetRepo lives only on SourcesHandler because clips.Handler received it
// at construction time via Deps — late rebinding here does NOT update
// h.clips (the clips.Handler assetRepo is unchanged). Callers that need to
// rewire asset.Repository across both must rebuild h.clips; in practice
// assetRepo is set once at startup so this limitation is theoretical.
func (h *SourcesHandler) SetAssetRepo(repo asset.Repository) {
	h.assetRepo = repo
}

// SetVoiceoverRepo sets the voiceover repository. Forwards to h.clips so
// DownloadClip's voiceover-source branch sees the late-bound repo. Both
// handlers hold the same reference; switching repos mid-process picks up
// on the next handler invocation.
func (h *SourcesHandler) SetVoiceoverRepo(repo *sqlite.VoiceoversRepository) {
	h.voiceoverRepo = repo
	if h.clips != nil {
		h.clips.SetVoiceoverRepo(repo)
	}
}

// SetImagesRepo sets the images repository. clips.Handler reads it in
// ListClips for the "source=images" branch, so this setter forwards to
// h.clips as well as Self.imagesRepo (legacy sources/ search_handlers.go,
// semantic_handlers.go also use it directly).
func (h *SourcesHandler) SetImagesRepo(repo *sqlite.ImagesRepository) {
	h.imagesRepo = repo
	if h.clips != nil {
		h.clips.SetImagesRepo(repo)
	}
}

// SetProviderRegistry wires the canonical providers.Registry after
// composition. Search handlers (handler_sources_search_handlers.go)
// use ByCapability(CapabilitySearch) dispatch when wired; when nil,
// source-level search returns no results.
// Late-binding matches the existing Set* setter pattern (see
// SetRealtimeService, SetClipIndexer, etc. above); the registry is
// constructed AFTER WireAssets in internal/app/registry.go so a
// constructor parameter would not work without reordering wiring.
func (h *SourcesHandler) SetProviderRegistry(reg *providers.Registry) {
	h.providerRegistry = reg
}

// SetFolderMemSvc sets the folder memory service. clips.Handler reads it
// in RegenerateManifest for the folder heuristic. sources/ search_handlers.go
// also uses folderMemSvc directly for legacy path resolution.
func (h *SourcesHandler) SetFolderMemSvc(svc *foldermemory.Service) {
	h.folderMemSvc = svc
	if h.clips != nil {
		h.clips.SetFolderMemSvc(svc)
	}
}

// NewSourcesHandler creates a new common media handler. The clips.Handler
// embedded in this SourcesHandler is wired from the same dep singletons
// so it shares the repos/uploads/jobs as the legacy in-package methods.
//
// Source-level search dispatch uses the providers.Registry (wired later
// via SetProviderRegistry). The youtube/artlist singletons are no longer
// stored directly — their adapters register in the registry instead.
//
// params:
//
//	cfg, voiceoverSvc, voiceoverSync, jobsSvc,
//	catalogRepo, assetIndexSvc, artlistRepo, clipsRepo, stockRepo,
//	cleanupSvc, folderMemSvc, assetTreeSvc, driveUploader,
//	mediaProcessor, deletionSvc, catalogSync, maintenanceSvc, log
func NewSourcesHandler(
	cfg *config.Config,
	voiceoverSvc *voiceover.Service,
	voiceoverSync *voiceoversync.Service,
	jobsSvc *jobservice.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
	artlistRepo, clipsRepo, stockRepo *sqlite.ClipsRepository,
	cleanupSvc *drivecleanup.Service,
	folderMemSvc *foldermemory.Service,
	assetTreeSvc *assettree.Service,
	driveUploader *drive.Uploader,
	mediaProcessor processor.Processor,
	deletionSvc *media.DeletionService,
	catalogSync *catalogsync.Service,
	maintenanceSvc *maintenance.Service,
	log *zap.Logger,
) *SourcesHandler {
	h := &SourcesHandler{
		cfg:            cfg,
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

	// PR-A Phase 4 BULK: build the unified clips.Handler with all 15 deps.
	// voiceoverRepo is set post-construction by SetVoiceoverRepo when
	// composition reaches that wiring point; nil here is fine because
	// every clip.Handler method that touches it nil-checks first.
	h.clips = clipsources.NewHandler(clipsources.Deps{
		AssetRepo:      h.assetRepo,
		ClipsRepo:      h.clipsRepo,
		StockRepo:      h.stockRepo,
		ArtlistRepo:    h.artlistRepo,
		DeletionSvc:    h.deletionSvc,
		DriveUploader:  h.driveUploader,
		MediaProcessor: h.mediaProcessor,
		AssetTreeSvc:   h.assetTreeSvc,
		MetaWriter:     h.metaWriter,
		ClipIndexer:    h.clipIndexer,
		VectorStore:    h.vectorStore,
		JobsSvc:        h.jobsSvc,
		Cfg:            h.cfg,
		Log:            h.log,
		VoiceoverRepo:  h.voiceoverRepo,
		ImagesRepo:     h.imagesRepo,
		ArtifactSvc:    h.artifactSvc,
		FolderMemSvc:   h.folderMemSvc,
	})

	// Register job handlers for this package (bulk upload, etc.)
	if jobsSvc != nil {
		if err := h.RegisterJobHandlers(); err != nil {
			log.Warn("failed to register sources job handlers", zap.Error(err))
		}
	}

	return h
}

// RegisterJobHandlers delegates to h.clips.RegisterJobHandlers.
// Kept on SourcesHandler so composition sites (init_core.go /
// compose_media.go) keep their existing h.RegisterJobHandlers() call
// shape without churn.
func (h *SourcesHandler) RegisterJobHandlers() error {
	if h.clips == nil {
		return nil
	}
	return h.clips.RegisterJobHandlers()
}

// Clips exposes the embedded clips.Handler for callers (mainly tests /
// integration suites) that want to drive clip routes through the same
// receiver the router uses. Returns nil if NewSourcesHandler has not run.
func (h *SourcesHandler) Clips() *clipsources.Handler {
	return h.clips
}

// RegisterRoutes registers media routes with source parameter.
//
// PR-A Phase 4 BULK: h.clips.RegisterRoutes(r) covers the entire clip
// route surface in one delegation call. SourcesHandler only registers
// non-clip routes (voiceover/sound_effect sub-routes, Drive ops,
// register-from-youtube, diagnostics, qdrant ops).
func (h *SourcesHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering common media routes")

	// Clip routes — single delegation to the unified clips.Handler.
	// This drops: CreateClip, GetClip, UpdateClip, TrashClip, DeleteClip,
	// VerifyClip, ReuploadClip, ReprocessClip, ReindexClip, ListClips,
	// FoldBrowse (List/FolderStatus/RegenerateManifest/TrashFolder/
	// DeleteFolder/GetFolderChildren/GetTree/GetBreadcrumb), BulkAddTags,
	// BulkRemoveTags, Reconcile, Cleanup, EnrichMedia, BatchReindex,
	// UploadVideoClip, AdvancedSearch, FindDuplicates, DownloadClip.
	// All of those routes are now registered exactly once here, with
	// the receiver owned by h.clips (single source of truth).
	if h.clips != nil {
		h.clips.RegisterRoutes(r)
	}

	// Source-level non-clip endpoints
	r.GET("/search", h.Search)
	r.GET("/semantic-search", h.SemanticSearch)
	r.POST("/recommend", h.RecommendClips)

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

	// Register from YouTube URL (download + metadata + Drive + Qdrant)
	r.POST("/register-from-youtube", h.RegisterFromYouTube)

	// Batch register multiple YouTube clips
	r.POST("/register-batch", h.BatchRegisterFromYouTube)

	// Bulk upload local .mp4 folders to Drive + DB + embeddings + Qdrant (async job).
	// Delegate to clipsources.Handler.BulkUploadYouTubeClips (the worker function
	// and route body live in the clips subpackage post Phase 4 BULK).
	if h.clips != nil {
		r.POST("/bulk-upload-youtube-clips", h.clips.BulkUploadYouTubeClips)
	}

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
